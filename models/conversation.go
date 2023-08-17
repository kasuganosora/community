package models

import (
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"time"
)

type Conversation struct {
	Id                   uint64                 `gorm:"primaryKey;autoIncrement" json:"id"`
	Uri                  string                 `gorm:"type:varchar(255)" json:"uri"`
	CreatedAt            *time.Time             `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt            *time.Time             `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt            gorm.DeletedAt         `gorm:"index" json:"deleted_at"`
	StatusesIds          []uint64               `gorm:"type:json;serializer:json" json:"statuses_ids"`
	AccountsIds          []uint64               `gorm:"type:text;serializer:json" json:"accounts_ids"`
	Statuses             []*Status              `gorm:"foreignKey:ConversationId" json:"statuses"`
	AccountConversations []*AccountConversation `gorm:"foreignKey:ConversationId" json:"account_conversations"`
}

func (c *Conversation) TableName() string {
	return "conversations"
}

func (c *Conversation) AppendAccount(accountId uint64) {
	c.AccountsIds = utils.UniqueSlice(append(c.AccountsIds, accountId))
}

func (c *Conversation) AppendStatus(statusId uint64) {
	c.StatusesIds = utils.UniqueSlice(append(c.StatusesIds, statusId))
}

type AccountConversation struct {
	Id             uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountId      uint64 `gorm:"index" json:"account_id"`
	ConversationId uint64 `gorm:"index" json:"conversation_id"`
	LastStatusId   uint64 `json:"last_status"` // 如果为0就代表没看过
}

func (ac *AccountConversation) TableName() string {
	return "account_conversations"
}

func (ac *AccountConversation) IsUnread() bool {
	return ac.LastStatusId == 0
}
