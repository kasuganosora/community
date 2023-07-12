package models

import "time"

const (
	PostTypeStatus = iota
	PostTypeRelog
	PostTypeReply
	PostTypeCard
	PostTypePoll
)

type Post struct {
	Id                 uint64      `gorm:"primary_key;auto_increment" json:"id"`
	PostType           uint8       `gorm:"not null;default:0" json:"post_type"`
	Content            PostContent `json:"content"`
	AccountId          uint64      `gorm:"not null;index" json:"account_id"`
	ApplicationId      uint64      `json:"application_id"`
	Pinned             bool        `gorm:"not null;default:false" json:"pinned"`
	Visibility         uint8       `gorm:"not null;default:0" json:"visibility"`
	RelogId            uint64      `json:"relog_id"`
	RelogCount         uint64      `gorm:"not null;default:0" json:"relog_count"`
	InReplyToId        uint64      `json:"in_reply_to_id"`
	InReplyToAccountId uint64      `json:"in_reply_to_account_id"`
	Sensitive          bool        `gorm:"not null;default:false" json:"sensitive"`
	Language           string      `gorm:"type:varchar(16)" json:"language"`
	FavouritesCount    uint64      `gorm:"not null;default:0" json:"favourites_count"`
	EditedAt           *time.Time  `json:"edited_at"`
	CreatedAt          *time.Time  `gorm:"not null;autoCreateTime" json:"created_at"`
	UpdatedAt          *time.Time  `gorm:"not null;autoUpdateTime" json:"updated_at"`
	DeletedAt          *time.Time  `gorm:"index" json:"-"`
}

func (p *Post) TableName() string {
	return "posts"
}

type PostContent struct {
	PostId      uint64 `gorm:"not null;index" json:"post_id"`
	Content     string `gorm:"type:text;not null" json:"content"`
	SpoilerText string `gorm:"type:text;" json:"spoiler_text"`
}

func (pc *PostContent) TableName() string {
	return "post_contents"
}
