package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"strconv"
)

type Conversation struct {
	Id         string     `json:"id"`
	Unread     bool       `json:"unread"`
	Accounts   []*Account `json:"accounts"`
	LastStatus *Status    `json:"last_status"`
}

func ToConversation(conversation *models.Conversation, account *models.Account) (conv Conversation, err error) {
	ret := Conversation{
		Id: strconv.FormatUint(conversation.Id, 10),
	}

	accountConversation := make([]*models.AccountConversation, 0)
	err = db.GetDB().Model(conversation).Where("account_id = ?", account.Id).Association("AccountConversations").Find(&accountConversation)
	if err != nil {
		return
	}

	if len(accountConversation) > 0 {
		ret.Unread = accountConversation[0].IsUnread()
	}

	accounts := make(models.Accounts, 0)

	err = db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra").Where("id in ?", conversation.AccountsIds).Find(&accounts).Error
	if err != nil {
		return
	}

	ret.Accounts = ToAccounts(accounts)

	lastStatus := &models.Status{}

	query := db.GetDB().Model(&models.Status{}).Where("id = ?", utils.Max(conversation.StatusesIds)).Order("created_at desc")
	query = models.StatusLoadAllQuery(query)
	err = query.First(lastStatus).Error
	if err != nil {
		return
	}

	ret.LastStatus, err = ToStatus(lastStatus, account)
	if err != nil {
		return
	}

	return
}

func ToConversations(account *models.Account, conversations []*models.Conversation) (ret []Conversation, err error) {
	ret = make([]Conversation, 0)

	accountsIds := make([]uint64, 0)
	statusesId := make([]uint64, 0)

	for _, conversation := range conversations {
		accountsIds = append(accountsIds, conversation.AccountsIds...)
		statusesId = append(statusesId, utils.Max(conversation.StatusesIds))
	}

	accounts := make(models.Accounts, 0)
	err = db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra").Where("id in ?", accountsIds).Find(&accounts).Error
	if err != nil {
		return
	}

	statuses := make([]*models.Status, 0)
	query := db.GetDB().Model(&models.Status{}).Where("id in ?", statusesId).Order("created_at desc")
	query = models.StatusLoadAllQuery(query)
	err = query.Find(&statuses).Error
	if err != nil {
		return
	}

	accs := ToAccounts(accounts)
	accountsMap := utils.MapByAnyFiled(accs, func(account *Account) uint64 {
		return account.intId
	})

	sts, err := ToStatuses(statuses, account)
	if err != nil {
		return
	}

	statusesMap := utils.MapByAnyFiled(sts, func(status *Status) uint64 {
		return status.intId
	})

	accountConversations := make([]*models.AccountConversation, 0)
	conversationId := make([]uint64, 0)
	for _, conversation := range conversations {
		conversationId = append(conversationId, conversation.Id)
	}

	err = db.GetDB().Model(&models.AccountConversation{}).Where("conversation_id in ? and account = ?", conversationId, account.Id).Find(&accountConversations).Error
	if err != nil {
		return
	}

	accountConversationsMap := utils.MapByAnyFiled(accountConversations, func(accountConversation *models.AccountConversation) uint64 {
		return accountConversation.ConversationId
	})

	ret = make([]Conversation, len(conversations))

	for i, conversation := range conversations {
		ret[i].Id = strconv.FormatUint(conversation.Id, 10)
		ret[i].Accounts = make([]*Account, 0)
		for _, accountId := range conversation.AccountsIds {
			if acc, ok := accountsMap[accountId]; ok {
				ret[i].Accounts = append(ret[i].Accounts, acc)
			}
		}

		ret[i].LastStatus, _ = statusesMap[utils.Max(conversation.StatusesIds)]

		if accountConversation, ok := accountConversationsMap[conversation.Id]; ok {
			ret[i].Unread = accountConversation.IsUnread()
		}
	}

	return
}
