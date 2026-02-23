package notifications

import (
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func init() {
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		status := event.Data.(*models.Status)
		//如果是新增的事件
		switch event.Name {
		case "after_create":
			go creatReblogNotification(status)  // 创建boost notification
			go creatMentionNotification(status) // 创建mention notification
		case "after_update":
			go updateReblogNotification(status)
			go updateMentionNotification(status)
		case "after_delete":
			go removeReblogNotification(status)
			go removeMentionNotification(status)
		}

		return true
	}})

	event.Register("favourite", "favourite", &event.Handler{Exec: func(event event.Event) bool {
		fs := event.Data.(*models.FavoriteStatus)
		go favouriteNotification(fs)
		return true
	}})

	event.Register("unfavourite", "favourite", &event.Handler{Exec: func(event event.Event) bool {
		fs := event.Data.(*models.FavoriteStatus)
		go unfavouriteNotification(fs)
		return true
	}})
}

// unfavouriteNotification 取消收藏通知
// 从原推送的作者那里删除
func unfavouriteNotification(fs *models.FavoriteStatus) {
	//如果是取消收藏的事件
	var err error
	if fs.Status == nil {
		fs.Status = &models.Status{}
		err = db.GetDB().Model(&models.Status{}).Preload("Reblog").Preload("Account").Where("id = ?", fs.StatusId).First(fs.Status).Error
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[unfavouriteNotification] get status faild, status id %d", fs.StatusId))
			return
		}
	}

	originalStatus := fs.Status
	if originalStatus.ReblogId != nil && *originalStatus.ReblogId > 0 {
		if originalStatus.Reblog == nil {
			err = errors.New("reblog status is nil")
			err = errors.Wrap(err, fmt.Sprintf("[unfavouriteNotification] reblog status id is %d", *originalStatus.ReblogId))
			log.ErrorV(err)
			return
		}
		originalStatus = originalStatus.Reblog
	}

	// 删除对应的通知
	ns := &models.Notification{}
	err = db.GetDB().Where("type = ? and status_id = ?", models.NotificationTypeFavourite, originalStatus.Id).First(ns).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		err = errors.Wrap(err, fmt.Sprintf("[unfavouriteNotification] find need remove favourite notification error, status id %d", originalStatus.Id))
		return
	}

	err = db.GetDB().Delete(ns).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[unfavouriteNotification] remove favourite notification error, status id %d", originalStatus.Id))
		return
	}

}

// favouriteNotification 收藏通知
// 是发给这个status的作者的
func favouriteNotification(fs *models.FavoriteStatus) {
	ns := &models.Notification{}
	ns.Type = models.NotificationTypeFavourite
	ns.StatusId = fs.StatusId
	var err error

	if fs.Status == nil {
		fs.Status = &models.Status{}
		err = db.GetDB().Model(&models.Status{}).Preload("Reblog").Preload("Account").Where("id = ?", fs.StatusId).First(fs.Status).Error
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[favouriteNotification] get status faild, status id %d", fs.StatusId))
			return
		}
	}

	originalStatus := fs.Status
	if originalStatus.ReblogId != nil && *originalStatus.ReblogId > 0 {
		if originalStatus.Reblog == nil {
			err = errors.New("reblog status is nil")
			err = errors.Wrap(err, fmt.Sprintf("[favouriteNotification] reblog status id is %d", *originalStatus.ReblogId))
			log.ErrorV(err)
			return
		}
		originalStatus = originalStatus.Reblog
	}

	// 只给本地的账号发通知
	if originalStatus.Account.Domain != nil {
		return
	}

	// 发给这个status的作者
	ns.AccountId = originalStatus.AccountId

	err = db.GetDB().Create(ns).Error

	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[favouriteNotification] create favourite notification error, status id %d", originalStatus.Id))
		return
	}
}

// removeMentionNotification 删除mention通知
func removeMentionNotification(status *models.Status) {
	// 删除对应的通知
	nsList := make([]*models.Notification, 0)
	err := db.GetDB().Where("type = ? and status_id = ?", models.NotificationTypeMention, status.Id).Find(&nsList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[removeMentionNotification] find need remove mention notification error, status id %d", status.Id))
		return
	}

	if len(nsList) == 0 {
		return
	}

	err = db.GetDB().Delete(nsList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[removeMentionNotification] remove mention notification error, status id %d", status.Id))
		return
	}

}

