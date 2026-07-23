package models

import (
	"time"
)

type MuteAccount struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	TargetId  uint64     `gorm:"not null;index"             json:"target_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
	ExpiredAt *time.Time `json:"expired_at"`
}

func (m *MuteAccount) TableName() string {
	return "mute_accounts"
}

type MuteAccounts []*MuteAccount

func (mm MuteAccounts) ExcludeExpired() MuteAccounts {
	var result MuteAccounts

	for _, m := range mm {
		if m.ExpiredAt == nil || m.ExpiredAt.After(time.Now()) {
			result = append(result, m)
		}
	}

	return result
}

func (mm MuteAccounts) IsMuted(accountId uint64) bool {
	for _, m := range mm {
		if m.AccountId == accountId {
			return true
		}
	}

	return false
}

type MuteStatus struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	StatusId  uint64     `gorm:"not null;index"             json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
}

func (m *MuteStatus) TableName() string {
	return "mute_statuses"
}

type MuteStatuses []*MuteStatus
