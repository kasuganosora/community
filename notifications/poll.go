package notifications

import (
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"time"
)

func init() {
	event.Register("poll", "finish", &event.Handler{Exec: func(event event.Event) bool {
		poll := event.Data.(*models.Poll) //nolint:forcetypeassert
		go finishPollNotification(poll)   // 发送给所有参与投票的人投票结果

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
		err = errors.Wrap(err, fmt.Sprintf("[finishPollNotification] get polled account ids faild, poll id %d", poll.Id))
		log.ErrorV(err)

		return
	}

	accounts, err := models.GetLocalAccounts(db.GetDB(), accountsId)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[finishPollNotification] get polled accounts faild, poll id %d", poll.Id))
		log.ErrorV(err)

		return
	}

	status := &models.Status{}

	err = db.GetDB().Model(&models.Status{}).Preload("Account").Where("id = ?", poll.StatusId).First(status).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}

		err = errors.Wrap(err, fmt.Sprintf("[finishPollNotification] get status faild, poll id %d", poll.Id))
		log.ErrorV(err)

		return
	}

	nfList := make([]*models.Notification, 0)

	for _, account := range accounts {
		nf := &models.Notification{
			AccountId:     account.Id,
			FromAccountId: status.Account.Id,
			Type:          models.NotificationTypePoll,
			StatusId:      status.Id,
		}
		nfList = append(nfList, nf)
	}

	err = db.GetDB().Model(&models.Notification{}).Create(&nfList).Error
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[finishPollNotification] create notification faild, poll id %d", poll.Id))
		log.ErrorV(err)

		return
	}
}
