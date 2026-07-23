package models

import (
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"regexp"
)

type Emoji struct {
	Id              uint64 `gorm:"primary_key;autoIncrement"        json:"id"`
	Shortcode       string `gorm:"unique;not null;type:varchar(36)" json:"shortcode"`
	Url             string `gorm:"type:varchar(255);not null"       json:"url"`
	StaticUrl       string `gorm:"type:varchar(255);not null"       json:"static_url"`
	VisibleInPicker bool   `json:"visible_in_picker"`
	Domain          string `gorm:"type:varchar(255);not null;index" json:"domain"` // 域名,如果位空则为本站自己的emoji
	Category        string `json:"category"`
	CreatedAt       int64  `gorm:"autoCreateTime"`
	UpdatedAt       int64  `gorm:"autoUpdateTime"`
	DeletedAt       gorm.DeletedAt
}

func (Emoji) TableName() string {
	return "emojis"
}

type Emojis []*Emoji

func (ss Emojis) GetIds() []uint64 {
	ret := make([]uint64, 0, len(ss))
	for _, emoji := range ss {
		ret = append(ret, emoji.Id)
	}

	return ret
}

func GetEmojiFromText(db *gorm.DB, text string) (ret map[string]*Emoji, err error) {
	emojisStr := MatchEmojiTags(text)
	if len(emojisStr) == 0 {
		return
	}

	emojis := make([]*Emoji, 0, len(emojisStr))

	err = db.Where("shortcode IN ?", emojisStr).Find(&emojis).Error
	if err != nil {
		return
	}

	ret = make(map[string]*Emoji, len(emojis))
	for _, emoji := range emojis {
		ret[emoji.Shortcode] = emoji
	}

	return
}

func MatchEmojiTags(text string) []string {
	regex := `:([a-zA-Z0-9_]+):`

	return utils.FilterDuplicateAndEmptyString(regexp.MustCompile(regex).FindAllString(text, -1))
}
