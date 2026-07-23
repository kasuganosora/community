package tag

import (
	"log"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"strings"
)

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		if event.Data == nil {
			return false
		}

		status := event.Data.(*models.Status) //nolint:forcetypeassert
		if status == nil {
			return false
		}

		// 不展示 私信
		if status.Visibility == models.StatusVisibilityDirect {
			return true
		}

		// 如果是新增的事件
		switch event.Name {
		case "after_create":
			go func() {
				err := Push(status, false, true)
				if err != nil {
					log.Printf("tag_timeline: Push failed: %v", err)
				}
			}()
		case "before_update":
			go func() { // 更新之前，先删除
				err := Unpush(status, true)
				if err != nil {
					log.Printf("tag_timeline: Unpush failed: %v", err)
				}
			}()
		case "after_update":
			go func() {
				err := Push(status, true, true)
				if err != nil {
					log.Printf("tag_timeline: Push failed: %v", err)
				}
			}()
		case "after_delete":
			go func() {
				err := Unpush(status, true)
				if err != nil {
					log.Printf("tag_timeline: Unpush failed: %v", err)
				}
			}()
		}

		return true
	}})
}

type TagTimeline struct {
	Tag        string
	OnlyLocal  bool
	OnlyRemote bool
	OnlyMedia  bool
}

func (timeline *TagTimeline) GetKeyFormat() string {
	return "feed:hashtag:%s"
}

func (timeline *TagTimeline) GetTimelineId() string {
	strs := make([]string, 0)
	if timeline.OnlyLocal {
		strs = append(strs, "local")
	}

	if timeline.OnlyRemote {
		strs = append(strs, "remote")
	}

	if timeline.OnlyMedia {
		strs = append(strs, "media")
	}

	strs = append(strs, timeline.Tag)

	return strings.Join(strs, ":")
}

func (timeline *TagTimeline) Rebuild() (err error) {
	statuses := make(models.Statuses, 0)
	tag := &models.Tag{}

	err = db.GetDB().Model(&models.Tag{}).Where("name = ?", timeline.Tag).First(tag).Error
	if err != nil {
		return err
	}

	err = feed.CleanFeeds(timeline, []string{timeline.GetTimelineId()})
	if err != nil {
		return err
	}

	err = db.GetDB().Model(&models.Status{}).
		Where("id in (?)", db.GetDB().Model(&models.StatusTag{}).Where("tag_id = ?", tag.Id).Select("status_id")).
		Order("id desc").Limit(feed.MAX_ITEMS / 2).
		Find(&statuses).Error
	if err != nil {
		return err
	}

	for _, status := range statuses {
		err = feed.AddToTimelineItem(timeline, status, true)
		if err != nil {
			return err
		}

		err = feed.TrimTimeline(timeline)

		if err != nil {
			return err
		}
	}

	return err
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	// 取得这个status有那些tag
	if err = getTags(status); err != nil {
		return err
	}

	if len(status.Tags) == 0 {
		return err
	}

	method := "update" // https://docs.joinmastodon.org/methods/streaming/ 上说 创建是 update，更新是 status.update
	if update {
		method = "status.update"
	}

	isReblog := status.ReblogId != nil && *status.ReblogId > 0

	if !isReblog {
		// 非转发：使用批量添加优化性能（Redis Pipeline）
		timelines := make([]feed.Timeline, 0, len(status.Tags))
		for _, tag := range status.Tags {
			timelines = append(timelines, &TagTimeline{Tag: tag.Name})
		}

		err = feed.BatchAddToTimelineItems(timelines, status, true)

		if err != nil {
			return err
		}

		for _, timeline := range timelines {
			err = feed.TrimTimeline(timeline)
			if err != nil {
				return err
			}
		}
	} else {
		// 转发：保留单条调用以保证 reblog 聚合逻辑正确
		for _, tag := range status.Tags {
			timeline := &TagTimeline{Tag: tag.Name}
			err = feed.AddToTimelineItem(timeline, status, true)
			if err != nil {
				return err
			}

			err = feed.TrimTimeline(timeline)

			if err != nil {
				return err
			}
		}
	}

	if pushToClient {
		for _, tag := range status.Tags {
			err = pushAllChannel(status, method, tag.Name)
			if err != nil {
				return err
			}
		}
	}

	return err
}

func Unpush(status *models.Status, pushToClient bool) (err error) {
	// 取得这个status有那些tag
	if err = getTags(status); err != nil {
		return
	}

	for _, tag := range status.Tags {
		timeline := &TagTimeline{Tag: tag.Name}
		err = feed.RemoveTimelineItem(timeline, status, true)
		if err != nil {
			return
		}

		err = feed.TrimTimeline(timeline)

		if err != nil {
			return
		}

		if pushToClient {
			err = pushAllChannel(status, "delete", tag.Name)
			if err != nil {
				return
			}
		}
	}

	return
}

func pushAllChannel(status *models.Status, method string, tagName string) (err error) {
	timelines := []*TagTimeline{
		{Tag: tagName},
		{Tag: tagName, OnlyMedia: true},
		{Tag: tagName, OnlyLocal: true},
		{Tag: tagName, OnlyRemote: true},
		{Tag: tagName, OnlyLocal: true, OnlyMedia: true},
		{Tag: tagName, OnlyRemote: true, OnlyMedia: true},
	}

	for _, timeline := range timelines {
		if timeline.OnlyMedia && len(status.Attachments) == 0 {
			continue
		}

		if timeline.OnlyLocal && (status.Account == nil || status.Domain != nil) {
			continue
		}

		if timeline.OnlyRemote && (status.Account == nil || status.Domain == nil) {
			continue
		}

		if feed.PushUpdateRequired(timeline) {
			err = feed.PushToClient(timeline, method, status)
			if err != nil {
				return err
			}
		}
	}

	return err
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
		query = tx.Where("id in (?)", db.GetDB().Model(&models.StatusTag{}).Where("tag_id = ?", tag).Select("status_id"))
		if isLocal := params.Other["local"]; isLocal != nil && isLocal.(bool) { //nolint:forcetypeassert
			query = query.Where("domain is null")
		} else if isRemote := params.Other["remote"]; isRemote != nil && isRemote.(bool) { //nolint:forcetypeassert
			query = query.Where("domain is not null")
		}

		return
	}

	followingIds := make([]uint64, 0)
	if account != nil {
		err = db.GetDB().Model(&models.Follows{}).Where("account_id = ?", account.Id).Select("follow_id").Find(&followingIds).Error
		if err != nil {
			return statuses, err
		}
	}

	followingCheckMap := utils.GetExistsMap(followingIds)

	afterFilter := func(statuses models.Statuses) models.Statuses {
		ret := make(models.Statuses, 0, len(statuses))

		for _, status := range statuses {
			if onlyMedia, ok := params.Other["only_media"]; ok && onlyMedia.(bool) && len(status.Attachments) == 0 { //nolint:forcetypeassert
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
