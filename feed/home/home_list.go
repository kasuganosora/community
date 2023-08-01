package home

import (
	"context"
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"strconv"
)

type Home struct {
	Account *models.Account
}

func (ll *Home) GetKeyFormat() string {
	return "feed:home:%s"
}

func (ll *Home) GetTimelineId() string {
	return strconv.FormatUint(ll.Account.Id, 10)
}

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", "status.feed.home", &event.Handler{Exec: func(event event.Event) bool {
		if event.Data == nil {
			return false
		}
		status := event.Data.(*models.Status)
		if status == nil {
			return false
		}

		//如果是新增的事件
		switch event.Name {
		case "after_create_status":
			go PushStatusToFollowers(status, false)
		case "after_update_status":
			go PushStatusToFollowers(status, true)
		case "after_delete_status":
			go UnPushStatusFromFollowers(status)
		}

		return true
	}})
}
func PushToHome(account *models.Account, status *models.Status, update bool, pushToClient bool) (err error) {
	homeTimeline := &Home{Account: account}

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
		method := "status.new"
		if update {
			method = "status.update"
		}
		err = feed.PushStatusEventToClient(homeTimeline, status, method)
	}

	return
}

func UnPushFromHome(account *models.Account, status *models.Status, pushToClient bool) (err error) {
	homeTimeline := &Home{Account: account}
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
		err = feed.PushStatusEventToClient(homeTimeline, status, "delete")
	}

	return
}

func PushStatusToFollowers(status *models.Status, update bool) (err error) {
	accountsId, err := feed.GetAccountRecentActiveLocalFollowersId(status.AccountId)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
		return
	}

	// 这个status 提及的用户
	mentionsAccountsId := make([]uint64, 0)
	err = db.GetDB().Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Pluck("account_id", &mentionsAccountsId).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
		return
	}

	accountsId = append(accountsId, mentionsAccountsId...)
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

func UnPushStatusFromFollowers(status *models.Status) (err error) {
	accountsId, err := feed.GetAccountRecentActiveLocalFollowersId(status.AccountId)
	if err != nil {
		return
	}

	// 这个status 提及的用户
	mentionsAccountsId := make([]uint64, 0)
	err = db.GetDB().Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Pluck("account_id", &mentionsAccountsId).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
		return
	}

	accountsId = append(accountsId, mentionsAccountsId...)
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

func RebuildHomeTimeline(account *models.Account) (err error) {
	statuses := make([]*models.Status, 0)
	followingAccounts, err := feed.GetAccountFollowingAccounts(account.Id)
	if err != nil {
		return
	}

	limit := feed.MAX_ITEMS / 2
	offset := 0

	accountsId := followingAccounts.GetIds()
	// 加上我自己
	accountsId = append(accountsId, account.Id)
	// 找出我关注的人和最近提及我的status
	err = db.GetDB().
		Where("account_id in (?) or id in (select status_id from status_to_accounts where account_id = ?)", accountsId, account.Id).
		Order("created_at desc").Limit(limit).Offset(offset).Find(&statuses).Error

	if err != nil {
		return
	}

	err = feed.CleanFeeds(&Home{Account: account}, []string{strconv.FormatUint(account.Id, 10)})
	if err != nil {
		return
	}

	for _, status := range statuses {
		err = PushToHome(account, status, false, false)
		if err != nil {
			return
		}
	}
	return
}

func GetTimeLineContent(account *models.Account, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeLine := &Home{Account: account}

	statusesId := make([]uint64, 0)
	recentRebuild := false

	// 判断是否有缓存, 没有缓存则重建
	redisConn := db.GetRedis()
	ctx := context.Background()
	timelineKey := fmt.Sprintf(timeLine.GetKeyFormat(), timeLine.GetTimelineId())
	if redisConn.Exists(ctx, timelineKey).Val() == 0 {
		RebuildHomeTimeline(account)
		recentRebuild = true
	}

	if params == nil {
		// 从缓存中取出timeline status id
		var statusesIdStr []string
		statusesIdStr, err = redisConn.ZRevRange(ctx, timelineKey, 0, -1).Result()
		if err != nil {
			return
		}

		statusesId = utils.StringNumberArrayConvertUint64(statusesIdStr)
	} else {
		var statusesIdStr []string

		if params.MaxId > 0 || params.MinId > 0 {
			min, max := params.GetRedisRangeMinMax()
			statusesIdStr = redisConn.ZRangeByScore(ctx, timelineKey, &redis.ZRangeBy{Min: min, Max: max}).Val()
		} else {
			start, end := params.GetRedisRangeOffsetCount()
			statusesIdStr = redisConn.ZRevRange(ctx, timelineKey, start, end).Val()
		}

		statusesId = utils.StringNumberArrayConvertUint64(statusesIdStr)
	}

	if len(statusesId) == 0 && !recentRebuild {
		// 没有找到 statuses id，尝试从数据库中找
		var followingAccounts models.Accounts
		followingAccounts, err = feed.GetAccountFollowingAccounts(account.Id)
		if err != nil {
			return
		}

		accountIds := followingAccounts.GetIds()
		// 加上我自己
		accountIds = append(accountIds, account.Id)

		query := db.GetDB().
			Where("account_id in (?) or id in (select status_id from status_to_accounts where account_id = ?)", accountIds, account.Id).
			Order("created_at desc")

		if params == nil {
			query = query.Limit(systemsettings.GetDefaultPageSize())
		} else {
			if params.MaxId > 0 {
				query = query.Where("id <= ? and id >=", params.MaxId, params.MinId)
			} else {
				query = query.Where("id >= ?", params.SinceId).Limit(int(params.Limit))
			}
		}

		statusesId = make([]uint64, 0)
		err = query.Pluck("id", &statusesId).Error
	}

	// 还是没有找到，返回空
	if len(statusesId) == 0 {
		return
	}

	// 找到了，从数据库中取出
	statuses = make([]*models.Status, 0)
	query := db.GetDB().Model(&models.Status{}).Preload("Account").
		Preload("Content").
		Preload("Application").
		Preload("Attachments").
		Preload("ReplyAccount").
		Preload("Emojis").
		Preload("ToAccounts").
		Preload("Poll").
		Preload("Poll.Options").
		Preload("Poll.Options").
		Preload("Tags").
		Preload("Reblog").
		Preload("Reblog.Account").
		Preload("Reblog.Content").
		Preload("Reblog.Application").
		Preload("Reblog.Attachments").
		Preload("Reblog.ReplyAccount").
		Preload("Reblog.Emojis").
		Preload("Reblog.ToAccounts").
		Preload("Reblog.Poll").
		Preload("Reblog.Poll.Options").
		Preload("Reblog.Poll.Options").
		Preload("Reblog.Tags")

	err = query.Where("id in (?)", statusesId).Find(&statuses).Error
	if err != nil {
		return
	}

	return
}
