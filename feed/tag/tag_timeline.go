package tag

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
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
		case "after_create":
			go Push(status, false, true)
		case "before_update":
			go Unpush(status, true) // 更新之前，先删除
		case "after_update":
			go Push(status, true, true)
		case "after_delete":
			go Unpush(status, true)
		}

		return true
	}})
}

type TagTimeline struct {
	Tag string
}

func (timeline *TagTimeline) GetKeyFormat() string {
	return "feed:tag:%s"
}

func (timeline *TagTimeline) GetTimelineId() string {
	return timeline.Tag
}

func (timeline *TagTimeline) Rebuild() (err error) {
	statuses := make(models.Statuses, 0)
	tag := &models.Tag{}
	err = db.GetDB().Model(&models.Tag{}).Where("name = ?", timeline.Tag).First(tag).Error

	if err != nil {
		return
	}

	err = feed.CleanFeeds(timeline, []string{timeline.GetTimelineId()})
	if err != nil {
		return
	}

	err = db.GetDB().Model(&models.Status{}).
		Where("id in (?)", db.GetDB().Model(&models.StatusTag{}).Where("tag_id = ?", tag.Id).Select("status_id")).
		Order("id desc").Limit(feed.MAX_ITEMS / 2).
		Find(&statuses).Error

	if err != nil {
		return
	}

	for _, status := range statuses {
		if err = feed.AddToTimelineItem(timeline, status, true); err != nil {
			return
		}
		if err = feed.TrimTimeline(timeline); err != nil {
			return
		}
	}

	return
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	// 取得这个status有那些tag
	if err = getTags(status); err != nil {
		return
	}

	for _, tag := range status.Tags {
		timeline := &TagTimeline{Tag: tag.Name}
		if err = feed.AddToTimelineItem(timeline, status, true); err != nil {
			return
		}
		if err = feed.TrimTimeline(timeline); err != nil {
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
	// 取得这个status有那些tag
	if err = getTags(status); err != nil {
		return
	}

	for _, tag := range status.Tags {
		timeline := &TagTimeline{Tag: tag.Name}
		if err = feed.RemoveTimelineItem(timeline, status, true); err != nil {
			return
		}
		if err = feed.TrimTimeline(timeline); err != nil {
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

func getTags(status *models.Status) (err error) {
	if status.Tags != nil {
		return
	}

	status.Tags = make(models.Tags, 0)
	err = db.GetDB().Model(&models.Status{}).Association("Tags").Find(&status.Tags)

	return
}

func GetTimelineContent(tag string, account *models.Account, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &TagTimeline{Tag: tag}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query.Where("id in (?)", db.GetDB().Model(&models.StatusTag{}).Where("tag_id = ?", tag).Select("status_id"))
		if isLocal := params.Other["local"]; isLocal != nil && isLocal.(bool) {
			query.Where("domain is nul")
		} else if isRemote := params.Other["remote"]; isRemote != nil && isRemote.(bool) {
			query.Where("domain is not null")
		}

		return
	}

	followingIds := make([]uint64, 0)
	if account != nil {
		err = db.GetDB().Model(&models.Follows{}).Where("account_id = ?", account.Id).Select("follow_id").Find(&followingIds).Error
		if err != nil {
			return
		}
	}

	followingCheckMap := utils.GetExistsMap(followingIds)

	afterFilter := func(statuses models.Statuses) models.Statuses {
		ret := make(models.Statuses, 0, len(statuses))

		for _, status := range statuses {
			if onlyMedia, ok := params.Other["only_media"]; ok && onlyMedia.(bool) && len(status.Attachments) == 0 {
				continue
			}

			// 没有登录的情况下，只能看到公开的
			if account == nil && status.Visibility != models.StatusVisibilityPublic {
				continue
			}

			if status.Visibility == models.StatusVisibilityDirect && status.ToAccounts.Contains(account.Id) {
				ret = append(ret, status)
				continue
			}

			// 如果是私有的那么只有关注的人才能看到
			if status.Visibility == models.StatusVisibilityPrivate && followingCheckMap[status.AccountId] {
				ret = append(ret, status)
				continue
			}

			// 公开的，所有人都能看到
			if status.Visibility == models.StatusVisibilityPublic {
				ret = append(ret, status)
				continue
			}
		}

		return ret
	}

	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, afterFilter)
}
