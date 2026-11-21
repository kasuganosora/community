package serializer

import (
	"errors"
	"strconv"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

type Notification struct {
	EmojiReaction *Reaction `json:"emoji_reaction,omitempty"`
	Id            string    `json:"id"`
	Type          string    `json:"type"`
	CreatedAt     *APITime  `json:"created_at"`
	GroupKey      string    `json:"group_key"`
	Account       *Account  `json:"account,omitempty"`
	Status        *Status   `json:"status,omitempty"`
	Report        *Report   `json:"report,omitempty"`
	List          *List     `json:"list,omitempty"`
}

func notificationMetadata(n *models.Notification) *Notification {
	id := strconv.FormatUint(n.Id, 10)
	return &Notification{Id: id, Type: n.GetTypeString(), CreatedAt: NewAPITime(n.CreatedAt), GroupKey: "ungrouped-" + id}
}

// A mute can hide the sender's notifications independently of their posts.
func notificationSenders(conn *gorm.DB, viewer *models.Account) *gorm.DB {
	query := conn.Model(&models.Account{})
	if viewer == nil {
		return query
	}
	return query.Where("accounts.id NOT IN (?)", models.NotificationMutedSenders(conn, viewer.Id))
}

// Resolve a request to its newest notification, including on pages that only
// contain older notifications from a withdrawn or rejected request.
func pendingRequestIDs(conn *gorm.DB, recipient uint64, senders []uint64) (map[uint64]bool, error) {
	var ids []uint64
	err := conn.Model(&models.Notification{}).
		Joins("JOIN follows ON follows.account_id = notifications.from_account_id AND follows.follow_id = notifications.account_id").
		Where("notifications.account_id = ? AND notifications.type = ? AND notifications.from_account_id IN ?", recipient, models.NotificationTypeFollowRequest, senders).
		Where("follows.status = ? AND (follows.created_at IS NULL OR notifications.created_at >= follows.created_at)", models.FollowStatusRequested).
		Group("notifications.from_account_id").Pluck("MAX(notifications.id)", &ids).Error
	if err != nil {
		return nil, err
	}
	result := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}

// NotificationVisible rechecks current authorization even for historical rows.
// A stale notification is not a grant to a direct/private post after revocation.
func NotificationVisible(n *models.Notification, currentAccount *models.Account) (bool, error) {
	if n == nil {
		return false, nil
	}
	// Edits never enter the inbox. Historical rows stay stored but are hidden,
	// including ones created when a non-edit save was labeled as an update.
	if n.Type == models.NotificationTypeStatusUpdate {
		return false, nil
	}
	if n.FromAccount != nil && n.FromAccount.IsBan {
		return false, nil
	}
	conn := db.GetDB()
	if conn == nil {
		return true, nil
	}
	if n.Type == models.NotificationTypeFollowRequest {
		pending, err := pendingRequestIDs(conn, n.AccountId, []uint64{n.FromAccountId})
		if err != nil || !pending[n.Id] {
			return false, err
		}
	}
	if n.FromAccountId != 0 {
		var sender models.Account
		if err := notificationSenders(conn, currentAccount).First(&sender, n.FromAccountId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		if sender.IsBan {
			return false, nil
		}
		if currentAccount != nil {
			blocked, err := models.AccountsBlocked(conn, currentAccount, &sender)
			if err != nil || blocked {
				return false, err
			}
		}
	}
	if n.ListId != 0 {
		var list models.List
		if err := conn.First(&list, n.ListId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		allowed, err := models.ListCanView(&list, currentAccount)
		if err != nil || !allowed {
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
	ret = notificationMetadata(n)
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
		status := &models.Status{}

		err = db.GetDB().Model(&models.Status{}).First(status, n.StatusId).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			return ret, err
		}
		n.Status = status
	}

	if n.Status != nil {
		if err = n.Status.CheckAndLoadAllExtendFields(db.GetDB()); err != nil {
			return ret, err
		}
		if err = models.LinkStoredReplyParents(db.GetDB(), []*models.Status{n.Status}, systemsettings.GetBaseURL()); err != nil {
			return ret, err
		}

		ret.Status, err = ToStatus(n.Status, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if n.ReportId > 0 && n.Report == nil {
		report := &models.Report{}

		err = db.GetDB().Model(&models.Report{}).First(report, n.ReportId).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			return ret, err
		}
		n.Report = report
	}

	if n.ReportId > 0 {
		ret.Report, err = ToReport(n.Report, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if n.ListId > 0 && n.List == nil {
		list := &models.List{}

		err = db.GetDB().Model(&models.List{}).Preload("Creator").First(list, n.ListId).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			return ret, err
		}
		n.List = list
	}
	if n.List != nil {
		ret.List = ToList(n.List)
	}

	if !notificationComplete(ret) {
		return nil, nil
	}

	return ret, err
}

func ToNotifications(ns []*models.Notification, currentAccount *models.Account) (ret []*Notification, err error) {
	candidates := make([]*models.Notification, 0, len(ns))
	for _, n := range ns {
		if n == nil || n.Type == models.NotificationTypeStatusUpdate {
			continue
		}
		candidates = append(candidates, n)
	}
	ns = candidates
	if len(ns) == 0 {
		return []*Notification{}, nil
	}

	conn := db.GetDB()
	rawAccountIDs := make([]uint64, 0, len(ns))
	rawStatusIDs := make([]uint64, 0, len(ns))
	rawListIDs := make([]uint64, 0)
	var requestSenders []uint64
	for _, n := range ns {
		if n.FromAccountId != 0 {
			rawAccountIDs = append(rawAccountIDs, n.FromAccountId)
		}
		if n.StatusId != 0 {
			rawStatusIDs = append(rawStatusIDs, n.StatusId)
		}
		if n.ListId != 0 {
			rawListIDs = append(rawListIDs, n.ListId)
		}
		if n.Type == models.NotificationTypeFollowRequest {
			requestSenders = append(requestSenders, n.FromAccountId)
		}
	}
	rawAccountIDs = utils.UniqueSlice(rawAccountIDs)
	rawStatusIDs = utils.UniqueSlice(rawStatusIDs)
	rawListIDs = utils.UniqueSlice(rawListIDs)

	var pendingRequests map[uint64]bool
	if currentAccount != nil && len(requestSenders) > 0 {
		pendingRequests, err = pendingRequestIDs(conn, currentAccount.Id, utils.UniqueSlice(requestSenders))
		if err != nil {
			return nil, err
		}
	}

	allowedAccountIDs := make(map[uint64]bool, len(rawAccountIDs))
	if len(rawAccountIDs) > 0 {
		var senders models.Accounts
		if err = notificationSenders(conn, currentAccount).Where("id IN ?", rawAccountIDs).Find(&senders).Error; err != nil {
			return nil, err
		}
		senders, err = models.FilterVisibleAccounts(conn, currentAccount, senders)
		if err != nil {
			return nil, err
		}
		for _, a := range senders {
			allowedAccountIDs[a.Id] = true
		}
	}

	allowedStatusIDs := make(map[uint64]bool, len(rawStatusIDs))
	if len(rawStatusIDs) > 0 {
		var statusHeaders models.Statuses
		if err = conn.Model(&models.Status{}).Preload("ToAccounts").Preload("Account").Preload("Reblog").Preload("Reblog.ToAccounts").Preload("Reblog.Account").Where("id IN ?", rawStatusIDs).Find(&statusHeaders).Error; err != nil {
			return nil, err
		}
		statusHeaders, err = models.FilterVisibleStatuses(conn, currentAccount, statusHeaders)
		if err != nil {
			return nil, err
		}
		for _, s := range statusHeaders {
			allowedStatusIDs[s.Id] = true
		}
	}

	allowedLists := make(map[uint64]*models.List, len(rawListIDs))
	if len(rawListIDs) > 0 {
		var fetchedLists []*models.List
		if err = conn.Model(&models.List{}).Preload("Creator").Where("id IN ?", rawListIDs).Find(&fetchedLists).Error; err != nil {
			return nil, err
		}
		for _, item := range fetchedLists {
			allowed, viewErr := models.ListCanView(item, currentAccount)
			if viewErr != nil {
				return nil, viewErr
			}
			if allowed {
				allowedLists[item.Id] = item
			}
		}
	}

	visibleNotifications := make([]*models.Notification, 0, len(ns))
	for _, n := range ns {
		if n.Type == models.NotificationTypeFollowRequest && pendingRequests != nil && !pendingRequests[n.Id] {
			continue
		}
		if n.FromAccountId != 0 && !allowedAccountIDs[n.FromAccountId] {
			continue
		}
		if n.StatusId != 0 && !allowedStatusIDs[n.StatusId] {
			continue
		}
		if n.ListId != 0 && allowedLists[n.ListId] == nil {
			continue
		}
		visibleNotifications = append(visibleNotifications, n)
	}
	ns = visibleNotifications
	if len(ns) == 0 {
		return []*Notification{}, nil
	}

	accountIds := make([]uint64, 0, len(ns))
	statusIds := make([]uint64, 0, len(ns))
	reportsIds := make([]uint64, 0)
	reactionStatusIds := make([]uint64, 0)
	reactionAccountIds := make([]uint64, 0)

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
		if n.Type == models.NotificationTypeEmojiReaction && n.StatusId != 0 && n.FromAccountId != 0 {
			reactionStatusIds = append(reactionStatusIds, n.StatusId)
			reactionAccountIds = append(reactionAccountIds, n.FromAccountId)
		}
	}
	accountIds = utils.UniqueSlice(accountIds)
	statusIds = utils.UniqueSlice(statusIds)
	reportsIds = utils.UniqueSlice(reportsIds)

	var accounts []*Account
	if len(accountIds) > 0 {
		_accounts := make(models.Accounts, 0, len(accountIds))
		err = conn.Model(&models.Account{}).Preload("Emojis").Preload("Extra").Where("id in ?", accountIds).Find(&_accounts).Error
		if err != nil {
			return ret, err
		}
		accounts = ToAccounts(_accounts)
	}

	statuses := make([]*Status, 0)
	if len(statusIds) > 0 {
		_statuses := make(models.Statuses, 0, len(statusIds))
		err = models.StatusLoadAllQuery(conn.Model(&models.Status{})).
			Where("id in ?", statusIds).Find(&_statuses).Error
		if err != nil {
			return ret, err
		}
		if len(_statuses) > 0 {
			if err = models.LinkStoredReplyParents(conn, _statuses, systemsettings.GetBaseURL()); err != nil {
				return ret, err
			}
			statuses, err = ToStatuses(_statuses, currentAccount)
			if err != nil {
				return ret, err
			}
		}
	}

	reports := make([]*Report, 0)
	if len(reportsIds) > 0 {
		_reports := make(models.Reports, 0, len(reportsIds))
		err = conn.Model(&models.Report{}).Where("id in ?", reportsIds).Find(&_reports).Error
		if err != nil {
			return ret, err
		}
		reports, err = ToReports(_reports, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	listsMap := make(map[uint64]*List, len(allowedLists))
	for id, item := range allowedLists {
		listsMap[id] = ToList(item)
	}

	type reactionKey struct {
		statusID  uint64
		accountID uint64
	}
	reactionsMap := make(map[reactionKey]*Reaction)
	if len(reactionStatusIds) > 0 {
		var rows []models.StatusReaction
		err = conn.Where("status_id IN ? AND account_id IN ?", utils.UniqueSlice(reactionStatusIds), utils.UniqueSlice(reactionAccountIds)).Find(&rows).Error
		if err != nil {
			return nil, err
		}
		emojiIDs := make([]uint64, 0)
		for _, row := range rows {
			if row.EmojiID != 0 {
				emojiIDs = append(emojiIDs, row.EmojiID)
			}
		}
		emojisByID := make(map[uint64]models.Emoji)
		if len(emojiIDs) > 0 {
			var emojis []models.Emoji
			if err := conn.Where("id IN ?", utils.UniqueSlice(emojiIDs)).Find(&emojis).Error; err != nil {
				return nil, err
			}
			for _, e := range emojis {
				if !e.Disabled {
					emojisByID[e.Id] = e
				}
			}
		}
		for _, row := range rows {
			r := &Reaction{Name: row.Name, Count: 1}
			if e, ok := emojisByID[row.EmojiID]; ok {
				r.URL = mediaproxy.URL(mediaproxy.KindEmoji, e.Url)
				r.StaticURL = mediaproxy.URL(mediaproxy.KindEmoji, e.StaticUrl)
				r.IsSensitive = e.IsSensitive
			}
			reactionsMap[reactionKey{statusID: row.StatusID, accountID: row.AccountID}] = r
		}
	}

	accountsMap := utils.MapByAnyFiled(accounts, func(t *Account) uint64 {
		id, _ := strconv.ParseUint(t.Id, 10, 64)
		return id
	})
	statusesMap := utils.MapByAnyFiled(statuses, func(t *Status) uint64 {
		id, _ := strconv.ParseUint(t.Id, 10, 64)
		return id
	})
	reportsMap := utils.MapByAnyFiled(reports, func(t *Report) uint64 {
		id, _ := strconv.ParseUint(t.Id, 10, 64)
		return id
	})

	ret = make([]*Notification, 0, len(ns))

	for _, n := range ns {
		nf := notificationMetadata(n)
		if n.Type == models.NotificationTypeEmojiReaction {
			nf.EmojiReaction = reactionsMap[reactionKey{statusID: n.StatusId, accountID: n.FromAccountId}]
			if nf.EmojiReaction == nil {
				continue
			}
		}

		if n.FromAccountId != 0 {
			nf.Account = accountsMap[n.FromAccountId]
		}
		if n.StatusId != 0 {
			nf.Status = statusesMap[n.StatusId]
		}
		if n.ReportId != 0 {
			nf.Report = reportsMap[n.ReportId]
		}
		if n.ListId != 0 {
			nf.List = listsMap[n.ListId]
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
	case "admin.report":
		return n.Report != nil
	case "mention", "status", "reblog", "favourite", "poll", "update", "emoji_reaction":
		return n.Status != nil
	}

	return true
}
