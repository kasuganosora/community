package scripts

import (
	"time"

	"gorm.io/gorm"
)

// ListHashtagsScript creates list_hashtags: lists that also collect posts by
// hashtag. A unique (list_id, tag_id) pair plus an index on tag_id, used to
// find the lists a new tagged post must be pushed to.
type ListHashtagsScript struct{}

type listHashtagRow struct {
	Id        uint64     `gorm:"primaryKey;autoIncrement"`
	ListId    uint64     `gorm:"not null;uniqueIndex:uidx_list_hashtags_list_tag,priority:1"`
	TagId     uint64     `gorm:"not null;index;uniqueIndex:uidx_list_hashtags_list_tag,priority:2"`
	CreatedAt *time.Time `gorm:"autoCreateTime"`
}

func (listHashtagRow) TableName() string { return "list_hashtags" }

func (ListHashtagsScript) Up(db *gorm.DB) error { return db.AutoMigrate(&listHashtagRow{}) }

func (ListHashtagsScript) Down(db *gorm.DB) error {
	return db.Migrator().DropTable(&listHashtagRow{})
}
