package notifications

import (
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		status, ok := event.Data.(*models.Status)
		if !ok {
			return true
		}

		// 如果是新增的事件
		switch event.Name {
		case "after_create":
			if status.IsProfileBackfill() {
				// Old posts imported for a profile view are history, not news.
				return true
			}
			safeGo("creatReblogNotification", func() { creatReblogNotification(status) }) // 创建boost notification
			dispatchMentionNotification(status, false)
		case "after_update":
			safeGo("updateReblogNotification", func() { updateReblogNotification(status) })
			dispatchMentionNotification(status, true)
		case "after_delete":
			safeGo("removeReblogNotification", func() { removeReblogNotification(status) })
			safeGo("removeMentionNotification", func() { removeMentionNotification(status) })
		}

		return true
	}})

	event.Register("favourite", "favourite", &event.Handler{Exec: func(event event.Event) bool {
		fs, ok := event.Data.(*models.FavoriteStatus)
		if !ok {
			return true
		}

		safeGo("favouriteNotification", func() { favouriteNotification(fs) })

		return true
	}})

	event.Register("unfavourite", "favourite", &event.Handler{Exec: func(event event.Event) bool {
		fs, ok := event.Data.(*models.FavoriteStatus)
		if !ok {
			return true
		}

		safeGo("unfavouriteNotification", func() { unfavouriteNotification(fs) })

		return true
	}})
}

// Locking the saved status waits for its transaction to commit or roll back.
// Read recipients only after that barrier, including when an edit rolls back.
func dispatchMentionNotification(status *models.Status, update bool) {
	statusID := status.Id
	version := mentionStatusVersion{content: status.Content}
	if status.UpdatedAt != nil {
		version.updatedAt = *status.UpdatedAt
	}
	conn := db.GetDB()
	safeGo("mentionNotification", func() {
		if err := processMentionNotification(conn, statusID, update, version); err != nil {
			log.ErrorV(errors.Wrap(err, "process committed status mentions"))
		}
	})
}

type mentionStatusVersion struct {
	updatedAt time.Time
	content   models.StatusContent
}

func processMentionNotification(conn *gorm.DB, statusID uint64, update bool, version mentionStatusVersion) error {
	var created []*models.Notification
	err := conn.Transaction(func(tx *gorm.DB) error {
		var status models.Status
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&status, statusID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// The save may have rolled back, or a newer edit may already have won.
		// MySQL stores these timestamps at millisecond precision.
		if !version.updatedAt.IsZero() && (status.UpdatedAt == nil || !status.UpdatedAt.Truncate(time.Millisecond).Equal(version.updatedAt.Truncate(time.Millisecond))) {
			return nil
		}
		// Distinguish content edits even when two saves share a millisecond.
		if version.content.StatusId != 0 {
			var content models.StatusContent
			if err := tx.First(&content, "status_id = ?", statusID).Error; err != nil {
				return err
			}
			if content.Content != version.content.Content || content.SpoilerText != version.content.SpoilerText {
				return nil
			}
		}
		ids, err := mentionAccountIDs(tx, statusID)
		if err != nil {
			return err
		}
		// Publish notification events only after their rows commit; otherwise
		// the feed can publish records that disappear if this worker rolls back.
		tx = tx.Session(&gorm.Session{SkipHooks: true})
		if update {
			return updateMentionNotificationForAccounts(tx, &status, ids, &created)
		}
		return createMentionNotificationForAccounts(tx, &status, ids, &created)
	})
	if err != nil {
		return err
	}
	for _, n := range created {
		event.Trigger("notification", event.Event{Name: "after_create", Data: n})
	}
	return nil
}

func createMentionBatch(tx *gorm.DB, rows []*models.Notification, created *[]*models.Notification) error {
	if err := tx.Create(rows).Error; err != nil {
		return err
	}
	if created != nil {
		*created = append(*created, rows...)
	}
	return nil
}

func mentionAccountIDs(tx *gorm.DB, statusID uint64) ([]uint64, error) {
	var status models.Status
	if err := tx.Preload("ToAccounts").Preload("Account").First(&status, statusID).Error; err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(status.ToAccounts))
	for _, account := range status.ToAccounts {
		visible, err := status.CanViewEx(tx, account)
		if err != nil {
			return nil, err
		}
		if visible {
			ids = append(ids, account.Id)
		}
	}
	return ids, nil
}

