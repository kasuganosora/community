package models

import (
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"regexp"
	"strings"
)

type Emoji struct {
	Id              uint64   `gorm:"primary_key;autoIncrement"        json:"id"`
	Shortcode       string   `gorm:"not null;type:varchar(36);uniqueIndex:idx_emoji_shortcode_domain,priority:1" json:"shortcode"`
	Url             string   `gorm:"type:varchar(2048);not null"       json:"url"`
	StaticUrl       string   `gorm:"type:varchar(2048);not null"       json:"static_url"`
	VisibleInPicker bool     `json:"visible_in_picker"`
	Domain          string   `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_emoji_shortcode_domain,priority:2" json:"domain"` // 域名,如果位空则为本站自己的emoji
	Category        string   `gorm:"size:128" json:"category"`
	Aliases         []string `gorm:"serializer:json;type:text" json:"aliases"`
	ReactionRoleIDs []uint64 `gorm:"serializer:json;type:text" json:"reaction_role_ids"`
	License         string   `gorm:"type:text" json:"license"`
	LocalOnly       bool     `gorm:"not null;default:false" json:"local_only"`
	IsSensitive     bool     `gorm:"not null;default:false" json:"is_sensitive"`
	Disabled        bool     `gorm:"not null;default:false" json:"disabled"`
	ContentType     string   `gorm:"size:100" json:"content_type"`
	FileSize        int64    `json:"total_file_size"`
	Width           int      `json:"width"`
	Height          int      `json:"height"`
	CreatedAt       int64    `gorm:"autoCreateTime"`
	UpdatedAt       int64    `gorm:"autoUpdateTime"`
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
	emojisStr := MatchEmojiTags(EmojiText(text))
	for i := range emojisStr {
		emojisStr[i] = strings.Trim(emojisStr[i], ":")
	}
	if len(emojisStr) == 0 {
		return
	}

	emojis := make([]*Emoji, 0, len(emojisStr))

	err = db.Where("shortcode IN ? AND domain = ? AND disabled = ?", emojisStr, "", false).Find(&emojis).Error
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
