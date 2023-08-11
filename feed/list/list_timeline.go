package list

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"strconv"
	"time"
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

type ListTimeline struct {
	List *models.List
}

func (timeline *ListTimeline) GetKeyFormat() string {
	return "feed:list:%s"
}

func (timeline *ListTimeline) GetTimelineId() string {
	return strconv.FormatUint(timeline.List.Id, 10)
}

func (timeline *ListTimeline) Rebuild() (err error) {
	accountIds := make([]uint64, 0)
	err = db.GetDB().Model(&models.ListAccount{}).Where("list_id = ?", timeline.List.Id).Pluck("account_id", &accountIds).Error

	if err != nil {
		return
	}

	statuses := make(models.Statuses, 0)
	err = db.GetDB().Model(&models.Status{}).Where("account_id in (?)", accountIds).
		Where("visibility = ?", models.StatusVisibilityPublic).
		Order("id desc").Limit(feed.MAX_ITEMS / 4).
		Find(statuses).Error

	if err != nil {
		return
	}

	err = feed.CleanFeeds(timeline, []string{timeline.GetTimelineId()})
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
	if status.Visibility != models.StatusVisibilityPublic {
		return
	}

	// 查询这个发推人在哪些列表里(找出最后30天有人看过的列表)
	lists := make([]models.List, 0)
	err = db.GetDB().Model(&models.List{}).Where("account_id = ?", status.AccountId).
		Where("last_viewed_at > ?", time.Now().AddDate(0, 0, -30)).
		Find(&lists).Error

	if err != nil {
		return
	}

	for _, list := range lists {
		timeline := &ListTimeline{List: &list}
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
	if status.Visibility != models.StatusVisibilityPublic {
		return
	}

	// 查询这个发推人在哪些列表里(找出最后30天有人看过的列表)
	lists := make([]models.List, 0)
	err = db.GetDB().Model(&models.List{}).Where("account_id = ?", status.AccountId).
		Where("last_viewed_at > ?", time.Now().AddDate(0, 0, -30)).
		Find(&lists).Error

	if err != nil {
		return
	}

	for _, list := range lists {
		timeline := &ListTimeline{List: &list}
		if err = feed.RemoveTimelineItem(timeline, status, true); err != nil {
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

func GetTimelineContent(list *models.List, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &ListTimeline{
		List: list,
	}

	// 标记最后查看时间
	list.LastViewedAt = utils.GetPtr(time.Now())
	err = db.GetDB().Model(&models.List{}).Where("id = ?", list.Id).Select("last_viewed_at").Updates(list).Error
	if err != nil {
		return
	}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("visibility = ?", models.StatusVisibilityPublic)
		return
	}

	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}
