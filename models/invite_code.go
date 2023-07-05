package models

import (
	"gorm.io/gorm"
	"time"
)

type InviteCode struct {
	Id                       int64          `json:"id" gorm:"primary_key;type:bigint(20);"`
	Code                     string         `json:"code" gorm:"not null;type:varchar(64);uniqueIndex"`
	UserId                   int64          `json:"user_id" gorm:"not null;type:bigint(20);index"`
	AccountId                string         `json:"account_id" gorm:"not null;type:varchar(64);index"`
	MaxUseCount              int            `json:"max_use_count" gorm:"type:int(11)"`
	UseCount                 int            `json:"use_count" gorm:"type:int(11)"`
	ExpireAt                 *time.Time     `json:"expire_at" gorm:"type:timestamp"`
	AfterRegisterAction      string         `json:"after_register_action" gorm:"type:varchar(64)"`
	AfterRegisterActionParam map[string]any `json:"after_register_action_param" gorm:"type:json"`
	Disabled                 bool           `json:"disabled" gorm:"default:false"`
	CreatedAt                *time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt                *time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
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
