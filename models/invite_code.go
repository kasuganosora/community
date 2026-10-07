package models

import (
	"gorm.io/gorm"
	"time"
)

type InviteCode struct { //nolint:recvcheck
	Id                       int64          `gorm:"primary_key;type:bigint(20);"          json:"id"`
	Code                     string         `gorm:"not null;type:varchar(64);uniqueIndex" json:"code"`
	UserId                   uint64         `gorm:"not null;type:bigint(20);index"        json:"user_id"`
	AccountId                string         `gorm:"not null;type:varchar(64);index"       json:"account_id"`
	MaxUseCount              int            `gorm:"type:int(11)"                          json:"max_use_count"`
	UseCount                 int            `gorm:"type:int(11)"                          json:"use_count"`
	ExpireAt                 *time.Time     `gorm:"type:timestamp"                        json:"expire_at"`
	AfterRegisterAction      string         `gorm:"type:varchar(64)"                      json:"after_register_action"`
	AfterRegisterActionParam map[string]any `gorm:"type:json;serializer:json"             json:"after_register_action_param"`
	Disabled                 bool           `gorm:"default:false"                         json:"disabled"`
	CreatedAt                *time.Time     `gorm:"autoCreateTime"                        json:"created_at"`
	UpdatedAt                *time.Time     `gorm:"autoUpdateTime"                        json:"updated_at"`
	gorm.DeletedAt
}

func (InviteCode) TableName() string {
	return "invite_codes"
}

func (inv *InviteCode) IsAvailable() bool {
	if inv.Disabled {
		return false
	}

	if inv.MaxUseCount > 0 && inv.UseCount >= inv.MaxUseCount {
		return false
	}

	if inv.ExpireAt != nil && inv.ExpireAt.Before(time.Now()) {
		return false
	}

	return true
}