// unfavouriteNotification 取消收藏通知
// 从原推送的作者那里删除.
func unfavouriteNotification(fs *models.FavoriteStatus) {
	originalStatus := getOriginalStatus(fs)
	if originalStatus == nil {
		return
	}

	// 删除对应的通知，加上 from_account_id 和 account_id 条件，
	// 确保只删除当前用户发出的收藏通知，避免误删其他用户的收藏通知。
	ns := &models.Notification{}

	err := db.GetDB().Where(
		"type = ? AND status_id = ? AND from_account_id = ?",
		models.NotificationTypeFavourite, originalStatus.Id, fs.AccountId,
	).First(ns).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}

		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[unfavouriteNotification] find need remove favourite notification error, status id %d", originalStatus.Id)))
		return
	}

	err = db.GetDB().Delete(ns).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[unfavouriteNotification] remove favourite notification error, status id %d", originalStatus.Id)))
		return
	}
}

// getOriginalStatus 解析 fs.Status，如果 status 是 reblog 则返回原始 status，
// 否则返回 status 本身。如果无法获取则返回 nil。
// 使用局部副本查询，不修改传入的共享 model 指针，避免 data race。
func getOriginalStatus(fs *models.FavoriteStatus) *models.Status {
	var status *models.Status

	if fs.Status == nil {
		status = &models.Status{}

		err := db.GetDB().Model(&models.Status{}).Preload("Reblog").Where("id = ?", fs.StatusId).First(status).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.ErrorV(errors.Wrap(err, fmt.Sprintf("[getOriginalStatus] get status faild, status id %d", fs.StatusId)))
			}

			return nil
		}
	} else {
		status = fs.Status
	}

	originalStatus := status
	if originalStatus.ReblogId != nil && *originalStatus.ReblogId > 0 {
		if originalStatus.Reblog == nil {
			log.ErrorV(errors.Wrap(errors.New("reblog status is nil"), fmt.Sprintf("[getOriginalStatus] reblog status id is %d", *originalStatus.ReblogId)))
			return nil
		}

		originalStatus = originalStatus.Reblog
	}

	return originalStatus
}

// favouriteNotification 收藏通知
// 是发给这个status的作者的.
func favouriteNotification(fs *models.FavoriteStatus) {
	originalStatus := getOriginalStatus(fs)
	if originalStatus == nil {
		return
	}

	// 获取 status 作者的账号信息，使用局部变量避免修改共享 model 指针（data race）
	account, err := getStatusAccount(originalStatus)
	if err != nil {
		return
	}

	if account == nil {
		return
	}

	// 只给本地的账号发通知
	if account.Domain != nil {
		return
	}

	// 自己收藏自己的 status 不通知
	if fs.AccountId == originalStatus.AccountId {
		return
	}

	// 发给这个status的作者，使用 originalStatus.Id 而非 fs.StatusId
	// （fs.StatusId 可能是 reblog 的 id，需要用原始 status 的 id 保持一致）
	ns := &models.Notification{
		Type:          models.NotificationTypeFavourite,
		StatusId:      originalStatus.Id,
		AccountId:     originalStatus.AccountId,
		FromAccountId: fs.AccountId,
	}

	err = db.GetDB().Create(ns).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[favouriteNotification] create favourite notification error, status id %d", originalStatus.Id)))
		return
	}
}

// removeMentionNotification 删除mention通知.
func removeMentionNotification(status *models.Status) {
	// 删除对应的通知
	nsList := make([]*models.Notification, 0)

	err := db.GetDB().Where("type = ? and status_id = ?", models.NotificationTypeMention, status.Id).Find(&nsList).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[removeMentionNotification] find need remove mention notification error, status id %d", status.Id)))
		return
	}

	if len(nsList) == 0 {
		return
	}

	err = db.GetDB().Delete(nsList).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[removeMentionNotification] remove mention notification error, status id %d", status.Id)))
		return
	}
}

// updateMentionNotification 更新mention通知
// 如果有新的mention账号，就创建新的mention通知
// 如果有的账号不再是mention账号，就删除对应的通知
// 给现有的账号发送更新status通知.
func updateMentionNotification(status *models.Status) {
	ids, err := mentionAccountIDs(db.GetDB(), status.Id)
	if err != nil {
		log.ErrorV(errors.Wrap(err, "read updated status mentions"))
		return
	}
	_ = updateMentionNotificationForAccounts(db.GetDB(), status, ids, nil)
}

