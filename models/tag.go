package models

import (
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"regexp"
	"time"
)

type Tag struct {
	Id               uint64     `gorm:"primary_key;auto_increment"      json:"id"`
	Name             string     `gorm:"type:varchar(64);not null;index" json:"name"`
	StatusCount      uint64     `gorm:"not null;default:0"              json:"status_count"`
	AccountCount     uint64     `gorm:"not null;default:0"              json:"account_count"`
	LastStatusUsedAt *time.Time `gorm:"index"                           json:"last_status_used_at"`
	CreatedAt        *time.Time `gorm:"not null;autoCreateTime;index"   json:"created_at"`
	UpdatedAt        *time.Time `gorm:"not null;autoUpdateTime"         json:"updated_at"`
}

func (t *Tag) TableName() string {
	return "tags"
}

type Tags []*Tag

func MatchTags(str string) []string {
	regex := regexp.MustCompile(`#(\S+)`)

	return utils.FilterDuplicateAndEmptyString(regex.FindAllString(str, -1))
}

func GetTagsFromText(dbConn *gorm.DB, text string) (ret map[string]*Tag, err error) {
	tagsStr := MatchTags(text)
	if len(tagsStr) == 0 {
		return
	}

	tags := make([]*Tag, 0, len(tagsStr))

	err = dbConn.Where("name IN ?", tagsStr).Find(&tags).Error
	if err != nil {
		return
	}

	ret = make(map[string]*Tag, len(tags))
	for _, tag := range tags {
		ret[tag.Name] = tag
	}

	return
}
