package serializer

import (
	"strconv"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

type Conversation struct {
	Id         string     `json:"id"`
	Unread     bool       `json:"unread"`
	Accounts   []*Account `json:"accounts"`
	LastStatus *Status    `json:"last_status"`
}

func ToConversation(conversation *models.Conversation, account *models.Account) (Conversation, error) {
	rows, err := ToConversations([]*models.Conversation{conversation}, account)
	if err != nil {
		return Conversation{}, err
	}
	if len(rows) == 0 {
		return Conversation{}, gorm.ErrRecordNotFound
	}
	return rows[0], nil
}

type visibleConversation struct {
	latest       uint64
	participants []uint64
}

// A reply thread can contain several private audiences. Its union of stored
// account IDs must never reveal participants of messages the viewer cannot read.
func visibleConversationData(conn *gorm.DB, ids []uint64, viewer uint64) (map[uint64]visibleConversation, error) {
	recipients := conn.Table("status_to_accounts").Select("status_id").Where("account_id = ?", viewer)
	var messages []*models.Status
	err := conn.Model(&models.Status{}).Select("id", "account_id", "conversation_id", "visibility").
		Where("conversation_id IN ? AND visibility = ? AND deleted_at IS NULL AND scheduled_at IS NULL", ids, models.StatusVisibilityDirect).
		Where("account_id = ? OR id IN (?)", viewer, recipients).
		Preload("ToAccounts").Order("id ASC").Find(&messages).Error
	if err != nil {
		return nil, err
	}
	result := make(map[uint64]visibleConversation)
	for _, message := range messages {
		allowed, err := message.CanViewEx(conn, &models.Account{Id: viewer})
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if message.ConversationId == nil {
			continue
		}
		id := *message.ConversationId
		row := result[id]
		row.latest = message.Id
		row.participants = append(row.participants, message.AccountId)
		for _, recipient := range message.ToAccounts {
			row.participants = append(row.participants, recipient.Id)
		}
		row.participants = utils.UniqueSlice(row.participants)
		result[id] = row
	}
	return result, nil
}

func ToConversations(conversations []*models.Conversation, account *models.Account) ([]Conversation, error) {
	ret := make([]Conversation, 0, len(conversations))
	if len(conversations) == 0 {
		return ret, nil
	}
	if account == nil {
		return ret, nil
	}
	conn := db.GetDB()
	ids := make([]uint64, 0, len(conversations))
	for _, conversation := range conversations {
		ids = append(ids, conversation.Id)
	}
	visible, err := visibleConversationData(conn, ids, account.Id)
	if err != nil {
		return nil, err
	}
	var accountIDs, statusIDs []uint64
	for _, row := range visible {
		accountIDs = append(accountIDs, row.participants...)
		statusIDs = append(statusIDs, row.latest)
	}
	if len(statusIDs) == 0 {
		return ret, nil
	}
	var accounts models.Accounts
	if err := conn.Preload("Emojis").Preload("Extra").Where("id IN ?", utils.UniqueSlice(accountIDs)).Find(&accounts).Error; err != nil {
		return nil, err
	}
	var statuses []*models.Status
	if err := models.StatusLoadAllQuery(conn).Where("id IN ?", statusIDs).Find(&statuses).Error; err != nil {
		return nil, err
	}
	serialized, err := ToStatuses(statuses, account)
	if err != nil {
		return nil, err
	}
	accountsByID := utils.MapByAnyFiled(ToAccounts(accounts), func(a *Account) uint64 { return a.intId })
	statusesByID := utils.MapByAnyFiled(serialized, func(s *Status) uint64 { return s.intId })
	var memberships []*models.AccountConversation
	if err := conn.Where("conversation_id IN ? AND account_id = ?", ids, account.Id).Find(&memberships).Error; err != nil {
		return nil, err
	}
	readThrough := make(map[uint64]uint64)
	for _, membership := range memberships {
		readThrough[membership.ConversationId] = membership.LastStatusId
	}
	for _, conversation := range conversations {
		row, ok := visible[conversation.Id]
		if !ok {
			continue
		}
		last := statusesByID[row.latest]
		if last == nil {
			continue
		}
		item := Conversation{Id: strconv.FormatUint(conversation.Id, 10), Accounts: make([]*Account, 0), LastStatus: last, Unread: row.latest > readThrough[conversation.Id]}
		for _, id := range row.participants {
			if a := accountsByID[id]; a != nil {
				item.Accounts = append(item.Accounts, a)
			}
		}
		ret = append(ret, item)
	}
	return ret, nil
}