// updateMentionNotification 更新mention通知
// 如果有新的mention账号，就创建新的mention通知
// 如果有的账号不再是mention账号，就删除对应的通知
// 给现有的账号发送更新status通知
func updateMentionNotification(status *models.Status) {
	// 获取原来的Mention 通知
	oldNsList := make([]*models.Notification, 0)
	err := db.GetDB().Where("type = ? and status_id = ?", models.NotificationTypeMention, status.Id).Find(&oldNsList).Error

	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] find old mention notification error, status id %d", status.Id))
		return
	}

	// 新的 mention 账号列表
	newMentionAccountList := make([]uint64, 0)
	err = db.GetDB().Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Pluck("account_id", &newMentionAccountList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] find new mention account list error, status id %d", status.Id))
		return
	}

	newMentionAccountListExistsMap := utils.GetExistsMap(newMentionAccountList)
	removeIdCheckMap := make(map[uint64]bool)
	// 找出那些需要删除的通知
	needRemoveNsList := make([]*models.Notification, 0)
	for _, oldNs := range oldNsList {
		if _, ok := newMentionAccountListExistsMap[oldNs.AccountId]; !ok {
			needRemoveNsList = append(needRemoveNsList, oldNs)
			removeIdCheckMap[oldNs.AccountId] = true
		}
	}

	if len(needRemoveNsList) > 0 {
		err = db.GetDB().Delete(needRemoveNsList).Error
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] remove mention notification error, status id %d", status.Id))
			return
		}
	}

	accounts, err := models.GetLocalAccounts(db.GetDB(), newMentionAccountList)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] get local accounts error, status id %d", status.Id))
		return
	}

	// 找出那些需要创建的通知（新提及的但不在旧通知列表中的账号）
	oldNotifiedAccountMap := make(map[uint64]bool)
	for _, oldNs := range oldNsList {
		oldNotifiedAccountMap[oldNs.AccountId] = true
	}
	needCreateNsList := make([]*models.Notification, 0)
	for _, account := range accounts {
		if _, ok := oldNotifiedAccountMap[account.Id]; !ok {
			nf := &models.Notification{}
			nf.Type = models.NotificationTypeMention
			nf.StatusId = status.Id
			nf.AccountId = account.Id
			nf.FromAccountId = status.AccountId
			needCreateNsList = append(needCreateNsList, nf)
		}
	}

	if len(needCreateNsList) > 0 {
		err = db.GetDB().Create(needCreateNsList).Error
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] create mention notification error, status id %d", status.Id))
			return
		}
	}

	// 现有的用户发送更新通知
	updateNfs := make([]*models.Notification, 0)
	for _, oldNs := range oldNsList {
		if _, ok := removeIdCheckMap[oldNs.AccountId]; ok {
			continue
		}

		nf := &models.Notification{}
		nf.Type = models.NotificationTypeStatusUpdate
		nf.StatusId = status.Id
		nf.AccountId = oldNs.AccountId
		nf.FromAccountId = status.AccountId
		updateNfs = append(updateNfs, nf)
	}

	if len(updateNfs) > 0 {
		err = db.GetDB().Create(updateNfs).Error
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] create update notification error, status id %d", status.Id))
			return
		}
	}

}

func creatMentionNotification(status *models.Status) {
	newMentionAccountList := make([]uint64, 0)
	err := db.GetDB().Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Pluck("account_id", &newMentionAccountList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[creatMentionNotification] find new mention account list error, status id %d", status.Id))
		return
	}

	if len(newMentionAccountList) == 0 {
		return
	}

	accounts, err := models.GetLocalAccounts(db.GetDB(), newMentionAccountList)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[creatMentionNotification] get local accounts error, status id %d", status.Id))
		return
	}

	nsList := make([]*models.Notification, 0)
	for _, account := range accounts {
		nf := &models.Notification{}
		nf.Type = models.NotificationTypeMention
		nf.StatusId = status.Id
		nf.FromAccountId = status.AccountId
		nf.AccountId = account.Id
		nsList = append(nsList, nf)
	}

	err = db.GetDB().Create(nsList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[creatMentionNotification] create mention notification error, status id %d", status.Id))
		return
	}
}

