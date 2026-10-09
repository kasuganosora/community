package scripts

import (
	"strings"
	"unicode"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// CleanInvalidTagsScript removes tag rows whose names are not valid
// hashtags. Remote statuses used to be tagged by running `#\S+` over their
// HTML, which stored names such as "<span>AirAlert</span></a></p>",
// "39;ll" or "34;prompters,&#34;" (and featured tags stored "#name").
//
// A name whose visible text is a valid hashtag once markup, entities,
// leading '#' and surrounding punctuation are removed is recovered: the row
// is renamed, or merged into the existing tag of that name. Anything else is
// deleted together with its status_tags, followed_tags and featured_tags
// links; the statuses keep their content and get tags again when they are
// next updated from ActivityPub. Idempotent and portable (SQLite, MySQL,
// PostgreSQL). Meilisearch never accepted these names as document ids, so
// there are no search documents to remove.
type CleanInvalidTagsScript struct{}

type tagRow struct {
	Id   uint64
	Name string
}

// RecoverTagName returns the valid hashtag hidden in a dirty tag name, or "".
func RecoverTagName(name string) string {
	text := strings.TrimFunc(models.TagText(name), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	candidate := models.NormalizeTagName(text)
	if models.ValidTagName(candidate) {
		return candidate
	}
	return ""
}

func (CleanInvalidTagsScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tags") {
		return nil
	}
	var last uint64
	for {
		var rows []tagRow
		if err := db.Table("tags").Select("id, name").Where("id > ?", last).Order("id").Limit(500).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			last = row.Id
			if models.ValidTagName(row.Name) {
				continue
			}
			if err := cleanTag(db, row); err != nil {
				return err
			}
		}
	}
}

func cleanTag(db *gorm.DB, row tagRow) error {
	recovered := RecoverTagName(row.Name)
	if recovered == "" {
		return dropTag(db, row.Id)
	}
	var target tagRow
	err := db.Table("tags").Select("id, name").Where("name = ? AND id <> ?", recovered, row.Id).Order("id").Limit(1).Scan(&target).Error
	if err != nil {
		return err
	}
	if target.Id == 0 {
		return db.Table("tags").Where("id = ?", row.Id).Update("name", recovered).Error
	}
	for _, link := range []struct{ table, owner string }{
		{"status_tags", "status_id"}, {"followed_tags", "account_id"}, {"featured_tags", "account_id"},
	} {
		if !db.Migrator().HasTable(link.table) {
			continue
		}
		// Drop links the target already has, then move the rest.
		if err := db.Exec("DELETE FROM "+link.table+" WHERE tag_id = ? AND "+link.owner+" IN (SELECT "+link.owner+" FROM (SELECT "+link.owner+" FROM "+link.table+" WHERE tag_id = ?) existing)", row.Id, target.Id).Error; err != nil {
			return err
		}
		if err := db.Table(link.table).Where("tag_id = ?", row.Id).Update("tag_id", target.Id).Error; err != nil {
			return err
		}
	}
	if err := db.Exec("DELETE FROM tags WHERE id = ?", row.Id).Error; err != nil {
		return err
	}
	if db.Migrator().HasTable("status_tags") {
		var count int64
		if err := db.Table("status_tags").Where("tag_id = ?", target.Id).Count(&count).Error; err != nil {
			return err
		}
		return db.Table("tags").Where("id = ?", target.Id).Update("status_count", count).Error
	}
	return nil
}

func dropTag(db *gorm.DB, id uint64) error {
	for _, table := range []string{"status_tags", "followed_tags", "featured_tags"} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Exec("DELETE FROM "+table+" WHERE tag_id = ?", id).Error; err != nil {
			return err
		}
	}
	return db.Exec("DELETE FROM tags WHERE id = ?", id).Error
}

func (CleanInvalidTagsScript) Down(*gorm.DB) error { return nil }
