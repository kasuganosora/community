package serve

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db/redis"
)

// failureStore shares the failure memo between processes and restarts.
type failureStore interface {
	mark(key string, rec failureRecord)
	failed(key string) (failureRecord, bool)
}

const redisFailureTimeout = 100 * time.Millisecond

type redisFailureStore struct{ c *cache.Cache }

// The value is "status|class|upstream status"; the key's TTL is the memo
// lifetime. A bare "1" was written before the answer was remembered.
func (s redisFailureStore) mark(key string, rec failureRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), redisFailureTimeout)
	defer cancel()
	value := strconv.Itoa(rec.status) + "|" + rec.class + "|" + strconv.Itoa(rec.upstream)
	_ = s.c.SetString(ctx, "mediaproxy:fail:"+key, value, rec.ttlLeft())
}

func (s redisFailureStore) failed(key string) (failureRecord, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), redisFailureTimeout)
	defer cancel()
	value, err := s.c.GetString(ctx, "mediaproxy:fail:"+key)
	if err != nil || value == "" {
		return failureRecord{}, false
	}
	ttl, err := s.c.TTL(ctx, "mediaproxy:fail:"+key)
	if err != nil || ttl <= 0 {
		ttl = transientFailureTTL
	}
	rec := parseFailureValue(value)
	rec.expires = now().Add(ttl)
	return rec, true
}

func parseFailureValue(value string) failureRecord {
	rec := failureRecord{status: http.StatusBadGateway, class: classFetch}
	parts := strings.SplitN(value, "|", 3)
	if len(parts) != 3 {
		return rec
	}
	if status, err := strconv.Atoi(parts[0]); err == nil && status >= 400 && status < 600 {
		rec.status = status
	}
	if parts[1] != "" {
		rec.class = parts[1]
	}
	rec.upstream, _ = strconv.Atoi(parts[2])
	return rec
}

func redisFailures() failureStore {
	if len(redis.GetRedises()) == 0 {
		return nil
	}
	return redisFailureStore{c: cache.GetCache()}
}

// stallReader re-arms timer on every read, so a relay is cancelled only
// when the upstream stops sending, not when a long video simply plays.
type stallReader struct {
	r     io.Reader
	timer *time.Timer
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(stallTimeout)
	}
	return n, err
}
