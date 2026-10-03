package home

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kasuganosora/community/db"
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
		if event.Data == nil {
			return false
		}

		status := event.Data.(*models.Status) //nolint:forcetypeassert
		if status == nil {
			return false
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
	statuses := make(models.Statuses, 0)

	err = homeCandidates(db.GetDB().WithContext(ctx), timeline.Account.Id).Preload("ToAccounts").Order("statuses.id desc").Limit(feed.MAX_ITEMS / 2).Find(&statuses).Error
	if err != nil {
		return err
	}

	// 过滤掉只有对方可见的status
	statuses = statuses.Filter(func(status *models.Status) bool {
		if status.Visibility == models.StatusVisibilityDirect {
			return status.AccountId == timeline.Account.Id || status.ToAccounts.Contains(timeline.Account.Id)
		}

		return true
	})

	aggregate := true
	if timeline.Account.User != nil && timeline.Account.User.Settings != nil {
		aggregate = timeline.Account.User.Settings.GetAggregatesReblogs()
	}
	return feed.RebuildTimelineContext(ctx, timeline, statuses, aggregate)
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
			for _, account := range accounts {
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
	return feed.EligibleTimelineStatuses(tx.Model(&models.Status{})).Where("(statuses.account_id = ? OR statuses.account_id IN (?) OR statuses.id IN (?))", accountID, following, mentions).Where("(statuses.visibility <> ? OR statuses.account_id = ? OR statuses.id IN (?))", models.StatusVisibilityDirect, accountID, mentions)
}

func (timeline *HomeTimeline) AggregateReblogs() bool {
	if timeline.Account != nil && timeline.Account.User != nil && timeline.Account.User.Settings != nil {
		return timeline.Account.User.Settings.GetAggregatesReblogs()
	}
	return true
}
