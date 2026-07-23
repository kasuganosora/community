package models

import (
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"time"
)

type Conversation struct {
	Id                   uint64                 `gorm:"primaryKey;autoIncrement"  json:"id"`
	Uri                  string                 `gorm:"type:varchar(255)"         json:"uri"`
	CreatedAt            *time.Time             `gorm:"autoCreateTime"            json:"created_at"`
	UpdatedAt            *time.Time             `gorm:"autoUpdateTime"            json:"updated_at"`
	DeletedAt            gorm.DeletedAt         `gorm:"index"                     json:"deleted_at"`
	StatusesIds          []uint64               `gorm:"type:json;serializer:json" json:"statuses_ids"`
	AccountsIds          []uint64               `gorm:"type:text;serializer:json" json:"accounts_ids"`
	Statuses             []*Status              `gorm:"foreignKey:ConversationId" json:"statuses"`
	AccountConversations []*AccountConversation `gorm:"foreignKey:ConversationId" json:"account_conversations"`
}

func (c *Conversation) TableName() string {
	return "conversations"
}

// BeforeSave 如果 AccountsIds 一个都没有，就删除.
func (c *Conversation) BeforeSave(tx *gorm.DB) (err error) {
	if len(c.AccountsIds) == 0 {
		err = tx.Delete(c).Error
		if err != nil {
			return
		}
	}

	return
}

// AfterSave 在保存完后 核对一下 AccountConversations 是否有新增的/删除的.
func (c *Conversation) AfterSave(tx *gorm.DB) (err error) {
	acs := make([]*AccountConversation, 0)

	err = tx.Model(c).Association("AccountConversations").Find(&acs)
	if err != nil {
		return err
	}

	// 对比下 AccountsIds 和 AccountConversations
	needAddAccountConversations := make([]*AccountConversation, 0)
	needDeleteAccountConversationsId := make([]uint64, 0)
	accountIdsMap := utils.GetExistsMap(c.AccountsIds)

	for _, ac := range acs {
		if _, ok := accountIdsMap[ac.AccountId]; !ok {
			needDeleteAccountConversationsId = append(needDeleteAccountConversationsId, ac.Id)
		}
	}

	acIdCheckMap := make(map[uint64]bool)
	for _, ac := range acs {
		acIdCheckMap[ac.AccountId] = true
	}

	for _, accountId := range c.AccountsIds {
		if _, ok := acIdCheckMap[accountId]; !ok {
			needAddAccountConversations = append(needAddAccountConversations, &AccountConversation{
				AccountId:      accountId,
				ConversationId: c.Id,
			})
		}
	}

	// 删除
	if len(needDeleteAccountConversationsId) > 0 {
		err = tx.Where("id IN ?", needDeleteAccountConversationsId).Delete(&AccountConversation{}).Error
		if err != nil {
			return err
		}
	}

	// 新增
	if len(needAddAccountConversations) > 0 {
		err = tx.Model(c).Association("AccountConversations").Append(needAddAccountConversations)
		if err != nil {
			return err
		}
	}

	return err
}

func (c *Conversation) AppendAccount(accountId uint64) {
	c.AccountsIds = utils.UniqueSlice(append(c.AccountsIds, accountId))
}

func (c *Conversation) AppendStatus(statusId uint64) {
	c.StatusesIds = utils.UniqueSlice(append(c.StatusesIds, statusId))
}

type AccountConversation struct {
	Id             uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountId      uint64 `gorm:"index"                    json:"account_id"`
	ConversationId uint64 `gorm:"index"                    json:"conversation_id"`
	LastStatusId   uint64 `json:"last_status"` // 如果为0就代表没看过
}

func (ac *AccountConversation) TableName() string {
	return "account_conversations"
}

func (ac *AccountConversation) IsUnread() bool {
	return ac.LastStatusId == 0
}
