package notification

import (
	"context"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/redis/go-redis/v9"
	"log"
	"strconv"
	"time"
)

func init() {
	// 监听 所有关于notification表的创建事件
	event.Register("notification", "after_create", &event.Handler{Exec: func(event event.Event) bool {
		if feed.DurableFanoutEnabled() {
			return true
		}
		if event.Data == nil {
			return false
		}

		notification := event.Data.(*models.Notification) //nolint:forcetypeassert
		if notification == nil {
			return false
		}

		// 如果是新增的事件
		go func() {
			err := Push(notification, true, true)
			if err != nil {
				log.Printf("notification_timeline: Push failed: %v", err)
			}
		}()

		return true
	}})
}

type NotificationTimeline struct {
	Account *models.Account
}

func (timeline *NotificationTimeline) GetKeyFormat() string {
	return "feed:notification:%s"
}

func (timeline *NotificationTimeline) GetTimelineId() string {
	return strconv.FormatUint(timeline.Account.Id, 10)
}

func (timeline *NotificationTimeline) Rebuild() error {
	return timeline.RebuildContext(context.Background())
}
func (timeline *NotificationTimeline) RebuildContext(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	lease, err := feed.BeginRebuild(ctx, timeline)
	if err != nil {
		return err
	}
	defer feed.ReleaseRebuild(ctx, timeline, lease)
	var ids []uint64
	if err = db.GetDB().WithContext(ctx).Model(&models.Notification{}).Where("account_id = ?", timeline.Account.Id).Order("id desc").Limit(feed.MAX_ITEMS).Pluck("id", &ids).Error; err != nil {
		return err
	}
	entries := make(models.Statuses, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, &models.Status{Id: id})
	}
	return feed.PublishRebuild(ctx, timeline, lease, entries, false)
}

func (timeline *NotificationTimeline) push(notification *models.Notification, pushToClient bool, trim bool) (err error) {
	ctx := context.Background()
	err = db.GetRedis().ZAdd(ctx, fmt.Sprintf(timeline.GetKeyFormat(), timeline.GetTimelineId()), redis.Z{
		Score:  float64(notification.Id),
		Member: notification.Id,
	}).Err()
	if err != nil {
		return
	}

	if pushToClient && feed.PushUpdateRequired(timeline) {
		var payload *serializer.Notification

		payload, err = serializer.ToNotification(notification, timeline.Account)
		if err != nil {
			return
		}

		// An invisible/incomplete notification is not streamed, but the
		// feed must still be trimmed.
		if payload != nil {
			err = feed.PushToClient(timeline, "notification", payload)
			if err != nil {
				return
			}
		}
	}

	if trim {
		timeline.trim()
	}

	return
}

func (timeline *NotificationTimeline) trim() {
	ctx := context.Background()
	_ = db.GetRedis().ZRemRangeByRank(ctx, fmt.Sprintf(timeline.GetKeyFormat(), timeline.GetTimelineId()), 0, -feed.MAX_ITEMS).Err()
}

func Push(notification *models.Notification, pushToClient bool, trim bool) (err error) {
	if notification.Account == nil {
		database := db.GetDB()
		if database == nil {
			return errors.New("database connection is nil")
		}

		err = notification.LoadAllExtendFields(database)
		if err != nil {
			return
		}
	}

	timeline := &NotificationTimeline{Account: notification.Account}
	err = timeline.push(notification, pushToClient, trim)

	return
}
