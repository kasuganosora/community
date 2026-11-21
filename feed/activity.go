package feed

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

type activityTimeline struct{ kind, id string }

func (t activityTimeline) GetKeyFormat() string  { return "feed:" + t.kind + ":%s" }
func (t activityTimeline) GetTimelineId() string { return t.id }
func (t activityTimeline) Rebuild() error        { return ErrNeedsRebuild }

var ErrActivityPending = errors.New("reader activation is not complete")

type activityCall struct {
	done chan struct{}
	err  error
}

type activityBucket struct {
	mu    sync.Mutex
	calls map[string]*activityCall
}

var activityBuckets [64]activityBucket

func coalesceActivity(ctx context.Context, flightKey string, fn func() error) error {
	h := fnv.New32a()
	_, _ = h.Write([]byte(flightKey))
	b := &activityBuckets[h.Sum32()&63]
	b.mu.Lock()
	if b.calls == nil {
		b.calls = make(map[string]*activityCall)
	}
	if call := b.calls[flightKey]; call != nil {
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-call.done:
			return call.err
		}
	}
	call := &activityCall{done: make(chan struct{})}
	b.calls[flightKey] = call
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.calls, flightKey)
		close(call.done)
		b.mu.Unlock()
	}()
	call.err = fn()
	return call.err
}

// Single-RTT atomic check+claim: returns 1 when already active ("done"),
// 0 when another worker owns an unfinished activation token, or 2 when claimed.
const recordActivityClaimScript = `
local v=redis.call('GET',KEYS[1])
if v=='done' then return 1 end
if v then return 0 end
redis.call('SET',KEYS[1],ARGV[1],'EX',2)
return 2
`

// RecordActivity counts authenticated reads. A short owner token is distinct
// from the completed throttle marker: a racing reader must use SQL until the
// returning reader's old index has actually been invalidated.
func RecordActivity(ctx context.Context, account *models.Account) error {
	if account == nil || account.Id == 0 || account.Domain != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	client := db.GetUniversalRedis()
	flightKey := fmt.Sprintf("%p:%d", client, account.Id)
	return coalesceActivity(ctx, flightKey, func() error {
		actCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		key := "feed:activity:v2:" + strconv.FormatUint(account.Id, 10)
		token := rand.Text()
		state, err := client.Eval(actCtx, recordActivityClaimScript, []string{key}, token).Int()
		if err != nil {
			return err
		}
		if state == 1 {
			return nil
		}
		if state == 0 {
			return ErrActivityPending
		}
		complete := false
		defer func() {
			if complete {
				return
			}
			// A timed-out request still releases only its own token. Never delete a
			// successor's claim, or retain a five-minute false-success marker.
			cleanup, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer stop()
			_ = client.Eval(cleanup, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{key}, token).Err()
		}()
		conn := db.GetDB().WithContext(actCtx)
		var stored models.Account
		if err = conn.Select("id, feed_last_seen_at").Where("id = ? AND domain IS NULL", account.Id).First(&stored).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if stored.FeedLastSeenAt == nil || now.Sub(*stored.FeedLastSeenAt) > 10*24*time.Hour {
			for _, kind := range []string{"home", "mention"} {
				if err = InvalidateTimelineContext(actCtx, activityTimeline{kind, strconv.FormatUint(account.Id, 10)}); err != nil {
					return err
				}
			}
		}
		if err = conn.Model(&models.Account{}).Where("id = ? AND (feed_last_seen_at IS NULL OR feed_last_seen_at < ?)", account.Id, now.Add(-5*time.Minute)).UpdateColumn("feed_last_seen_at", now).Error; err != nil {
			return err
		}
		result, err := client.Eval(actCtx, `if redis.call('GET',KEYS[1])==ARGV[1] then redis.call('SET',KEYS[1],'done','EX',300);return 1 end return 0`, []string{key}, token).Int()
		if err != nil {
			return err
		}
		if result != 1 {
			return ErrActivityPending
		}
		complete = true
		return nil
	})
}
