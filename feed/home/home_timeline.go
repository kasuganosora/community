package home

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
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

		// 如果是新增的事件
		switch event.Name {
		case "after_create":
			go func() {
				err := Push(status, false)
				if err != nil {
					log.ErrorV(err)
				}
			}()
		case "before_update":
			go ClearMentionsPushStatus(status)
		case "after_update":
			go func() {
				err := Push(status, true)
				if err != nil {
					log.ErrorV(err)
				}
			}()
		case "after_delete":
			go func() {
				err := Unpush(status)
				if err != nil {
					log.ErrorV(err)
				}
			}()
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

func (timeline *HomeTimeline) Rebuild() error { return timeline.RebuildContext(context.Background()) }
func (timeline *HomeTimeline) RebuildContext(ctx context.Context) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	lease, leaseErr := feed.BeginRebuild(ctx, timeline)
	if leaseErr != nil {
		return leaseErr
	}
	defer feed.ReleaseRebuild(ctx, timeline, lease)

	statuses := make(models.Statuses, 0)

	err = homeCandidates(db.GetDB().WithContext(ctx), timeline.Account.Id).Order(feed.StatusTimelineOrder).Limit(feed.MAX_ITEMS).Find(&statuses).Error
	if err != nil {
		return err
	}

	return feed.PublishRebuild(ctx, timeline, lease, statuses, timeline.AggregateReblogs())
}

// ClearMentionsPushStatus 清除提及的推送状态.
func ClearMentionsPushStatus(status *models.Status) {
	// 查找原来的提及用户信息
	toAccounts := make([]*models.StatusToAccount, 0)

	err := db.GetDB().Model(
		&models.StatusToAccount{}).
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
		aggregatesReblogs = homeTimeline.Account.User.Settings.GetAggregatesReblogs()
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
		aggregatesReblogs = homeTimeline.Account.User.Settings.GetAggregatesReblogs()
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
	if status == nil || db.GetDB() == nil {
		return nil
	}
	cloned := *status
	status = &cloned
	if status.ToAccounts == nil {
		status.ToAccounts = make(models.Accounts, 0)

		err = db.GetDB().Model(status).Association("ToAccounts").Find(&status.ToAccounts)

		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))

			return err
		}
	}

	var accountsId []uint64

	if status.Visibility == models.StatusVisibilityDirect {
		accountsId = status.ToAccounts.GetIds()
	} else {
		accountsId, err = feed.GetAccountRecentActiveLocalFollowersId(status.AccountId)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))

			return err
		}
		// 再加上这个status提及的用户
		accountsId = append(accountsId, status.ToAccounts.GetIds()...)
	}

	// 加上这个status的作者
	accountsId = append(accountsId, status.AccountId)
	accountsId = utils.UniqueSlice(accountsId)
	excluded, exclErr := ExclusiveHomeRecipients(db.GetDB(), status.AccountId, accountsId)
	if exclErr != nil {
		return errors.Wrap(exclErr, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
	}
	if len(excluded) > 0 {
		kept := make([]uint64, 0, len(accountsId))
		for _, id := range accountsId {
			if !excluded[id] {
				kept = append(kept, id)
			}
		}
		accountsId = kept
	}

	// 推送到发这个status的用户的粉丝和提及的用户
	go func() {
		var goroutineErr error // 使用局部变量避免与外部 err 的数据竞争

		accounts := make([]*models.Account, 0)

		goroutineErr = db.GetDB().Model(
			&models.Account{}).Preload("User").Preload("User.Settings").Where("id in (?)", accountsId).Find(&accounts).Error
		if goroutineErr != nil {
			goroutineErr = errors.Wrap(goroutineErr, fmt.Sprintf("Push status to followers failed, status id %d, to accounts %v", status.Id, accountsId))
			log.ErrorV(goroutineErr)

			return
		}

		isReblog := status.ReblogId != nil && *status.ReblogId > 0

		if !isReblog {
			// 非转发：使用批量添加优化性能（Redis Pipeline）
			// 注意：非转发情况下 aggregateReblogs 参数不影响结果，因为 reblog 分支不会被进入
			timelines := make([]feed.Timeline, 0, len(accounts))
			for _, account := range accounts {
				timelines = append(timelines, &HomeTimeline{Account: account})
			}

			goroutineErr = feed.BatchAddToTimelineItems(timelines, status, false)

			if goroutineErr != nil {
				goroutineErr = errors.Wrap(goroutineErr, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
				log.ErrorV(goroutineErr)

				return
			}

			for _, timeline := range timelines {
				goroutineErr = feed.TrimTimeline(timeline)
				if goroutineErr != nil {
					goroutineErr = errors.Wrap(goroutineErr, fmt.Sprintf("Push status to followers failed, status id %d", status.Id))
					log.ErrorV(goroutineErr)

					return
				}
			}
			// 推送到客户端
			for _, account := range accounts {
				homeTimeline := &HomeTimeline{Account: account}
				if feed.PushUpdateRequired(homeTimeline) {
					method := "update"
					if update {
						method = "status.update"
					}

					goroutineErr = feed.PushToClient(homeTimeline, method, status)

					if goroutineErr != nil {
						goroutineErr = errors.Wrap(goroutineErr, fmt.Sprintf("Push status to followers failed, status id %d, to account %d", status.Id, account.Id))
						log.ErrorV(goroutineErr)

						continue
					}
				}
			}
		} else {
			// 转发：保留单条调用，因为每个用户可能有不同的 aggregatesReblogs 设置
			// Followers who turned this booster's reblogs off don't get it.
			hidden := make(map[uint64]bool)
			hiddenIDs := make([]uint64, 0)
			conn := db.GetDB()
			if goroutineErr = conn.Model(&models.Follow{}).Where("follows.follow_id = ? AND follows.account_id IN ?", status.AccountId, accountsId).Where(models.HiddenReblogsCondition(conn)).Pluck("follows.account_id", &hiddenIDs).Error; goroutineErr != nil {
				log.ErrorV(errors.Wrap(goroutineErr, fmt.Sprintf("load hidden reblog followers failed, status id %d", status.Id)))
			}
			for _, id := range hiddenIDs {
				hidden[id] = true
			}
			for _, account := range accounts {
				if hidden[account.Id] && account.Id != status.AccountId {
					continue
				}
				goroutineErr = PushToHome(account, status, update, true)
				if goroutineErr != nil {
					goroutineErr = errors.Wrap(goroutineErr, fmt.Sprintf("Push status to followers failed, status id %d, to account %d", status.Id, account.Id))
					log.ErrorV(goroutineErr)

					continue
				}
			}
		}
	}()

	return err
}

func Unpush(status *models.Status) (err error) {
	if status == nil || db.GetDB() == nil {
		return nil
	}
	cloned := *status
	status = &cloned
	if status.ToAccounts == nil {
		status.ToAccounts = make(models.Accounts, 0)

		err = db.GetDB().Model(status).Association("ToAccounts").Find(&status.ToAccounts)

		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Unpush status to followers failed, status id %d", status.Id))

			return err
		}
	}

	var accountsId []uint64

	if status.Visibility == models.StatusVisibilityDirect {
		accountsId = status.ToAccounts.GetIds()
	} else {
		accountsId, err = feed.GetAccountRecentActiveLocalFollowersId(status.AccountId)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("Unpush status to followers failed, status id %d", status.Id))

			return err
		}
		// 再加上这个status提及的用户
		accountsId = append(accountsId, status.ToAccounts.GetIds()...)
	}

	// 加上这个status的作者
	accountsId = append(accountsId, status.AccountId)
	accountsId = utils.UniqueSlice(accountsId)

	// 推送到发这个status的用户的粉丝和提及的用户
	go func() {
		var goroutineErr error // 使用局部变量避免与外部 err 的数据竞争

		accounts := make([]*models.Account, 0)

		goroutineErr = db.GetDB().Preload("User").Preload("User.Settings").Where("id in (?)", accountsId).Find(&accounts).Error
		if goroutineErr != nil {
			goroutineErr = errors.Wrap(goroutineErr, fmt.Sprintf("Unpush status to followers failed, status id %d, to accounts %v", status.Id, accountsId))
			log.ErrorV(goroutineErr)

			return
		}

		for _, account := range accounts {
			goroutineErr = UnPushFromHome(account, status, true)
			if goroutineErr != nil {
				goroutineErr = errors.Wrap(goroutineErr, fmt.Sprintf("Unpush status to followers failed, status id %d, to account %d", status.Id, account.Id))
				log.ErrorV(goroutineErr)

				continue
			}
		}
	}()

	return err
}

