package models

import (
	"time"
)

type MuteAccount struct {
	Id        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
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
		// mute_accounts 中 account_id 是执行静音的用户，target_id 是被静音的目标。
		// 这里要判断 accountId 是否是被静音的目标，因此比较 target_id。
		if m.TargetId == accountId {
			return true
		}
	}

	return false
}

type MuteStatus struct {
	Id        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	StatusId  uint64     `gorm:"not null;index"             json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
}

func (m *MuteStatus) TableName() string {
	return "mute_statuses"
}

type MuteStatuses []*MuteStatus
