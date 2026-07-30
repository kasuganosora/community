package list

import (
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
	return "feed:list:%s"
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

	strs = append(strs, timeline.List.Title)

	return strings.Join(strs, ":")
}

func (timeline *ListTimeline) Rebuild() (err error) {
	accountIds := make([]uint64, 0)

	err = db.GetDB().Model(
		&models.ListAccount{}).Where("list_id = ?", timeline.List.Id).Pluck("account_id", &accountIds).Error
	if err != nil {
		return err
	}

	statuses := make(models.Statuses, 0)

	err = db.GetDB().Model(
		&models.Status{}).Where("account_id in (?)", accountIds).
		Where("visibility = ?", models.StatusVisibilityPublic).
		Order("id desc").Limit(feed.MAX_ITEMS / 4).
		Find(
			&statuses).Error
	if err != nil {
		return err
	}

	err = feed.CleanFeeds(timeline, []string{timeline.GetTimelineId()})
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
	if status.Visibility != models.StatusVisibilityPublic {
		return err
	}

	// 查询这个发推人在哪些列表里(找出最后30天有人看过的列表)
	lists := make([]models.List, 0)

	err = db.GetDB().Model(
		&models.List{}).
		Where("id IN (?)", db.GetDB().Model(
			&models.ListAccount{}).Where("account_id = ?", status.AccountId).Select("list_id")).
		Where("last_viewed_at > ?", time.Now().AddDate(0, 0, -30)).
		Find(
			&lists).Error
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
			err = feed.PushToClient(timeline, method, status)
			if err != nil {
				return err
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

	err = db.GetDB().Model(
		&models.List{}).
		Where("id IN (?)", db.GetDB().Model(
			&models.ListAccount{}).Where("account_id = ?", status.AccountId).Select("list_id")).
		Where("last_viewed_at > ?", time.Now().AddDate(0, 0, -30)).
		Find(
			&lists).Error
	if err != nil {
		return err
	}

	for _, list := range lists {
		timeline := &ListTimeline{List: &list}
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

	// 标记最后查看时间
	now := time.Now()
	list.LastViewedAt = &now

	err = db.GetDB().Model(
		&models.List{}).Where("id = ?", list.Id).Select("last_viewed_at").Updates(list).Error
	if err != nil {
		return
	}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("visibility = ?", models.StatusVisibilityPublic)

		return
	}

	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}