func updateMentionNotificationForAccounts(tx *gorm.DB, status *models.Status, newMentionAccountList []uint64, created *[]*models.Notification) error {
	// 获取原来的Mention 通知
	oldNsList := make([]*models.Notification, 0)

	err := tx.Where("type = ? and status_id = ?", models.NotificationTypeMention, status.Id).Find(&oldNsList).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] find old mention notification error, status id %d", status.Id)))
		return err
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
		err = tx.Delete(needRemoveNsList).Error
		if err != nil {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] remove mention notification error, status id %d", status.Id)))
			return err
		}
	}

	accounts, err := models.GetLocalAccounts(tx, newMentionAccountList)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] get local accounts error, status id %d", status.Id)))
		return err
	}

	// 找出那些需要创建的通知（新提及的但不在旧通知列表中的账号）
	oldNotifiedAccountMap := make(map[uint64]bool)
	for _, oldNs := range oldNsList {
		oldNotifiedAccountMap[oldNs.AccountId] = true
	}

	needCreateNsList := make([]*models.Notification, 0)

	for _, account := range accounts {
		if _, ok := oldNotifiedAccountMap[account.Id]; !ok {
			// 自己 mention 自己不通知
			if account.Id == status.AccountId {
				continue
			}

			nf := &models.Notification{
				Type:          models.NotificationTypeMention,
				StatusId:      status.Id,
				AccountId:     account.Id,
				FromAccountId: status.AccountId,
			}
			needCreateNsList = append(needCreateNsList, nf)
		}
	}

	if len(needCreateNsList) > 0 {
		err = createMentionBatch(tx, needCreateNsList, created)
		if err != nil {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] create mention notification error, status id %d", status.Id)))
			return err
		}
	}

	// 现有的用户发送更新通知
	updateNfs := make([]*models.Notification, 0)

	for _, oldNs := range oldNsList {
		if _, ok := removeIdCheckMap[oldNs.AccountId]; ok {
			continue
		}

		nf := &models.Notification{
			Type:          models.NotificationTypeStatusUpdate,
			StatusId:      status.Id,
			AccountId:     oldNs.AccountId,
			FromAccountId: status.AccountId,
		}
		updateNfs = append(updateNfs, nf)
	}

	if len(updateNfs) > 0 {
		err = createMentionBatch(tx, updateNfs, created)
		if err != nil {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateMentionNotification] create update notification error, status id %d", status.Id)))
			return err
		}
	}
	return nil
}

func creatMentionNotification(status *models.Status) {
	newMentionAccountList, err := mentionAccountIDs(db.GetDB(), status.Id)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[creatMentionNotification] find new mention account list error, status id %d", status.Id)))
		return
	}
	_ = createMentionNotificationForAccounts(db.GetDB(), status, newMentionAccountList, nil)
}

func createMentionNotificationForAccounts(tx *gorm.DB, status *models.Status, newMentionAccountList []uint64, created *[]*models.Notification) error {

	if len(newMentionAccountList) == 0 {
		return nil
	}

	accounts, err := models.GetLocalAccounts(tx, newMentionAccountList)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[creatMentionNotification] get local accounts error, status id %d", status.Id)))
		return err
	}

	nsList := make([]*models.Notification, 0)

	for _, account := range accounts {
		// 自己 mention 自己不通知
		if account.Id == status.AccountId {
			continue
		}

		nf := &models.Notification{
			Type:          models.NotificationTypeMention,
			StatusId:      status.Id,
			FromAccountId: status.AccountId,
			AccountId:     account.Id,
		}
		nsList = append(nsList, nf)
	}

	// 空切片时不调用 Create
	if len(nsList) == 0 {
		return nil
	}

	err = createMentionBatch(tx, nsList, created)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[creatMentionNotification] create mention notification error, status id %d", status.Id)))
		return err
	}
	return nil
}

