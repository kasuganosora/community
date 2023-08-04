package public

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		if event.Data == nil {
			return false
		}
		status := event.Data.(*models.Status)
		if status == nil {
			return false
		}

		//如果是新增的事件
		switch event.Name {
		case "after_create_status":
			go Push(status, false, true)
		case "before_update_status":
			go Unpush(status, true) // 更新之前，先删除
		case "after_update_status":
			go Push(status, true, true)
		case "after_delete_status":
			go Unpush(status, true)
		}

		return true
	}})
}

type PublicTimeline struct {
	OnlyMedia bool
}

func (timeline *PublicTimeline) GetKeyFormat() string {
	return "feed:public:%s"
}

func (timeline *PublicTimeline) GetTimelineId() string {
	if timeline.OnlyMedia {
		return "has_media"
	}
	return "public"
}

func (timeline *PublicTimeline) Rebuild() (err error) {
	statuses := make(models.Statuses, 0)
	err = db.GetDB().Model(&models.Status{}).Preload("Attachments").
		Where("visibility = ?", models.StatusVisibilityPublic).
		Order("id desc").Limit(feed.MAX_ITEMS / 2).Find(&statuses).Error

	if err != nil {
		return
	}

	allPublicTimeline := &PublicTimeline{OnlyMedia: false}
	onlyMediaTimeline := &PublicTimeline{OnlyMedia: true}

	if err = feed.CleanFeeds(allPublicTimeline, []string{"feed:public:public"}); err != nil {
		return
	}

	if err = feed.CleanFeeds(onlyMediaTimeline, []string{"feed:public:has_media"}); err != nil {
		return
	}

	for _, status := range statuses {
		if status.Attachments != nil && len(status.Attachments) > 0 {
			if err = feed.AddToTimelineItem(onlyMediaTimeline, status, true); err != nil {
				return
			}

			if err = feed.TrimTimeline(onlyMediaTimeline); err != nil {
				return
			}
		}

		if err = feed.AddToTimelineItem(allPublicTimeline, status, true); err != nil {
			return
		}

		if err = feed.TrimTimeline(allPublicTimeline); err != nil {
			return
		}

	}

	return
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	if status.Visibility != models.StatusVisibilityPublic {
		return
	}

	timelines := make([]feed.Timeline, 0)
	timelines = append(timelines, &PublicTimeline{OnlyMedia: false})
	// 如果有附件，就推送到has_media
	if status.Attachments != nil && len(status.Attachments) > 0 {
		timelines = append(timelines, &PublicTimeline{OnlyMedia: true})
	}

	for _, timeline := range timelines {
		err = feed.AddToTimelineItem(timeline, status, true)
		if err != nil {
			return
		}

		err = feed.TrimTimeline(timeline)
		if err != nil {
			return
		}

		if feed.PushUpdateRequired(timeline) && pushToClient {
			method := "update" // https://docs.joinmastodon.org/methods/streaming/ 上说 创建是 update，更新是 status.update
			if update {
				method = "status.update"
			}

			err = feed.PushToClient(timeline, method, status)

			if err != nil {
				return
			}
		}
	}

	return
}

func Unpush(status *models.Status, pushToClient bool) (err error) {
	timelines := make([]feed.Timeline, 0)
	timelines = append(timelines, &PublicTimeline{OnlyMedia: false})
	// 如果有附件，就推送到has_media
	if status.Attachments != nil && len(status.Attachments) > 0 {
		timelines = append(timelines, &PublicTimeline{OnlyMedia: true})
	}

	for _, timeline := range timelines {
		err = feed.RemoveTimelineItem(timeline, status, true)
		if err != nil {
			return
		}

		err = feed.TrimTimeline(timeline)
		if err != nil {
			return
		}

		if feed.PushUpdateRequired(timeline) && pushToClient {
			err = feed.PushToClient(timeline, "delete", status)
			if err != nil {
				return
			}
		}
	}

	return
}

func GetTimelineContentAll(params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &PublicTimeline{OnlyMedia: false}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("visibility = ?", models.StatusVisibilityPublic)

		if params.Other["local"] != nil && params.Other["local"].(bool) {
			query = query.Where("statuses.domain is null")
		} else if params.Other["remote"] != nil && params.Other["remote"].(bool) {
			query = query.Where("statuses.domain not is null")
		}

		return
	}

	afterFilter := func(statuses models.Statuses) (ret models.Statuses) {
		var remote, local bool
		if _remote, ok := params.Other["remote"]; ok {
			remote = _remote.(bool)
		}

		if _local, ok := params.Other["local"]; ok {
			local = _local.(bool)
		}

		if !remote && !local {
			return statuses
		}

		return statuses.Filter(func(status *models.Status) bool {
			if remote && status.Domain == nil {
				return false
			}

			if local && status.Domain != nil {
				return false
			}

			return true
		})
	}

	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, afterFilter)
}

func GetTimelineContentMedia(params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &PublicTimeline{OnlyMedia: true}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("visibility = ?", models.StatusVisibilityPublic)

		if params.Other["local"] != nil && params.Other["local"].(bool) {
			query = query.Where("statuses.domain is null")
		} else if params.Other["remote"] != nil && params.Other["remote"].(bool) {
			query = query.Where("statuses.domain not is null")
		}

		return
	}

	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}
