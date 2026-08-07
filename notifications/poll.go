package notifications

import (
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func init() {
	event.Register("poll", "finish", &event.Handler{Exec: func(event event.Event) bool {
		poll, ok := event.Data.(*models.Poll)
		if !ok {
			return true
		}

		safeGo("finishPollNotification", func() { finishPollNotification(poll) })

		return true
	}})
}

// finishPollNotification 发送给所有参与投票的人投票结果.
func finishPollNotification(poll *models.Poll) {
	if poll.ExpiresAt != nil && poll.ExpiresAt.After(time.Now()) {
		return
	}

	accountsId := make([]uint64, 0)

	err := db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id = ?", poll.Id).Pluck("account_id", &accountsId).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[finishPollNotification] get polled account ids faild, poll id %d", poll.Id)))
		return
	}

	accounts, err := models.GetLocalAccounts(db.GetDB(), accountsId)
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[finishPollNotification] get polled accounts faild, poll id %d", poll.Id)))
		return
	}

	status := &models.Status{}

	err = db.GetDB().Model(&models.Status{}).Preload("Account").Where("id = ?", poll.StatusId).First(status).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}

		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[finishPollNotification] get status faild, poll id %d", poll.Id)))
		return
	}

	// Preload 未找到关联时 Account 为 nil，需做 nil 检查
	if status.Account == nil {
		return
	}

	// 去重投票者 ID，避免同一用户多选时创建多条通知
	notifiedAccountIds := make(map[uint64]bool)
	nfList := make([]*models.Notification, 0)

	for _, account := range accounts {
		if notifiedAccountIds[account.Id] {
			continue
		}

		notifiedAccountIds[account.Id] = true

		nf := &models.Notification{
			AccountId:     account.Id,
			FromAccountId: status.Account.Id,
			Type:          models.NotificationTypePoll,
			StatusId:      status.Id,
		}
		nfList = append(nfList, nf)
	}

	// 空切片时不调用 Create，某些驱动会返回错误
	if len(nfList) == 0 {
		return
	}

	err = db.GetDB().Create(&nfList).Error
	if err != nil {
		log.ErrorV(errors.Wrap(err, fmt.Sprintf("[finishPollNotification] create notification faild, poll id %d", poll.Id)))
		return
	}
}
