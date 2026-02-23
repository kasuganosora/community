package notifications

import (
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

func init() {
	event.Register("account", "follow", &event.Handler{Exec: func(event event.Event) bool {
		follow := event.Data.(*models.Follow)
		go followNotification(follow)
		return true
	}})

	event.Register("account", "follow-request", &event.Handler{Exec: func(event event.Event) bool {
		follow := event.Data.(*models.Follow)
		go followRequestNotification(follow)
		return true
	}})
}

func followRequestNotification(follow *models.Follow) {
	var err error
	if follow.Account == nil {
		follow.Account = &models.Account{}
		err = db.GetDB().Model(follow).Association("Account").Find(follow.Account)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[followRequestNotification] get account faild, follow id %d", follow.Id))
			log.ErrorV(err)
			return
		}
	}

	// 通知
	if follow.Account.Domain != nil {
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
		err = errors.Wrap(err, fmt.Sprintf("[followRequestNotification] create notification faild, follow id %d", follow.Id))
		log.ErrorV(err)
		return
	}
}

func followNotification(follow *models.Follow) {
	var err error
	if follow.Account == nil {
		follow.Account = &models.Account{}
		err = db.GetDB().Model(follow).Association("Account").Find(follow.Account)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[followNotification] get account faild, follow id %d", follow.Id))
			log.ErrorV(err)
			return
		}
	}

	// 通知
	if follow.Account.Domain != nil {
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
		err = errors.Wrap(err, fmt.Sprintf("[followNotification] create notification faild, follow id %d", follow.Id))
		log.ErrorV(err)
		return
	}
}
