package models

import (
	"gorm.io/gorm"
	"time"
)

type Poll struct {
	Id        uint64         `gorm:"primary_key;auto_increment" json:"id"`
	AccountId uint64         `gorm:"not null;index" json:"account_id"`
	StatusId  uint64         `gorm:"not null;index" json:"status_id"`
	ExpiresAt *time.Time     `gorm:"not null" json:"expires_at"`
	Multiple  bool           `gorm:"not null;default:false" json:"multiple"`
	Options   []*PollOption  `gorm:"many2many:poll_options;" json:"options"`
	CratedAt  *time.Time     `gorm:"not null;autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time     `gorm:"not null;autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (p *Poll) TableName() string {
	return "polls"
}

type PollOption struct {
	Id      uint64 `gorm:"primary_key;auto_increment" json:"id"`
	PollId  uint64 `gorm:"not null;index" json:"poll_id"`
	Index   uint8  `gorm:"not null" json:"index"`
	Content string `gorm:"not null" json:"content"`
	Votes   uint64 `gorm:"not null;default:0" json:"votes"`
}

func (po *PollOption) TableName() string {
	return "poll_options"
}

type PollOptionVote struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	PollId    uint64     `gorm:"not null;index" json:"poll_id"`
	AccountId uint64     `gorm:"not null;index" json:"account_id"`
	OptionId  uint64     `gorm:"not null;index" json:"option_id"`
	CratedAt  *time.Time `gorm:"not null;autoCreateTime" json:"created_at"`
}

func (pov *PollOptionVote) TableName() string {
	return "poll_option_votes"
}
