package feed

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

var ErrQueueCapacity = errors.New("feed delivery queue is at capacity")

var admissionState struct {
	sync.Mutex
	checked time.Time
	err     error
}

func queueLimit(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		value = config.C.Other[name]
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return fallback
	}
	return min(n, 10_000_000)
}

// AdmitWrite checks a cached queue budget before status-producing requests.
// Normal concurrent requests share at most one query pair per API process per
// second. This is soft backpressure, not a globally reserved hard capacity;
// simultaneous API processes may overshoot within that observation window.
// Persistence failures still roll back the business transaction.
func AdmitWrite(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !models.FeedOutboxRequired() {
		return nil
	}
	return admitWrite(ctx)
}

func admitWrite(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	admissionState.Lock()
	defer admissionState.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Since(admissionState.checked) < time.Second {
		return admissionState.err
	}
	requestContext := ctx
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn := db.GetDB().WithContext(ctx)
	var ids []uint64
	err := conn.Model(&models.FeedEvent{}).Where("state IN ?", []string{"pending", "processing", "failed"}).Offset(queueLimit("FEED_MAX_PENDING_EVENTS", 100000)-1).Limit(1).Pluck("id", &ids).Error
	if err == nil && len(ids) > 0 {
		err = ErrQueueCapacity
	}
	if err == nil {
		err = conn.Model(&models.FeedBatch{}).Where("state IN ?", []string{"pending", "processing", "failed"}).Offset(queueLimit("FEED_MAX_PENDING_BATCHES", 1000000)-1).Limit(1).Pluck("id", &ids).Error
		if err == nil && len(ids) > 0 {
			err = ErrQueueCapacity
		}
	}
	if requestContext.Err() != nil {
		return requestContext.Err()
	}
	admissionState.checked = time.Now()
	admissionState.err = err
	return err
}