func GetTimeLineContent(account *models.Account, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeLine := &HomeTimeline{Account: account}
	if params == nil {
		params = &feed.ListParam{}
	}
	params.Viewer = account

	newFilterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		return homeCandidates(tx, account.Id), nil
	}

	return feed.GetStatusTimelineContent(timeLine, params, newFilterCondition, nil)
}

func homeCandidates(tx *gorm.DB, accountID uint64) *gorm.DB {
	following := tx.Session(&gorm.Session{NewDB: true}).Model(&models.Follow{}).Where("account_id = ? AND status = ?", accountID, models.FollowStatusFollowed).Select("follow_id")
	mentions := tx.Session(&gorm.Session{NewDB: true}).Model(&models.StatusToAccount{}).Where("account_id = ?", accountID).Select("status_id")
	hiddenReblogs := tx.Session(&gorm.Session{NewDB: true}).Model(&models.Follow{}).Where("follows.account_id = ?", accountID).Where(models.HiddenReblogsCondition(tx)).Select("follows.follow_id")
	exclusiveMembers := tx.Session(&gorm.Session{NewDB: true}).Model(&models.ListAccount{}).
		Joins("JOIN lists ON lists.id = list_accounts.list_id").
		Where("lists.account_id = ? AND lists.exclusive = ? AND lists.deleted_at IS NULL", accountID, true).
		Select("list_accounts.account_id")
	profileBackfill := dialect.JSONText(tx, "statuses.properties", models.StatusPropProfileBackfill)
	return feed.EligibleTimelineStatuses(tx.Model(&models.Status{})).
		Where("(statuses.reblog_id IS NULL OR statuses.reblog_id = 0 OR statuses.account_id NOT IN (?))", hiddenReblogs).
		Where("COALESCE("+profileBackfill+", '') <> '1'").
		Where("(COALESCE("+dialect.JSONText(tx, "statuses.properties", models.StatusPropBackfilled)+", '') <> '1' OR statuses.account_id = ? OR statuses.id IN (?))", accountID, mentions).
		Where("(statuses.account_id = ? OR statuses.account_id IN (?) OR statuses.id IN (?))", accountID, following, mentions).
		Where("(statuses.account_id = ? OR statuses.account_id NOT IN (?))", accountID, exclusiveMembers).
		Where("(statuses.visibility <> ? OR statuses.account_id = ? OR statuses.id IN (?))", models.StatusVisibilityDirect, accountID, mentions)
}

func (timeline *HomeTimeline) AggregateReblogs() bool {
	if timeline != nil && timeline.Account != nil && timeline.Account.User != nil && timeline.Account.User.Settings != nil {
		return timeline.Account.User.Settings.GetAggregatesReblogs()
	}
	return true
}
