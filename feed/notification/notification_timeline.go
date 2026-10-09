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
)

func init() {
	// 监听 所有关于notification表的创建事件
	event.Register("notification", "after_create", &event.Handler{Exec: func(event event.Event) bool {
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

func (timeline *NotificationTimeline) Rebuild() (err error) {
	notificationIds := make([]uint64, 0)

	err = db.GetDB().Model(&models.Notification{}).Where("account_id = ?", timeline.Account.Id).
		Order("id desc").
		Limit(feed.MAX_ITEMS/2).
		Pluck("id", &notificationIds).Error
	if err != nil {
		return
	}

	ctx := context.Background()
	timelineKey := fmt.Sprintf(timeline.GetKeyFormat(), timeline.GetTimelineId())

	// 清理旧的 timeline 数据
	if delErr := db.GetRedis().Del(ctx, timelineKey).Err(); delErr != nil {
		err = delErr
		return
	}

	for _, nId := range notificationIds {
		n := &models.Notification{Id: nId}
		err = timeline.push(n, false, false)
		if err != nil {
			return
		}
	}

	return
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
