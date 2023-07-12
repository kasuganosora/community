package models

import "gorm.io/gorm"

type List struct {
	ID        uint64         `gorm:"primary_key;auto_increment" json:"id"`
	Name      string         `gorm:"type:varchar(255);not null" json:"name"`
	UserId    uint64         `gorm:"not null" json:"user_id"`
	CreatedAt int64          `gorm:"not null" json:"created_at"`
	UpdatedAt int64          `gorm:"not null" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
	Accounts  []*Account     `gorm:"many2many:list_accounts;" json:"accounts"`
	Followers []*Account     `gorm:"many2many:list_follow_accounts;" json:"followers"`
}

func (l *List) TableName() string {
	return "lists"
}

type ListAccount struct {
	ID        uint64 `gorm:"primary_key;auto_increment" json:"id"`
	ListId    uint64 `gorm:"not null;index" json:"list_id"`
	AccountId uint64 `gorm:"not null;index" json:"account_id"`
}

func (la *ListAccount) TableName() string {
	return "list_accounts"
}

type ListFollowAccount struct {
	ID        uint64 `gorm:"primary_key;auto_increment" json:"id"`
	ListId    uint64 `gorm:"not null;index" json:"list_id"`
	AccountId uint64 `gorm:"not null;index" json:"account_id"`
}

func (lfa *ListFollowAccount) TableName() string {
	return "list_follow_accounts"
}
