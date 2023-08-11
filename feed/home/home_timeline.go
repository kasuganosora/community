package home

import (
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
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

		//如果是新增的事件
		switch event.Name {
		case "after_create":
			go Push(status, false)
		case "before_update":
			go ClearMentionsPushStatus(status)
		case "after_update":
			go Push(status, true)
		case "after_delete":
			go Unpush(status)
		}

		return true
	}})
}

type HomeTimeline struct {
	Account *models.Account
}

func (timeline *HomeTimeline) GetKeyFormat() string {
	return "feed:home:%s"
}

func (timeline *HomeTimeline) GetTimelineId() string {
	return strconv.FormatUint(timeline.Account.Id, 10)
}

func (timeline *HomeTimeline) Rebuild() (err error) {
	statuses := make(models.Statuses, 0)
	followingAccounts, err := feed.GetAccountFollowingAccounts(timeline.Account.Id)
	if err != nil {
		return
	}

	limit := feed.MAX_ITEMS / 2
	offset := 0

	accountsId := followingAccounts.GetIds()
	// 加上我自己
	accountsId = append(accountsId, timeline.Account.Id)
	// 找出我关注的人和最近提及我的status
	err = db.GetDB().
		Preload("ToAccounts").
		Where("account_id in ? or id in (select status_id from status_to_accounts where account_id = ?)", accountsId, timeline.Account.Id).
		Order("created_at desc").Limit(limit).Offset(offset).Find(&statuses).Error

	if err != nil {
		return
	}

	// 过滤掉只有对方可见的status
	statuses = statuses.Filter(func(status *models.Status) bool {
		if status.Visibility == models.StatusVisibilityDirect {
			return status.ToAccounts.Contains(timeline.Account.Id)
		}

		return true
	})

	err = feed.CleanFeeds(timeline, []string{strconv.FormatUint(timeline.Account.Id, 10)})
	if err != nil {
		return
	}

	for _, status := range statuses {
		err = PushToHome(timeline.Account, status, false, false)
		if err != nil {
			return
		}
	}
	return
}

// ClearMentionsPushStatus 清除提及的推送状态
func ClearMentionsPushStatus(status *models.Status) {
	// 查找原来的提及用户信息
	toAccounts := make([]*models.StatusToAccount, 0)
	err := db.GetDB().Model(&models.StatusToAccount{}).
		Preload("Account").
		Preload("Account.User").
		Preload("Account.User.Settings").
		Where("status_id = ?", status.Id).Select("account_id").Find(&toAccounts).Error

	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("ClearMentionsPushStatus: status_id: %d", status.Id))
		log.ErrorV(err)
		return
	}
	for _, statusToAccount := range toAccounts {
		// 删除提及的推送状态
		err = UnPushFromHome(statusToAccount.Account, status, true)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("ClearMentionsPushStatus: status_id: %d, account_id: %d", status.Id, statusToAccount.AccountId))
			log.ErrorV(err)
			continue
		}
	}
}

func PushToHome(account *models.Account, status *models.Status, update bool, pushToClient bool) (err error) {
	homeTimeline := &HomeTimeline{Account: account}

	aggregatesReblogs := true
	if homeTimeline.Account.User != nil && homeTimeline.Account.User.Settings != nil {
		aggregatesReblogs = homeTimeline.Account.User.Settings.GetaggrAgatesReblogs()
	}

	err = feed.AddToTimelineItem(homeTimeline, status, aggregatesReblogs)
	if err != nil {
		return
	}

	err = feed.TrimTimeline(homeTimeline)
	if err != nil {
		return
	}

	if feed.PushUpdateRequired(homeTimeline) && pushToClient {
		method := "update" // https://docs.joinmastodon.org/methods/streaming/ 上说 创建是 update，更新是 status.update
		if update {
			method = "status.update"
		}
		err = feed.PushToClient(homeTimeline, method, status)
	}

	return
}

