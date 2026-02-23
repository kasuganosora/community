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
	event.Register("list", "add-to-list", &event.Handler{Exec: func(event event.Event) bool {
		la := event.Data.(*models.ListAccount)
		go listAddToListNotification(la)
		return true
	}})
	event.Register("list", "follow-list", &event.Handler{Exec: func(event event.Event) bool {
		lfa := event.Data.(*models.ListFollowAccount)
		go listFollowListNotification(lfa)
		return true
	}})
}

// listAddToListNotification 给被添加的人发通知
// 只给本地用户发
func listAddToListNotification(la *models.ListAccount) {
	list := &models.List{}
	err := db.GetDB().Model(list).Where("id = ?", la.ListId).First(list).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[listAddToListNotification] get list faild, list id %d", la.ListId))
		log.ErrorV(err)
		return
	}

	account := &models.Account{}
	err = db.GetDB().Model(account).Where("id = ?", la.AccountId).First(account).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[listAddToListNotification] get account faild, account id %d", la.AccountId))
		log.ErrorV(err)
		return
	}

	if account.Domain != nil {
		return
	}

	nf := &models.Notification{
		AccountId:     la.AccountId,
		FromAccountId: list.AccountId,
		Type:          models.NotificationTypeAddList,
		ListId:        list.Id,
	}

	err = db.GetDB().Create(nf).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[listAddToListNotification] create notification faild, list id %d", la.ListId))
		log.ErrorV(err)
		return
	}
}

// listFollowListNotification 有人关注了这个列表的时候给列表的创建者发通知
func listFollowListNotification(lfa *models.ListFollowAccount) {
	list := &models.List{}
	err := db.GetDB().Model(list).Preload("Creator").Where("id = ?", lfa.ListId).First(list).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[listFollowListNotification] get list faild, list id %d", lfa.ListId))
		log.ErrorV(err)
		return
	}

	if list.Creator.Domain != nil {
		return
	}

	nf := &models.Notification{
		AccountId:     list.AccountId,
		FromAccountId: lfa.AccountId,
		Type:          models.NotificationTypeFollow,
		ListId:        list.Id,
	}

	err = db.GetDB().Create(nf).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[listFollowListNotification] create notification faild, list id %d", lfa.ListId))
		log.ErrorV(err)
		return
	}

}
