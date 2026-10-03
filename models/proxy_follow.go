package models

import "time"

// ProxyFollow status constants.
const (
	ProxyFollowStatusPending  = 0
	ProxyFollowStatusAccepted = 1
	ProxyFollowStatusRejected = 2
)

// ProxyFollow reason constants.
const (
	ProxyFollowReasonList = "list"
)

// ProxyFollow tracks why the system proxy account is following a remote account.
// Multiple reasons can reference the same remote account (e.g., multiple lists).
// When all references are removed, the proxy unfollow is triggered.
type ProxyFollow struct {
	ID               uint64     `gorm:"primaryKey;autoIncrement"                                      json:"id"`
	AccountID        uint64     `gorm:"not null;index:idx_proxy_account;uniqueIndex:idx_proxy_unique" json:"account_id"`
	Reason           string     `gorm:"type:varchar(32);not null;uniqueIndex:idx_proxy_unique"        json:"reason"`
	ReasonID         uint64     `gorm:"not null;uniqueIndex:idx_proxy_unique"                         json:"reason_id"`
	FollowActivityID string     `gorm:"type:varchar(512)"                                             json:"follow_activity_id"`
	Status           int        `gorm:"not null;default:0"                                            json:"status"`
	CreatedAt        *time.Time `gorm:"autoCreateTime"                                                json:"created_at"`
}

func (ProxyFollow) TableName() string {
	return "proxy_follows"
}

// AccountKey stores RSA keypairs for local accounts (used for HTTP Signatures).
type AccountKey struct {
	KeyID      string     `gorm:"type:varchar(2048)" json:"-"`
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountID  uint64     `gorm:"not null;uniqueIndex"     json:"account_id"`
	PublicKey  string     `gorm:"type:text;not null"       json:"-"`
	PrivateKey string     `gorm:"type:text;not null"       json:"-"`
	CreatedAt  *time.Time `gorm:"autoCreateTime"           json:"created_at"`
}

func (AccountKey) TableName() string {
	return "account_keys"
}
