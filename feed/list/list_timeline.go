package list

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	appredis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		if feed.DurableFanoutEnabled() {
			return true
		}
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

type ListTimeline struct {
	List       *models.List
	OnlyLocal  bool
	OnlyRemote bool
	OnlyMedia  bool
}

func (timeline *ListTimeline) GetKeyFormat() string {
	return "feed:list:v2:%s"
}

func (timeline *ListTimeline) AggregateReblogs() bool { return true }

func (timeline *ListTimeline) memberIDs(tx *gorm.DB) *gorm.DB {
	return tx.Session(&gorm.Session{NewDB: true}).Model(&models.ListAccount{}).Where("list_id = ?", timeline.List.Id).Select("account_id")
}

func (timeline *ListTimeline) tagIDs(tx *gorm.DB) *gorm.DB {
	return tx.Session(&gorm.Session{NewDB: true}).Model(&models.ListHashtag{}).Where("list_id = ?", timeline.List.Id).Select("tag_id")
}

// candidates are posts by list members (subject to replies_policy) plus
// posts carrying one of the list's hashtags, both public or unlisted.
// Tagged posts by strangers skip authors the owner blocks (either way) or
// whose domain the owner blocked; replies_policy "none" still drops replies.
func (timeline *ListTimeline) candidates(tx *gorm.DB) *gorm.DB {
	query := feed.ExcludeBackfilled(feed.EligibleTimelineStatuses(tx)).
		Where("(statuses.visibility = ? OR statuses.visibility = ?)", models.StatusVisibilityPublic, models.StatusVisibilityUnlisted)
	if timeline.OnlyLocal {
		query = query.Where("statuses.domain IS NULL")
	}
	if timeline.OnlyRemote {
		query = query.Where("statuses.domain IS NOT NULL")
	}
	if timeline.OnlyMedia {
		query = query.Where("EXISTS (SELECT 1 FROM media_attachments WHERE media_attachments.status_id = statuses.id)")
	}
	if timeline.List == nil {
		return query
	}
	noReply := "(statuses.in_reply_to_id IS NULL OR statuses.in_reply_to_id = 0)"
	var members *gorm.DB
	switch timeline.List.RepliesPolicy {
	case models.ListRepliesPolicyNone:
		members = tx.Session(&gorm.Session{NewDB: true}).Where("statuses.account_id IN (?) AND "+noReply, timeline.memberIDs(tx))
	case models.ListRepliesPolicyFollowed:
		following := tx.Session(&gorm.Session{NewDB: true}).Model(&models.Follow{}).
			Scopes(models.Followed).
			Where("account_id = ?", timeline.List.AccountId).
			Select("follow_id")
		members = tx.Session(&gorm.Session{NewDB: true}).Where("statuses.account_id IN (?) AND ("+noReply+" OR statuses.in_reply_to_account_id IN (?))", timeline.memberIDs(tx), following)
	default:
		members = tx.Session(&gorm.Session{NewDB: true}).Where("statuses.account_id IN (?) AND ("+noReply+" OR statuses.in_reply_to_account_id IN (?))", timeline.memberIDs(tx), timeline.memberIDs(tx))
	}
	owner := timeline.List.AccountId
	tagged := tx.Session(&gorm.Session{NewDB: true}).
		Where("statuses.id IN (?)", tx.Session(&gorm.Session{NewDB: true}).Model(&models.StatusTag{}).Where("tag_id IN (?)", timeline.tagIDs(tx)).Select("status_id")).
		Where("NOT "+models.ListAuthorBlockedSQL, owner, owner, owner)
	if timeline.List.RepliesPolicy == models.ListRepliesPolicyNone {
		tagged = tagged.Where(noReply)
	}
	return query.Where(members.Or(tagged))
}

