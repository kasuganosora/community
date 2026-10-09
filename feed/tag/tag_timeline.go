package tag

import (
	"context"
	"log"

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

	// Case-insensitive like the tag itself: #OSINT and #osint share a
	// timeline cache and streaming channel.
	strs = append(strs, models.TagKey(timeline.Tag))

	return strings.Join(strs, ":")
}

func (timeline *TagTimeline) Rebuild() error { return timeline.RebuildContext(context.Background()) }
func (timeline *TagTimeline) RebuildContext(ctx context.Context) (err error) {
	statuses := make(models.Statuses, 0)
	tag, err := models.FindTagByName(db.GetDB().WithContext(ctx), timeline.Tag)
	if err != nil {
		return err
	}

	membership, err := tagMembership(db.GetDB().WithContext(ctx), tag.Id)
	if err != nil {
		return err
	}
	query := membership(feed.ExcludeBackfilled(feed.EligibleTimelineStatuses(db.GetDB().WithContext(ctx).Model(&models.Status{}))))
	if timeline.OnlyLocal {
		query = query.Where("statuses.domain IS NULL")
	} else if timeline.OnlyRemote {
		query = query.Where("statuses.domain IS NOT NULL")
	}
	if timeline.OnlyMedia {
		query = query.Where("(SELECT 1 FROM media_attachments WHERE media_attachments.status_id = statuses.id LIMIT 1) IS NOT NULL")
	}
	err = query.Order("statuses.id desc").Limit(feed.MAX_ITEMS / 2).Find(&statuses).Error
	if err != nil {
		return err
	}

	return feed.RebuildTimelineContext(ctx, timeline, statuses, true)
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

	// 安全处理 nil params 和 nil Other map
	if params == nil {
		params = &feed.ListParam{}
	}

	params.Viewer = account
	if params.Other == nil {
		params.Other = make(map[string]any)
	}
	// Resolve once per request, including requests which cross several cache
	// batches after filtering. This identifier is not an authorization cache.
	tagModel, err := models.FindTagByName(db.GetDB().WithContext(contextForParams(params)), tag)
	if err != nil {
		return nil, err
	}

	membership, err := tagMembership(db.GetDB().WithContext(contextForParams(params)), tagModel.Id)
	if err != nil {
		return nil, err
	}
	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = membership(feed.ExcludeBackfilled(feed.EligibleTimelineStatuses(tx)))
		// Preserve the personalized tag audience, but filter it before LIMIT
		// and association loading instead of hydrating inaccessible pages.
		if account == nil {
			query = query.Where("statuses.visibility IN ?", []int{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted})
		} else {
			query = query.Where(`(statuses.visibility IN ? OR
				(statuses.visibility = ? AND (statuses.account_id = ? OR EXISTS (SELECT 1 FROM follows WHERE follows.account_id = ? AND follows.follow_id = statuses.account_id AND follows.status = ?))) OR
				(statuses.visibility = ? AND EXISTS (SELECT 1 FROM status_to_accounts WHERE status_to_accounts.status_id = statuses.id AND status_to_accounts.account_id = ?)))`,
				[]int{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted}, models.StatusVisibilityPrivate,
				account.Id, account.Id, models.FollowStatusFollowed, models.StatusVisibilityDirect, account.Id)
		}
		if isLocal := params.Other["local"]; isLocal != nil && isLocal.(bool) { //nolint:forcetypeassert
			query = query.Where("domain is null")
		} else if isRemote := params.Other["remote"]; isRemote != nil && isRemote.(bool) { //nolint:forcetypeassert
			query = query.Where("domain is not null")
		}

		if onlyMedia, _ := params.Other["only_media"].(bool); onlyMedia {
			query = query.Where("(SELECT 1 FROM media_attachments WHERE media_attachments.status_id = statuses.id LIMIT 1) IS NOT NULL")
		}
		return
	}

	if local, _ := params.Other["local"].(bool); local {
		params.Other["database_only"] = true
	}
	if remote, _ := params.Other["remote"].(bool); remote {
		params.Other["database_only"] = true
	}
	if media, _ := params.Other["only_media"].(bool); media {
		params.Other["database_only"] = true
	}
	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}

func contextForParams(params *feed.ListParam) context.Context {
	if params.Context != nil {
		return params.Context
	}
	return context.Background()
}

func (timeline *TagTimeline) AggregateReblogs() bool { return true }

// Choose a request-local query shape from a bounded indexed membership probe.
// Small/empty tags must not scan status history to find an old post or EOF.
// Dense tags avoid MySQL's association-driven semijoin and global filesort.
// Large but old/sparse tags are not universally optimal under this heuristic;
// an ordered covering association index is a separate measured DDL decision.
func tagMembership(conn *gorm.DB, tagID uint64) (func(*gorm.DB) *gorm.DB, error) {
	ids := []uint64{}
	if err := conn.Model(&models.StatusTag{}).Where("tag_id = ?", tagID).Limit(feed.MAX_ITEMS+1).Pluck("status_id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return func(tx *gorm.DB) *gorm.DB { return tx.Where("1 = 0") }, nil
	}
	if len(ids) <= feed.MAX_ITEMS {
		return func(tx *gorm.DB) *gorm.DB { return tx.Where("statuses.id IN ?", ids) }, nil
	}
	return func(tx *gorm.DB) *gorm.DB {
		return tx.Where("(SELECT 1 FROM status_tags WHERE tag_id = ? AND status_id = statuses.id LIMIT 1) IS NOT NULL", tagID)
	}, nil
}
