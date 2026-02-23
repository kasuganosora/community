package models

import "time"

// ProxyFollow status constants
const (
	ProxyFollowStatusPending  = 0
	ProxyFollowStatusAccepted = 1
	ProxyFollowStatusRejected = 2
)

// ProxyFollow reason constants
const (
	ProxyFollowReasonList = "list"
)

// ProxyFollow tracks why the system proxy account is following a remote account.
// Multiple reasons can reference the same remote account (e.g., multiple lists).
// When all references are removed, the proxy unfollow is triggered.
type ProxyFollow struct {
	ID               uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	AccountID        uint64     `json:"account_id" gorm:"not null;index:idx_proxy_account;uniqueIndex:idx_proxy_unique"`
	Reason           string     `json:"reason" gorm:"type:varchar(32);not null;uniqueIndex:idx_proxy_unique"`
	ReasonID         uint64     `json:"reason_id" gorm:"not null;uniqueIndex:idx_proxy_unique"`
	FollowActivityID string     `json:"follow_activity_id" gorm:"type:varchar(512)"`
	Status           int        `json:"status" gorm:"not null;default:0"`
	CreatedAt        *time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (ProxyFollow) TableName() string {
	return "proxy_follows"
}

// AccountKey stores RSA keypairs for local accounts (used for HTTP Signatures).
type AccountKey struct {
	ID         uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	AccountID  uint64     `json:"account_id" gorm:"not null;uniqueIndex"`
	PublicKey  string     `json:"-" gorm:"type:text;not null"`
	PrivateKey string     `json:"-" gorm:"type:text;not null"`
	CreatedAt  *time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (AccountKey) TableName() string {
	return "account_keys"
}
