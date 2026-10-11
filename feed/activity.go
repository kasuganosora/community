package feed

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

type activityTimeline struct{ kind, id string }

func (t activityTimeline) GetKeyFormat() string  { return "feed:" + t.kind + ":%s" }
func (t activityTimeline) GetTimelineId() string { return t.id }
func (t activityTimeline) Rebuild() error        { return ErrNeedsRebuild }

var ErrActivityPending = errors.New("reader activation is not complete")

// RecordActivity counts authenticated reads. A short owner token is distinct
// from the completed throttle marker: a racing reader must use SQL until the
// returning reader's old index has actually been invalidated.
func RecordActivity(ctx context.Context, account *models.Account) error {
	if account == nil || account.Id == 0 || account.Domain != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	client := db.GetUniversalRedis()
	key := "feed:activity:v2:" + strconv.FormatUint(account.Id, 10)
	token := rand.Text()
	claimed, err := client.SetNX(ctx, key, token, 2*time.Second).Result()
	if err != nil {
		return err
	}
	if !claimed {
		value, err := client.Get(ctx, key).Result()
		if err != nil {
			return err
		}
		if value == "done" {
			return nil
		}
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
	conn := db.GetDB().WithContext(ctx)
	var stored models.Account
	if err = conn.Select("id, feed_last_seen_at").Where("id = ? AND domain IS NULL", account.Id).First(&stored).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	if stored.FeedLastSeenAt == nil || now.Sub(*stored.FeedLastSeenAt) > 10*24*time.Hour {
		for _, kind := range []string{"home", "mention"} {
			if err = InvalidateTimelineContext(ctx, activityTimeline{kind, strconv.FormatUint(account.Id, 10)}); err != nil {
				return err
			}
		}
	}
	if err = conn.Model(&models.Account{}).Where("id = ? AND (feed_last_seen_at IS NULL OR feed_last_seen_at < ?)", account.Id, now.Add(-5*time.Minute)).UpdateColumn("feed_last_seen_at", now).Error; err != nil {
		return err
	}
	result, err := client.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then redis.call('SET',KEYS[1],'done','EX',300);return 1 end return 0`, []string{key}, token).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return ErrActivityPending
	}
	complete = true
	return nil
}
