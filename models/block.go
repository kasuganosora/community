package models

import "time"

const (
	BlockTypeAccount = iota
	BlockTypeDomain
)

type Block struct {
	Id              uint64     `gorm:"primary_key;auto_increment" json:"id"`
	AccountId       uint64     `gorm:"not null;index" json:"account_id"`
	Type            uint8      `gorm:"not null;index" json:"type"`
	TargetAccountId uint64     `json:"target_account_id"`
	TargetDomain    string     `gorm:"size:255" json:"target_domain"`
	CreatedAt       *time.Time `gorm:"not null;autoCreateTime" json:"created_at"`
}

func (b *Block) TableName() string {
	return "blocks"
}

type Blocks []*Block

func (bb Blocks) IsBlocked(account *Account) bool {
	for _, b := range bb {
		if b.Type == BlockTypeAccount && (b.AccountId == account.Id || b.TargetAccountId == account.Id) {
			return true
		}

		// 不能屏蔽自己的域名
		if b.Type == BlockTypeDomain && account.Domain != nil && b.TargetDomain == *account.Domain {
			return true
		}
	}
	return false
}

func (bb Blocks) IsStatusBlocked(status *Status) bool {
	if bb.IsBlocked(status.Account) {
		return true
	}
	if status.Reblog != nil && bb.IsBlocked(status.Reblog.Account) {
		return true
	}
	return false
}
