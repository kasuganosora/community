package public

import (
	"context"
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

		// Old posts imported late never enter the live timelines.
		if feed.SkipsTimelines(status) {
			return true
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

func (timeline *PublicTimeline) AggregateReblogs() bool { return true }

func (timeline *PublicTimeline) candidates(tx *gorm.DB) *gorm.DB {
	query := feed.ExcludeBackfilled(feed.EligibleTimelineStatuses(tx)).Where("statuses.visibility = ?", models.StatusVisibilityPublic)
	if timeline.OnlyMedia {
		query = query.Where("(SELECT 1 FROM media_attachments WHERE media_attachments.status_id = statuses.id LIMIT 1) IS NOT NULL")
	}
	if timeline.OnlyLocal {
		query = query.Where("statuses.domain IS NULL")
	}
	if timeline.OnlyRemote {
		query = query.Where("statuses.domain IS NOT NULL")
	}
	return query
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

func (timeline *PublicTimeline) Rebuild() error { return timeline.RebuildContext(context.Background()) }
func (timeline *PublicTimeline) RebuildContext(ctx context.Context) (err error) {
	statuses := make(models.Statuses, 0)

	err = timeline.candidates(db.GetDB().WithContext(ctx).Model(&models.Status{})).
		Order("statuses.id desc").Limit(feed.MAX_ITEMS / 2).Find(&statuses).Error
	if err != nil {
		return
	}

	return feed.RebuildTimelineContext(ctx, timeline, statuses, true)
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

	// 安全处理 nil params
	if params == nil {
		params = &feed.ListParam{}
	}

	if params.Other == nil {
		params.Other = make(map[string]any)
	}
	timeline.OnlyLocal, _ = params.Other["local"].(bool)
	timeline.OnlyRemote, _ = params.Other["remote"].(bool)

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		return timeline.candidates(tx), nil
	}

	if local, _ := params.Other["local"].(bool); local {
		params.Other["database_only"] = true
	}
	if remote, _ := params.Other["remote"].(bool); remote {
		params.Other["database_only"] = true
	}
	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, dropSilencedDomains)
}

func GetTimelineContentMedia(params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &PublicTimeline{OnlyMedia: true}

	// 安全处理 nil params
	if params == nil {
		params = &feed.ListParam{}
	}

	if params.Other == nil {
		params.Other = make(map[string]any)
	}
	timeline.OnlyLocal, _ = params.Other["local"].(bool)
	timeline.OnlyRemote, _ = params.Other["remote"].(bool)

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		return timeline.candidates(tx), nil
	}

	// Publication maintains the shared public feed. Filtered views use SQL so
	// a separately cached filter cannot miss posts published after its rebuild.
	params.Other["database_only"] = true
	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, dropSilencedDomains)
}

// dropSilencedDomains keeps silenced instances out of public timelines without
// hiding them from followers. Suspended domains are removed later by visibility.
func dropSilencedDomains(statuses models.Statuses) models.Statuses {
	_, silence, err := models.DomainSeverities(db.GetDB())
	if err != nil || len(silence) == 0 {
		return statuses
	}
	out := make(models.Statuses, 0, len(statuses))
	for _, status := range statuses {
		if statusDomainSilenced(silence, status) {
			continue
		}
		out = append(out, status)
	}
	return out
}

func statusDomainSilenced(silence map[string]bool, status *models.Status) bool {
	if status == nil {
		return false
	}
	if status.Domain != nil && silence[strings.ToLower(*status.Domain)] {
		return true
	}
	if status.Reblog != nil && status.Reblog.Domain != nil && silence[strings.ToLower(*status.Reblog.Domain)] {
		return true
	}
	return false
}
