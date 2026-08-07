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
	event.Register("list", "add-to-list", &event.Handler{Exec: func(event event.Event) bool {
		la, ok := event.Data.(*models.ListAccount)
		if !ok {
			return true
		}

		safeGo("listAddToListNotification", func() { listAddToListNotification(la) })

		return true
	}})

	event.Register("list", "follow-list", &event.Handler{Exec: func(event event.Event) bool {
		lfa, ok := event.Data.(*models.ListFollowAccount)
		if !ok {
			return true
		}

		safeGo("listFollowListNotification", func() { listFollowListNotification(lfa) })

		return true
	}})
}

// listAddToListNotification 给被添加的人发通知
// 只给本地用户发.
func listAddToListNotification(la *models.ListAccount) {
	list := &models.List{}

	err := db.GetDB().Model(list).Where("id = ?", la.ListId).First(list).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[listAddToListNotification] get list faild, list id %d", la.ListId)))
		}

		return
	}

	account := &models.Account{}

	err = db.GetDB().Model(account).Where("id = ?", la.AccountId).First(account).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[listAddToListNotification] get account faild, account id %d", la.AccountId)))
		}

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
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[listAddToListNotification] create notification faild, list id %d", la.ListId)))
		return
	}
}

// listFollowListNotification 有人关注了这个列表的时候给列表的创建者发通知.
func listFollowListNotification(lfa *models.ListFollowAccount) {
	list := &models.List{}

	err := db.GetDB().Model(list).Preload("Creator").Where("id = ?", lfa.ListId).First(list).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[listFollowListNotification] get list faild, list id %d", lfa.ListId)))
		}

		return
	}

	// Preload("Creator") 未找到关联时 list.Creator 为 nil，需做 nil 检查
	if list.Creator == nil {
		return
	}

	// 如果列表创建者是远程用户，不发通知
	if list.Creator.Domain != nil {
		return
	}

	// 自己关注自己创建的列表不通知
	if list.AccountId == lfa.AccountId {
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
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[listFollowListNotification] create notification faild, list id %d", lfa.ListId)))
		return
	}
}
