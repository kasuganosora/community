package public

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
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

		// 只展示 公开的
		if status.Visibility != models.StatusVisibilityPublic {
			return true
		}

		// 如果是新增的事件
		switch event.Name {
		case "after_create":
			go func() { _ = Push(status, false, true) }()
		case "before_update":
			go func() { _ = Unpush(status, true) }() // 更新之前，先删除
		case "after_update":
			go func() { _ = Push(status, true, true) }()
		case "after_delete":
			go func() { _ = Unpush(status, true) }()
		}

		return true
	}})
}

type PublicTimeline struct {
	OnlyMedia  bool
	OnlyLocal  bool
	OnlyRemote bool
}

func (timeline *PublicTimeline) GetKeyFormat() string {
	return "feed:public:%s"
}

func (timeline *PublicTimeline) GetTimelineId() string {
	if !timeline.OnlyMedia && !timeline.OnlyLocal && !timeline.OnlyRemote {
		return "all"
	}

	ret := make([]string, 0)
	if timeline.OnlyMedia {
		ret = append(ret, "media")
	}

	if timeline.OnlyLocal {
		ret = append(ret, "local")
	}

	if timeline.OnlyRemote {
		ret = append(ret, "remote")
	}

	return strings.Join(ret, ":")
}

func (timeline *PublicTimeline) Rebuild() (err error) {
	statuses := make(models.Statuses, 0)

	err = db.GetDB().Model(&models.Status{}).Preload("Attachments").
		Where("visibility = ?", models.StatusVisibilityPublic).
		Order("id desc").Limit(feed.MAX_ITEMS / 2).Find(&statuses).Error
	if err != nil {
		return
	}

	allPublicTimeline := &PublicTimeline{}

	if err = feed.CleanFeeds(allPublicTimeline, []string{allPublicTimeline.GetTimelineId()}); err != nil {
		return
	}

	for _, status := range statuses {
		err = feed.AddToTimelineItem(allPublicTimeline, status, true)
		if err != nil {
			return
		}

		err = feed.TrimTimeline(allPublicTimeline)
		if err != nil {
			return
		}
	}

	return
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	if status.Visibility != models.StatusVisibilityPublic {
		return
	}

	// 先放到all里面
	allPublicTimeline := &PublicTimeline{}
	if err = feed.AddToTimelineItem(allPublicTimeline, status, true); err != nil {
		return
	}

	if err = feed.TrimTimeline(allPublicTimeline); err != nil {
		return
	}

	method := "update" // https://docs.joinmastodon.org/methods/streaming/ 上说 创建是 update，更新是 status.update
	if update {
		method = "status.update"
	}

	// 判断那些参数的时间线有人在订阅，如果有，就推送
	if pushToClient {
		err = pushAllChannel(status, method)
		if err != nil {
			return
		}
	}

	return
}

func Unpush(status *models.Status, pushToClient bool) (err error) {
	// 从全部时间线里面删除
	allPublicTimeline := &PublicTimeline{}
	if err = feed.RemoveTimelineItem(allPublicTimeline, status, true); err != nil {
		return
	}

	if err = feed.TrimTimeline(allPublicTimeline); err != nil {
		return
	}

	// 如果其他有带条件的时间线，有人在订阅的时候，需要发送删除通知

	if pushToClient {
		err = pushAllChannel(status, "delete")
		if err != nil {
			return
		}
	}

	return
}

func pushAllChannel(status *models.Status, method string) (err error) {
	if status.Visibility != models.StatusVisibilityPublic {
		return err
	}

	timelines := []*PublicTimeline{
		{},
		{OnlyMedia: true},
		{OnlyLocal: true},
		{OnlyRemote: true},
		{OnlyLocal: true, OnlyMedia: true},
		{OnlyRemote: true, OnlyMedia: true},
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

func GetTimelineContentAll(params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &PublicTimeline{OnlyMedia: false}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("visibility = ?", models.StatusVisibilityPublic)

		if params.Other["local"] != nil && params.Other["local"].(bool) { //nolint:forcetypeassert
			query = query.Where("statuses.domain is null")
		} else if params.Other["remote"] != nil && params.Other["remote"].(bool) { //nolint:forcetypeassert
			query = query.Where("statuses.domain IS NOT NULL")
		}

		return
	}

	afterFilter := func(statuses models.Statuses) (ret models.Statuses) {
		var remote, local bool
		if _remote, ok := params.Other["remote"]; ok {
			remote = _remote.(bool) //nolint:forcetypeassert
		}

		if _local, ok := params.Other["local"]; ok {
			local = _local.(bool) //nolint:forcetypeassert
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

		if params.Other["local"] != nil && params.Other["local"].(bool) { //nolint:forcetypeassert
			query = query.Where("statuses.domain is null")
		} else if params.Other["remote"] != nil && params.Other["remote"].(bool) { //nolint:forcetypeassert
			query = query.Where("statuses.domain IS NOT NULL")
		}

		return
	}

	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}