func removeReblogNotification(status *models.Status) {
	if status.ReblogId == nil || *status.ReblogId == 0 {
		return
	}

	reblogStatus, err := getReblogStatus(status)
	if err != nil {
		return
	}

	// 删除对应的通知
	nsList := make([]*models.Notification, 0)

	err = db.GetDB().
		Where("type In ? and status_id = ?", []int{models.NotificationTypeReblog, models.NotificationTypeStatusUpdate}, reblogStatus.Id).
		Find(&nsList).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[removeReblogNotification] find reblog notification error, status id %d", status.Id)))
		return
	}

	if len(nsList) == 0 {
		return
	}

	// 删除对应的通知
	err = db.GetDB().Delete(nsList).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[removeReblogNotification] delete reblog notification error, status id %d", status.Id)))
	}
}

// updateReblogNotification 更新转发通知
// 告诉所有转发人，这篇文章被修改了.
func updateReblogNotification(status *models.Status) {
	if status.ReblogId == nil || *status.ReblogId == 0 {
		return
	}

	reblogStatus, err := getReblogStatus(status)
	if err != nil {
		return
	}

	// 获取原来的转发通知
	accountsId := make([]uint64, 0)

	err = db.GetDB().Model(&models.Status{}).Where("reblog_id = ?", *status.ReblogId).Pluck("account_id", &accountsId).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateReblogNotification] find reblog accounts error, status id %d", reblogStatus.Id)))
		return
	}

	// 通知只发给本站用户
	accounts, err := models.GetLocalAccounts(db.GetDB(), accountsId)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateReblogNotification] get local accounts error, status id %d", reblogStatus.Id)))
		return
	}

	// 发送帖子修改通知
	nfList := make([]*models.Notification, 0)

	for _, account := range accounts {
		nf := &models.Notification{
			FromAccountId: status.AccountId,
			AccountId:     account.Id,
			Type:          models.NotificationTypeStatusUpdate,
			StatusId:      reblogStatus.Id,
		}
		nfList = append(nfList, nf)
	}

	if len(nfList) == 0 {
		return
	}

	err = db.GetDB().Create(nfList).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[updateReblogNotification] create reblog notification error, status id %d", reblogStatus.Id)))
		return
	}
}

// creatReblogNotification 创建转发通知
// 通知原作者，有人转发了他的文章.
func creatReblogNotification(status *models.Status) {
	if status.ReblogId == nil || *status.ReblogId == 0 {
		return
	}

	reblogStatus, err := getReblogStatus(status)
	if err != nil {
		return
	}

	reblogStatusAccount, err := getStatusAccount(status)
	if err != nil {
		return
	}

	if reblogStatusAccount == nil {
		return
	}

	// 如果不是本站的用户，不需要创建通知
	if reblogStatusAccount.Domain != nil {
		return
	}

	// 自己转发自己不通知
	if status.AccountId == reblogStatus.AccountId {
		return
	}

	nf := &models.Notification{
		FromAccountId: status.AccountId,
		AccountId:     reblogStatus.AccountId,
		Type:          models.NotificationTypeReblog,
		StatusId:      *status.ReblogId,
	}

	err = db.GetDB().Create(nf).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[creatReblogNotification] create reblog notification error, status id %d", status.ReblogId)))
		return
	}
}

// getReblogStatus 获取 status 的 Reblog 对象。
// 使用局部变量查询，不修改传入的共享 status 指针，避免 data race。
func getReblogStatus(status *models.Status) (*models.Status, error) {
	if status.Reblog != nil {
		return status.Reblog, nil
	}

	reblogStatus := &models.Status{}

	err := db.GetDB().Model(&models.Status{}).Where("id = ?", *status.ReblogId).First(reblogStatus).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[getReblogStatus] find reblog status error, status id %d", status.Id)))
		}

		return nil, err
	}

	return reblogStatus, nil
}

// getStatusAccount 获取 status 的 Account（转发者）。
// 使用局部变量查询，不修改传入的共享 status 指针，避免 data race。
func getStatusAccount(status *models.Status) (*models.Account, error) {
	if status.Account != nil {
		return status.Account, nil
	}

	account := &models.Account{}

	err := db.GetDB().Model(&models.Account{}).Where("id = ?", status.AccountId).First(account).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.ErrorV(errors.Wrap(err, fmt.Sprintf("[getStatusAccount] find status account error, status id %d", status.Id)))
		}

		return nil, err
	}

	return account, nil
}
