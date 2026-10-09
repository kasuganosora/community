package serializer

import (
	"errors"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"strconv"
)

type Notification struct {
	EmojiReaction *Reaction `json:"emoji_reaction,omitempty"`
	Id            string    `json:"id"`
	Type          string    `json:"type"`
	CreatedAt     *APITime  `json:"created_at"`
	Account       *Account  `json:"account,omitempty"`
	Status        *Status   `json:"status,omitempty"`
	Report        *Report   `json:"report,omitempty"`
	List          *List     `json:"list,omitempty"`
}

// NotificationVisible rechecks current authorization even for historical rows.
// A stale notification is not a grant to a direct/private post after revocation.
func NotificationVisible(n *models.Notification, currentAccount *models.Account) (bool, error) {
	if n == nil {
		return false, nil
	}
	conn := db.GetDB()
	if currentAccount != nil && n.FromAccountId != 0 {
		var sender models.Account
		if err := conn.First(&sender, n.FromAccountId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		blocked, err := models.AccountsBlocked(conn, currentAccount, &sender)
		if err != nil || blocked {
			return false, err
		}
	}
	if n.StatusId == 0 {
		return true, nil
	}
	var status models.Status
	if err := conn.Preload("ToAccounts").Preload("Account").First(&status, n.StatusId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return status.CanViewEx(conn, currentAccount)
}

func ToNotification(n *models.Notification, currentAccount *models.Account) (ret *Notification, err error) {
	visible, err := NotificationVisible(n, currentAccount)
	if err != nil || !visible {
		return nil, err
	}
	ret = &Notification{}
	ret.Id = strconv.FormatUint(n.Id, 10)
	ret.Type = n.GetTypeString()
	ret.CreatedAt = NewAPITime(n.CreatedAt)
	if n.Type == models.NotificationTypeEmojiReaction {
		ret.EmojiReaction, err = notificationReaction(n)
		if err != nil {
			return nil, err
		}
		if ret.EmojiReaction == nil {
			return nil, nil
		}
	}

	if n.FromAccountId != 0 && n.FromAccount == nil {
		fromAccount := &models.Account{}

		err = db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra").First(fromAccount, n.FromAccountId).Error
		if err != nil {
			return ret, err
		}

		ret.Account = ToAccount(fromAccount)
	} else {
		ret.Account = ToAccount(n.FromAccount)
	}

	if n.StatusId > 0 && n.Status == nil {
		n.Status = &models.Status{}

		err = db.GetDB().Model(&models.Status{}).Find(n.Status, n.StatusId).Error
		if err != nil {
			return ret, err
		}
	}

	if n.Status != nil {
		if err = n.Status.CheckAndLoadAllExtendFields(db.GetDB()); err != nil {
			return ret, err
		}

		ret.Status, err = ToStatus(n.Status, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if n.ReportId > 0 && n.Report == nil {
		n.Report = &models.Report{}

		err = db.GetDB().Model(&models.Report{}).Find(n.Report, n.ReportId).Error
		if err != nil {
			return ret, err
		}
	}

	if n.ReportId > 0 {
		ret.Report, err = ToReport(n.Report, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if n.ListId > 0 && n.List == nil {
		n.List = &models.List{}

		err = db.GetDB().Model(&models.List{}).Preload("Creator").Find(n.List, n.ListId).Error
		if err != nil {
			return ret, err
		}
	}

	if !notificationComplete(ret) {
		return nil, nil
	}

	return ret, err
}

func ToNotifications(ns []*models.Notification, currentAccount *models.Account) (ret []*Notification, err error) {
	visibleNotifications := make([]*models.Notification, 0, len(ns))
	for _, n := range ns {
		visible, err := NotificationVisible(n, currentAccount)
		if err != nil {
			return nil, err
		}
		if visible {
			visibleNotifications = append(visibleNotifications, n)
		}
	}
	ns = visibleNotifications
	if len(ns) == 0 {
		return []*Notification{}, nil
	}

	accountIds := make([]uint64, 0)
	statusIds := make([]uint64, 0)
	reportsIds := make([]uint64, 0)
	listsIds := make([]uint64, 0)

	for _, n := range ns {
		if n.FromAccountId != 0 {
			accountIds = append(accountIds, n.FromAccountId)
		}

		if n.StatusId != 0 {
			statusIds = append(statusIds, n.StatusId)
		}

		if n.ReportId != 0 {
			reportsIds = append(reportsIds, n.ReportId)
		}

		if n.ListId != 0 {
			listsIds = append(listsIds, n.ListId)
		}
	}

	var accounts []*Account

	_accounts := make(models.Accounts, 0)

	err = db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra").Where("id in ?", accountIds).Find(&_accounts).Error
	if err != nil {
		return ret, err
	}

	accounts = ToAccounts(_accounts)

	statuses := make([]*Status, 0)

	if len(statusIds) > 0 {
		_statuses := make(models.Statuses, 0)

		err = models.StatusLoadAllQuery(db.GetDB().Model(&models.Status{})).
			Where("id in ?", statusIds).Find(&_statuses).Error
		if err != nil {
			return ret, err
		}

		statuses, err = ToStatuses(_statuses, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	reports := make([]*Report, 0)

	if len(reportsIds) > 0 {
		_reports := make(models.Reports, 0)

		err = db.GetDB().Model(&models.Report{}).Where("id in ?", reportsIds).Find(&_reports).Error
		if err != nil {
			return ret, err
		}

		reports, err = ToReports(_reports, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	lists := make([]*List, 0)

	if len(listsIds) > 0 {
		_lists := make([]*models.List, 0)

		err = db.GetDB().Model(&models.List{}).Preload("Creator").Where("id in ?", listsIds).Find(&_lists).Error
		if err != nil {
			return ret, err
		}

		lists = ToLists(_lists)

		if err != nil {
			return ret, err
		}
	}

	accountsMap := utils.MapByAnyFiled(accounts, func(t *Account) uint64 {
		var id uint64

		id, _ = strconv.ParseUint(t.Id, 10, 64)

		return id
	})

	statusesMap := utils.MapByAnyFiled(statuses, func(t *Status) uint64 {
		var id uint64

		id, _ = strconv.ParseUint(t.Id, 10, 64)

		return id
	})

	reportsMap := utils.MapByAnyFiled(reports, func(t *Report) uint64 {
		var id uint64

		id, _ = strconv.ParseUint(t.Id, 10, 64)

		return id
	})

	listsMap := utils.MapByAnyFiled(lists, func(t *List) uint64 {
		var id uint64

		id, _ = strconv.ParseUint(t.Id, 10, 64)

		return id
	})

	ret = make([]*Notification, 0)

	for _, n := range ns {
		nf := &Notification{}
		nf.Id = strconv.FormatUint(n.Id, 10)
		nf.Type = n.GetTypeString()
		nf.CreatedAt = NewAPITime(n.CreatedAt)
		if n.Type == models.NotificationTypeEmojiReaction {
			nf.EmojiReaction, err = notificationReaction(n)
			if err != nil {
				return nil, err
			}
			if nf.EmojiReaction == nil {
				continue
			}
		}

		if n.FromAccountId != 0 {
			if account, ok := accountsMap[n.FromAccountId]; ok {
				nf.Account = account
			}
		}

		if n.StatusId != 0 {
			if status, ok := statusesMap[n.StatusId]; ok {
				nf.Status = status
			}
		}

		if n.ReportId != 0 {
			if report, ok := reportsMap[n.ReportId]; ok {
				nf.Report = report
			}
		}

		if n.ListId != 0 {
			if list, ok := listsMap[n.ListId]; ok {
				nf.List = list
			}
		}

		if !notificationComplete(nf) {
			continue
		}

		ret = append(ret, nf)
	}

	return ret, err
}

func notificationReaction(n *models.Notification) (*Reaction, error) {
	var row models.StatusReaction
	err := db.GetDB().Where("status_id = ? AND account_id = ?", n.StatusId, n.FromAccountId).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r := &Reaction{Name: row.Name, Count: 1}
	if row.EmojiID != 0 {
		var e models.Emoji
		if db.GetDB().First(&e, row.EmojiID).Error == nil && !e.Disabled {
			r.URL = mediaproxy.URL(mediaproxy.KindEmoji, e.Url)
			r.StaticURL = mediaproxy.URL(mediaproxy.KindEmoji, e.StaticUrl)
			r.IsSensitive = e.IsSensitive
		}
	}
	return r, nil
}

// notificationComplete reports whether a notification carries what Mastodon
// clients decode as non-optional: the sending account always, and the status
// for every status-bearing type. Incomplete rows (deleted sender/status) are
// dropped instead of poisoning the whole page.
func notificationComplete(n *Notification) bool {
	if n.Account == nil {
		return false
	}

	switch n.Type {
	case "mention", "status", "reblog", "favourite", "poll", "update", "emoji_reaction":
		return n.Status != nil
	}

	return true
}