func UnPushFromHome(account *models.Account, status *models.Status, pushToClient bool) (err error) {
	homeTimeline := &HomeTimeline{Account: account}
	aggregatesReblogs := true

	if homeTimeline.Account.User != nil && homeTimeline.Account.User.Settings != nil {
		aggregatesReblogs = homeTimeline.Account.User.Settings.GetaggrAgatesReblogs()
	}

	err = feed.RemoveTimelineItem(homeTimeline, status, aggregatesReblogs)
	if err != nil {
		return
	}

	err = feed.TrimTimeline(homeTimeline)
	if err != nil {
		return
	}

	if feed.PushUpdateRequired(homeTimeline) && pushToClient {
		err = feed.PushToClient(homeTimeline, "delete", status)
	}

	return
}

func Push(status *models.Status, update bool) (err error) {
	if status.ToAccounts == nil {
		status.ToAccounts = make(models.Accounts, 0)
		err = db.GetDB().Model(status).Association("ToAccounts").Find(&status.ToAccounts)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
			return
		}
	}

	accountsId := make([]uint64, 0)

	if status.Visibility == models.StatusVisibilityDirect {
		accountsId = status.ToAccounts.GetIds()
	} else {
		accountsId, err = feed.GetAccountRecentActiveLocalFollowersId(status.AccountId)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
			return
		}
		// 再加上这个status提及的用户
		accountsId = append(accountsId, status.ToAccounts.GetIds()...)
	}

	// 加上这个status的作者
	accountsId = append(accountsId, status.AccountId)
	accountsId = utils.UniqueSlice(accountsId)

	// 推送到发这个status的用户的粉丝和提及的用户
	go func() {
		accounts := make([]*models.Account, 0)
		err = db.GetDB().Model(&models.Account{}).Preload("User").Preload("User.Settings").Where("id in (?)", accountsId).Find(&accounts).Error
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d, to accounts %v", status.Id, accountsId))
			log.ErrorV(err)
			return
		}

		for _, account := range accounts {
			err = PushToHome(account, status, update, true)
			if err != nil {
				err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d, to account %d", status.Id, account.Id))
				log.ErrorV(err)
				continue
			}
		}
	}()

	return
}

func Unpush(status *models.Status) (err error) {
	if status.ToAccounts == nil {
		status.ToAccounts = make(models.Accounts, 0)
		err = db.GetDB().Model(status).Association("ToAccounts").Find(&status.ToAccounts)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
			return
		}
	}

	accountsId := make([]uint64, 0)

	if status.Visibility == models.StatusVisibilityDirect {
		accountsId = status.ToAccounts.GetIds()
	} else {
		accountsId, err = feed.GetAccountRecentActiveLocalFollowersId(status.AccountId)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
			return
		}
		// 再加上这个status提及的用户
		accountsId = append(accountsId, status.ToAccounts.GetIds()...)
	}

	// 加上这个status的作者
	accountsId = append(accountsId, status.AccountId)
	accountsId = utils.UniqueSlice(accountsId)

	// 推送到发这个status的用户的粉丝和提及的用户
	go func() {
		accounts := make([]*models.Account, 0)
		err = db.GetDB().Preload("account.User").Preload("account.User.Settings").Where("id in (?)", accountsId).Find(&accounts).Error
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d, to accounts %v", status.Id, accountsId))
			log.ErrorV(err)
			return
		}

		for _, account := range accounts {
			err = UnPushFromHome(account, status, true)
			if err != nil {
				err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d, to account %d", status.Id, account.Id))
				log.ErrorV(err)
				continue
			}
		}
	}()

	return
}

func GetTimeLineContent(account *models.Account, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeLine := &HomeTimeline{Account: account}

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		// 没有找到 statuses id，尝试从数据库中找
		var followingAccounts models.Accounts
		followingAccounts, err = feed.GetAccountFollowingAccounts(account.Id)
		if err != nil {
			return
		}

		accountIds := followingAccounts.GetIds()
		// 加上我自己
		accountIds = append(accountIds, account.Id)

		query = tx.Where("account_id in (?) or id in (select status_id from status_to_accounts where account_id = ?)", accountIds, account.Id).
			Order("created_at desc")
		return
	}

	afterFilter := func(statuses models.Statuses) (ret models.Statuses) {
		return statuses.Filter(func(status *models.Status) bool {
			if status.Visibility == models.StatusVisibilityDirect {
				return status.ToAccounts.Contains(account.Id)
			}

			return true
		})
	}

	return feed.GetStatusTimelineContent(timeLine, params, newFilterCondition, afterFilter)
}
