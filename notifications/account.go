package notifications

import (
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func init() {
	event.Register("account", "follow", &event.Handler{Exec: func(event event.Event) bool {
		follow, ok := event.Data.(*models.Follow)
		if !ok {
			return true
		}

		safeGo("followNotification", func() { followNotification(follow) })

		return true
	}})

	event.Register("account", "follow-request", &event.Handler{Exec: func(event event.Event) bool {
		follow, ok := event.Data.(*models.Follow)
		if !ok {
			return true
		}

		safeGo("followRequestNotification", func() { followRequestNotification(follow) })

		return true
	}})
}

func followRequestNotification(follow *models.Follow) {
	// 自己关注自己的请求不通知
	if follow.AccountId == follow.FollowId {
		return
	}

	// 只通知本地用户
	isLocal, err := isLocalAccount(follow.FollowId)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[followRequestNotification] check local account faild, follow id %d", follow.Id)))
		return
	}

	if !isLocal {
		return
	}

	// 通知被关注的人
	nf := &models.Notification{
		AccountId:     follow.FollowId,
		FromAccountId: follow.AccountId,
		Type:          models.NotificationTypeFollowRequest,
	}

	err = db.GetDB().Create(nf).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[followRequestNotification] create notification faild, follow id %d", follow.Id)))
		return
	}
}

func followNotification(follow *models.Follow) {
	// 自己关注自己不通知
	if follow.AccountId == follow.FollowId {
		return
	}

	// 只通知本地用户
	isLocal, err := isLocalAccount(follow.FollowId)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[followNotification] check local account faild, follow id %d", follow.Id)))
		return
	}

	if !isLocal {
		return
	}

	// 防重复通知：如果已存在同类型同 from/to 的通知则不再创建（Follow.AfterUpdate 会重复触发 follow 事件）
	var count int64

	err = db.GetDB().Model(&models.Notification{}).
		Where("account_id = ? AND from_account_id = ? AND type = ?", follow.FollowId, follow.AccountId, models.NotificationTypeFollow).
		Count(&count).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[followNotification] check duplicate notification faild, follow id %d", follow.Id)))
		return
	}

	if count > 0 {
		return
	}

	// 通知被关注的人
	nf := &models.Notification{
		AccountId:     follow.FollowId,
		FromAccountId: follow.AccountId,
		Type:          models.NotificationTypeFollow,
	}

	err = db.GetDB().Create(nf).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[followNotification] create notification faild, follow id %d", follow.Id)))
		return
	}
}

// isLocalAccount 检查指定 id 的账号是否是本地账号 (Domain 为 nil)。
// 避免直接使用 Association("Account").Find() 的两个问题：
//  1. 当关联不存在时返回 nil pointer，后续访问 .Domain 会 panic；
//  2. 在 goroutine 中修改传入的共享 model 指针，产生 data race。
func isLocalAccount(accountId uint64) (bool, error) {
	if accountId == 0 {
		return false, nil
	}

	var account models.Account

	err := db.GetDB().Select("domain").Where("id = ?", accountId).First(&account).Error
	if err != nil {
		// 账号不存在，不发送通知
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}

		return false, err
	}

	// Domain 为 nil 表示本地账号
	return account.Domain == nil, nil
}
