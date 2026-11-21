package public

import (
	"context"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"strings"
	"time"
)

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		if feed.DurableFanoutEnabled() {
			return true
		}
		if event.Data == nil {
			return true
		}

		status, ok := event.Data.(*models.Status)
		if !ok || status == nil {
			return true
		}

		// Old posts imported late never enter the live timelines.
		if feed.SkipsTimelines(status) {
			return true
		}

		// The saved row already carries the new visibility. Leaving the public
		// stream still has to delete the previous card.
		if event.Name == "before_update" && status.Visibility != models.StatusVisibilityPublic {
			go func() {
				clone := *status
				clone.Visibility = models.StatusVisibilityPublic
				_ = Unpush(&clone, true)
			}()
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
			go func() { _ = Unpush(status, false) }() // 更新之前，先删除缓存项，不向客户端发 delete
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	lease, leaseErr := feed.BeginRebuild(ctx, timeline)
	if leaseErr != nil {
		return leaseErr
	}
	defer feed.ReleaseRebuild(ctx, timeline, lease)

	statuses := make(models.Statuses, 0)

	err = timeline.candidates(db.GetDB().WithContext(ctx).Model(&models.Status{})).
		Order(feed.StatusTimelineOrder).Limit(feed.MAX_ITEMS).Find(&statuses).Error
	if err != nil {
		return
	}

	return feed.PublishRebuild(ctx, timeline, lease, statuses, true)
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	if status == nil {
		return nil
	}
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
	if status == nil {
		return nil
	}
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
		if method != "delete" && timeline.OnlyMedia && len(status.Attachments) == 0 {
			continue
		}

		if timeline.OnlyLocal && status.Domain != nil {
			continue
		}

		if timeline.OnlyRemote && status.Domain == nil {
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

	if !feed.DurableFanoutEnabled() {
		if local, _ := params.Other["local"].(bool); local {
			params.Other["database_only"] = true
		}
		if remote, _ := params.Other["remote"].(bool); remote {
			params.Other["database_only"] = true
		}
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

	// Legacy fanout only maintains the shared public feed; durable fanout
	// projects all 6 public variants directly.
	if !feed.DurableFanoutEnabled() {
		params.Other["database_only"] = true
	}
	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, dropSilencedDomains)
}

// dropSilencedDomains keeps silenced instances out of public timelines without
// hiding them from followers. Suspended domains are removed later by visibility.
func dropSilencedDomains(statuses models.Statuses) models.Statuses {
	hasRemote := false
	for _, status := range statuses {
		if status != nil && (status.Domain != nil || (status.Reblog != nil && status.Reblog.Domain != nil)) {
			hasRemote = true
			break
		}
	}
	if !hasRemote {
		return statuses
	}
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

func statusDomainSilenced(silence models.HostSet, status *models.Status) bool {
	if status == nil {
		return false
	}
	if status.Domain != nil && silence.Contains(*status.Domain) {
		return true
	}
	if status.Reblog != nil && status.Reblog.Domain != nil && silence.Contains(*status.Reblog.Domain) {
		return true
	}
	return false
}