func removeReblogNotification(status *models.Status) {
	if status.ReblogId == nil || *status.ReblogId == 0 {
		return
	}

	if status.Reblog == nil {
		status.Reblog = &models.Status{}
		err := db.GetDB().Model(status).Association("Reblog").Find(status.Reblog)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[removeReblogNotification] find reblog status error, status id %d", status.Id))
			log.ErrorV(err)
			return
		}
	}

	// 删除对应的通知
	nsList := make([]*models.Notification, 0)
	err := db.GetDB().
		Where("type In ? and status_id = ?", []int{models.NotificationTypeReblog, models.NotificationTypeStatusUpdate}, status.Reblog.Id).
		Find(&nsList).Error

	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[removeReblogNotification] find reblog notification error, status id %d", status.Id))
		log.ErrorV(err)
		return
	}

	if len(nsList) == 0 {
		return
	}

	// 删除对应的通知
	err = db.GetDB().Delete(nsList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[removeReblogNotification] delete reblog notification error, status id %d", status.Id))
		log.ErrorV(err)
	}
}

// updateReblogNotification 更新转发通知
// 告诉所有转发人，这篇文章被修改了
func updateReblogNotification(status *models.Status) {
	if status.ReblogId == nil || *status.ReblogId == 0 {
		return
	}

	if status.Reblog == nil {
		status.Reblog = &models.Status{}
		err := db.GetDB().Model(status).Association("Reblog").Find(status.Reblog)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[updateReblogNotification] find reblog status error, status id %d", status.Id))
			log.ErrorV(err)
			return
		}
	}

	// 获取原来的转发通知
	accountsId := make([]uint64, 0)
	err := db.GetDB().Model(&models.Status{}).Where("reblog_id =?", *status.ReblogId).Pluck("account_id", &accountsId).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[updateReblogNotification] find reblog accounts error, status id %d", status.Reblog.Id))
		return
	}

	// 通知只发给本站用户
	accounts, err := models.GetLocalAccounts(db.GetDB(), accountsId)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[updateReblogNotification] get local accounts error, status id %d", status.Reblog.Id))
		return
	}

	// 发送帖子修改通知
	nfList := make([]*models.Notification, 0)
	for _, account := range accounts {
		nf := &models.Notification{}
		nf.FromAccountId = status.AccountId
		nf.AccountId = account.Id
		nf.Type = models.NotificationTypeStatusUpdate
		nf.StatusId = status.Reblog.Id
		nfList = append(nfList, nf)
	}

	err = db.GetDB().Create(nfList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[updateReblogNotification] create reblog notification error, status id %d", status.Reblog.Id))
		return
	}

}

// creatReblogNotification 创建转发通知
// 通知原作者，有人转发了他的文章
func creatReblogNotification(status *models.Status) {
	if status.ReblogId == nil || *status.ReblogId == 0 {
		return
	}

	if status.Reblog == nil {
		status.Reblog = &models.Status{}
		err := db.GetDB().Model(status).Association("Reblog").Find(status.Reblog)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[creatReblogNotification] find reblog status error, status id %d", status.Id))
			log.ErrorV(err)
			return
		}
	}

	if status.Account == nil {
		status.Account = &models.Account{}
		err := db.GetDB().Model(status).Association("Account").Find(status.Account)
		if err != nil {
			err = errors.Wrap(err, fmt.Sprintf("[creatReblogNotification] find status account error, status id %d", status.ReblogId))
			log.ErrorV(err)
			return
		}
	}

	// 如果不是本站的用户，不需要创建通知
	if status.Account.Domain != nil {
		return
	}

	nf := &models.Notification{}
	nf.FromAccountId = status.AccountId
	nf.AccountId = status.Reblog.AccountId
	nf.Type = models.NotificationTypeReblog
	nf.StatusId = *status.ReblogId
	nf.Status = status.Reblog

	err := db.GetDB().Create(nf).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[creatReblogNotification] create reblog notification error, status id %d", status.ReblogId))
		log.ErrorV(err)
		return
	}
}
