package models

import (
	"gorm.io/gorm"
	"time"
)

const (
	ListRepliesPolicyList     = iota //仅显示列表成员之间的回复
	ListRepliesPolicyFollowed        // 显示与列表成员相关的所有回复，包括列表成员回复给其他非列表成员的回复
	ListRepliesPolicyNone            // 不显示任何回复
)

type List struct {
	Id            uint64         `gorm:"primary_key;auto_increment" json:"id"`
	Title         string         `gorm:"type:varchar(255);not null" json:"title"`
	AccountId     uint64         `gorm:"not null" json:"account_id"`
	RepliesPolicy uint8          `gorm:"not null;default:0" json:"replies_policy"`
	LastViewedAt  *time.Time     `gorm:"index" json:"last_viewed_at"` // 最后被查看的时间
	CreatedAt     *time.Time     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     *time.Time     `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
	Accounts      []*Account     `gorm:"many2many:list_accounts;" json:"accounts"`
	Followers     []*Account     `gorm:"many2many:list_follow_accounts;" json:"followers"`
}

func (l *List) TableName() string {
	return "lists"
}

func (l *List) AfterCreate(tx *gorm.DB) (err error) {
	listFollowAccount := &ListFollowAccount{
		ListId:    l.Id,
		AccountId: l.AccountId,
	}
	return tx.Create(listFollowAccount).Error
}

func (l *List) SetRepliesPolicy(policy string) {
	switch policy {
	case "list":
		l.RepliesPolicy = ListRepliesPolicyList
	case "followed":
		l.RepliesPolicy = ListRepliesPolicyFollowed
	case "none":
		l.RepliesPolicy = ListRepliesPolicyNone
	default:
		l.RepliesPolicy = ListRepliesPolicyList
	}
}

func (l *List) GetRepliesPolicy() string {
	switch l.RepliesPolicy {
	case ListRepliesPolicyList:
		return "list"
	case ListRepliesPolicyFollowed:
		return "followed"
	case ListRepliesPolicyNone:
		return "none"
	default:
		return "list"
	}
}

type ListAccount struct {
	ID        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	ListId    uint64     `gorm:"not null;index" json:"list_id"`
	AccountId uint64     `gorm:"not null;index" json:"account_id"`
	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (la *ListAccount) TableName() string {
	return "list_accounts"
}

type ListFollowAccount struct {
	ID        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	ListId    uint64     `gorm:"not null;index" json:"list_id"`
	AccountId uint64     `gorm:"not null;index" json:"account_id"`
	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (lfa *ListFollowAccount) TableName() string {
	return "list_follow_accounts"
}
