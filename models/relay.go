package models

import (
	"time"

	"gorm.io/gorm"
)

// RelayState 中继状态枚举
type RelayState int

const (
	RelayStateIdle     RelayState = 0 // 空闲
	RelayStatePending  RelayState = 1 // 等待中
	RelayStateAccepted RelayState = 2 // 已接受
	RelayStateRejected RelayState = 3 // 已拒绝
)

// Relay 联邦中继
// 中继服务器可以广播公开内容到订阅的实例
type Relay struct {
	ID                uint64         `gorm:"primaryKey" json:"id"`
	InboxURL          string         `gorm:"type:varchar(512);not null;uniqueIndex" json:"inbox_url"` // 中继服务器的 inbox URL
	FollowActivityID  string         `gorm:"type:varchar(255)" json:"follow_activity_id"`             // Follow 活动的 ID
	State             RelayState     `gorm:"type:tinyint;not null;default:0" json:"state"`            // 中继状态
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 指定表名
func (Relay) TableName() string {
	return "relays"
}

// IsEnabled 检查中继是否已启用
func (r *Relay) IsEnabled() bool {
	return r.State == RelayStateAccepted
}

// IsPending 检查中继是否等待中
func (r *Relay) IsPending() bool {
	return r.State == RelayStatePending
}

// SetState 设置中继状态
func (r *Relay) SetState(state RelayState) {
	r.State = state
}

// GetEnabledRelays 获取所有已启用的中继
func GetEnabledRelays(db *gorm.DB) ([]Relay, error) {
	var relays []Relay
	err := db.Where("state = ?", RelayStateAccepted).Find(&relays).Error
	return relays, err
}

// GetAllRelays 获取所有中继
func GetAllRelays(db *gorm.DB) ([]Relay, error) {
	var relays []Relay
	err := db.Order("created_at DESC").Find(&relays).Error
	return relays, err
}

// GetRelayByID 根据 ID 获取中继
func GetRelayByID(db *gorm.DB, id uint64) (*Relay, error) {
	var relay Relay
	err := db.First(&relay, id).Error
	if err != nil {
		return nil, err
	}
	return &relay, nil
}

// GetRelayByInboxURL 根据 InboxURL 获取中继
func GetRelayByInboxURL(db *gorm.DB, inboxURL string) (*Relay, error) {
	var relay Relay
	err := db.Where("inbox_url = ?", inboxURL).First(&relay).Error
	if err != nil {
		return nil, err
	}
	return &relay, nil
}

// GetRelayByFollowActivityID 根据 FollowActivityID 获取中继
func GetRelayByFollowActivityID(db *gorm.DB, followActivityID string) (*Relay, error) {
	var relay Relay
	err := db.Where("follow_activity_id = ?", followActivityID).First(&relay).Error
	if err != nil {
		return nil, err
	}
	return &relay, nil
}

// CreateRelay 创建中继
func CreateRelay(db *gorm.DB, inboxURL string) (*Relay, error) {
	relay := &Relay{
		InboxURL: inboxURL,
		State:    RelayStateIdle,
	}
	err := db.Create(relay).Error
	return relay, err
}

// DeleteRelay 删除中继
func DeleteRelay(db *gorm.DB, id uint64) error {
	return db.Delete(&Relay{}, id).Error
}

// GetEnabledRelayInboxes 获取所有已启用中继的 inbox URL
func GetEnabledRelayInboxes(db *gorm.DB) ([]string, error) {
	var inboxURLs []string
	err := db.Model(&Relay{}).Where("state = ?", RelayStateAccepted).Pluck("inbox_url", &inboxURLs).Error
	return inboxURLs, err
}
