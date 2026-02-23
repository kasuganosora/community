package mention

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"strconv"
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

		// 不展示 私信
		if status.Visibility == models.StatusVisibilityDirect {
			return true
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

type MentionTimeline struct {
	Account *models.Account
}

func (timeline *MentionTimeline) GetKeyFormat() string {
	return "feed:mention:%s"
}

func (timeline *MentionTimeline) GetTimelineId() string {
	return strconv.FormatUint(timeline.Account.Id, 10)
}

func (timeline *MentionTimeline) Rebuild() (err error) {
	statuses := make(models.Statuses, 0)

	err = db.GetDB().Model(&models.Status{}).Where("id in ?", db.GetDB().Model(&models.StatusToAccount{}).
		Where("account_id = ?", timeline.Account.Id).Select("status_id").Order("id desc").Limit(feed.MAX_ITEMS/2)).
		Order("id desc").Find(&statuses).Error

	if err != nil {
		return
	}

	if err = feed.CleanFeeds(timeline, []string{timeline.GetTimelineId()}); err != nil {
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
	accounts := make(models.Accounts, 0)

	// 只要本地用户的
	err = db.GetDB().Model(&models.Account{}).Preload("User").Preload("User.Settings").
		Where("id in ?", db.GetDB().Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Select("account_id")).
		Where("domain is null").
		Find(&accounts).Error

	if len(accounts) == 0 {
		return
	}

	for _, account := range accounts {
		timeline := &MentionTimeline{Account: account}
		aggregatesReblogs := false
		if account.User != nil && account.User.Settings != nil {
			aggregatesReblogs = account.User.Settings.GetAggregatesReblogs()
		}
		if err = feed.AddToTimelineItem(timeline, status, aggregatesReblogs); err != nil {
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
	if status.ToAccounts == nil || len(status.ToAccounts) == 0 {
		return
	}

	// 读取出旧的 to_accounts
	accounts := make(models.Accounts, 0)
	err = db.GetDB().Model(&models.Account{}).Preload("User").Preload("User.Settings").
		Where("id in ?", db.GetDB().Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Select("account_id")).
		Where("domain is null").
		Find(&accounts).Error

	for _, account := range accounts {
		timeline := &MentionTimeline{Account: account}
		if err = feed.RemoveTimelineItem(timeline, status, account.User.Settings.GetAggregatesReblogs()); err != nil {
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

func GetTimelineContent(account *models.Account, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &MentionTimeline{Account: account}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		return tx.Where("id in ?", db.GetDB().Model(&models.StatusToAccount{}).
			Where("account_id = ?", account.Id).Select("status_id").Order("id desc").Limit(feed.MAX_ITEMS/2)), nil
	}
	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}