// DropCache forgets a list feed so the next read rebuilds it from the current
// members. Adding or removing someone otherwise leaves the previous snapshot.
func DropCache(listID uint64) {
	if listID == 0 || (len(appredis.GetRedises()) == 0 && !appredis.HasUniversalRedis()) {
		return
	}
	timeline := &ListTimeline{List: &models.List{Id: listID}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = feed.InvalidateTimelineContext(ctx, timeline)
}

func (timeline *ListTimeline) GetTimelineId() string {
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

	strs = append(strs, strconv.FormatUint(timeline.List.Id, 10))

	return strings.Join(strs, ":")
}

func (timeline *ListTimeline) Rebuild() error { return timeline.RebuildContext(context.Background()) }
func (timeline *ListTimeline) RebuildContext(ctx context.Context) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	lease, leaseErr := feed.BeginRebuild(ctx, timeline)
	if leaseErr != nil {
		return leaseErr
	}
	defer feed.ReleaseRebuild(ctx, timeline, lease)

	statuses := make(models.Statuses, 0)
	err = timeline.candidates(db.GetDB().WithContext(ctx).Model(&models.Status{})).
		Order(feed.StatusTimelineOrder).Limit(feed.MAX_ITEMS / 4).Find(&statuses).Error
	if err != nil {
		return err
	}

	return feed.PublishRebuild(ctx, timeline, lease, statuses, true)
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	if status.Visibility != models.StatusVisibilityPublic {
		return err
	}

	// 查询这个发推人在哪些列表里(找出最后30天有人看过的列表)
	lists := make([]models.List, 0)

	err = targetLists(status).Find(&lists).Error
	if err != nil {
		return err
	}

	if len(lists) == 0 {
		return err
	}

	method := "update" // https://docs.joinmastodon.org/methods/streaming/ 上说 创建是 update，更新是 status.update
	if update {
		method = "status.update"
	}

	isReblog := status.ReblogId != nil && *status.ReblogId > 0

	if !isReblog {
		// 非转发：使用批量添加优化性能（Redis Pipeline）
		timelines := make([]feed.Timeline, 0, len(lists))
		for i := range lists {
			timelines = append(timelines, &ListTimeline{List: &lists[i]})
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
		for i := range lists {
			timeline := &ListTimeline{List: &lists[i]}
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
		for i := range lists {
			timeline := &ListTimeline{List: &lists[i]}
			if feed.PushUpdateRequired(timeline) {
				err = feed.PushToClient(timeline, method, status)
				if err != nil {
					return err
				}
			}
		}
	}

	return err
}

func Unpush(status *models.Status, pushToClient bool) (err error) {
	if status.Visibility != models.StatusVisibilityPublic {
		return err
	}

	// 查询这个发推人在哪些列表里(找出最后30天有人看过的列表)
	lists := make([]models.List, 0)

	err = targetLists(status).Find(&lists).Error
	if err != nil {
		return err
	}

	for i := range lists {
		timeline := &ListTimeline{List: &lists[i]}
		err = feed.RemoveTimelineItem(timeline, status, true)
		if err != nil {
			return err
		}

		err = feed.TrimTimeline(timeline)
		if err != nil {
			return err
		}

		if pushToClient {
			err = feed.PushToClient(timeline, "delete", status)
			if err != nil {
				return err
			}
		}
	}

	return err
}

func GetTimelineContent(list *models.List, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &ListTimeline{
		List: list,
	}
	if params == nil {
		params = &feed.ListParam{}
	}
	ctx := params.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Activity bookkeeping is coarse (the active-list window is 30 days).
	// The controller-loaded timestamp avoids rewriting the list on every page.
	now := time.Now()
	if list.LastViewedAt == nil || now.Sub(*list.LastViewedAt) >= time.Minute {
		err = db.GetDB().WithContext(ctx).Model(&models.List{}).Where("id = ?", list.Id).Update("last_viewed_at", now).Error
		if err != nil {
			return
		}
		list.LastViewedAt = &now
	}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		return timeline.candidates(tx), nil
	}

	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}

// targetLists are the recently viewed lists a status belongs in: lists that
// contain its author, plus lists following one of its hashtags (tag_id is
// indexed) whose owner has not blocked the author.
func targetLists(status *models.Status) *gorm.DB {
	conn := db.GetDB()
	members := conn.Model(&models.ListAccount{}).Where("account_id = ?", status.AccountId).Select("list_id")
	byTag := conn.Model(&models.ListHashtag{}).
		Where("tag_id IN (?)", conn.Model(&models.StatusTag{}).Where("status_id = ?", status.Id).Select("tag_id")).
		Select("list_id")
	notBlocked := conn.Model(&models.Status{}).Select("1").Where("statuses.id = ?", status.Id).
		Where("NOT " + strings.ReplaceAll(models.ListAuthorBlockedSQL, "?", "lists.account_id"))
	if status.InReplyToId != nil && *status.InReplyToId != 0 {
		byTag = byTag.Where("list_id NOT IN (?)", conn.Model(&models.List{}).Where("replies_policy = ?", models.ListRepliesPolicyNone).Select("id"))
	}
	return conn.Model(&models.List{}).
		Where("id IN (?) OR (id IN (?) AND EXISTS (?))", members, byTag, notBlocked).
		Where("last_viewed_at > ?", time.Now().AddDate(0, 0, -30))
}
