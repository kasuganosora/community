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
	// Once tags.name_key exists (TagCaseInsensitiveScript) tags are
	// identified case-insensitively by it; before that, by exact name.
	hasKey := db.Migrator().HasColumn("tags", "name_key")
	var target tagRow
	query := db.Table("tags").Select("id, name").Where("id <> ?", row.Id)
	if hasKey {
		query = query.Where("name_key = ?", models.TagKey(recovered))
	} else {
		query = query.Where("name = ?", recovered)
	}
	if err := query.Order("id").Limit(1).Scan(&target).Error; err != nil {
		return err
	}
	if target.Id == 0 {
		updates := map[string]any{"name": recovered}
		if hasKey {
			updates["name_key"] = models.TagKey(recovered)
		}
		return db.Table("tags").Where("id = ?", row.Id).Updates(updates).Error
	}
	if err := mergeTagInto(db, row.Id, target.Id); err != nil {
		return err
	}
	return recountTagStatuses(db, target.Id)
}

// mergeTagInto moves every status_tags, followed_tags and featured_tags link
// of tag from onto tag into (dropping links into already has, so no
// duplicates appear) and deletes tag from. The self-referencing DELETE goes
// through a derived table so MySQL accepts it.
func mergeTagInto(db *gorm.DB, from, into uint64) error {
	for _, link := range []struct{ table, owner string }{
		{"status_tags", "status_id"}, {"followed_tags", "account_id"}, {"featured_tags", "account_id"},
	} {
		if !db.Migrator().HasTable(link.table) {
			continue
		}
		// Drop links the target already has, then move the rest.
		if err := db.Exec("DELETE FROM "+link.table+" WHERE tag_id = ? AND "+link.owner+" IN (SELECT "+link.owner+" FROM (SELECT "+link.owner+" FROM "+link.table+" WHERE tag_id = ?) existing)", from, into).Error; err != nil {
			return err
		}
		if err := db.Table(link.table).Where("tag_id = ?", from).Update("tag_id", into).Error; err != nil {
			return err
		}
	}
	return db.Exec("DELETE FROM tags WHERE id = ?", from).Error
}

// recountTagStatuses sets tags.status_count to the number of status_tags
// links of the tag.
func recountTagStatuses(db *gorm.DB, id uint64) error {
	if !db.Migrator().HasTable("status_tags") {
		return nil
	}
	var count int64
	if err := db.Table("status_tags").Where("tag_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	return db.Table("tags").Where("id = ?", id).Update("status_count", count).Error
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
