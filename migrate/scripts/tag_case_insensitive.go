package scripts

import (
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// TagCaseInsensitiveScript makes hashtags case-insensitive like Mastodon.
//
// It adds tags.name_key (models.TagKey: NFC + Unicode lowercase, computed in
// Go so every database compares the same bytes), merges case-variant
// duplicates such as OSINT/osint into the oldest row (moving status_tags,
// followed_tags and featured_tags links without creating duplicates and
// recounting status_count), removes duplicate status_tags and featured_tags
// rows, and finally adds unique indexes on tags.name_key and
// status_tags(status_id, tag_id) so neither can recur. The oldest row's
// spelling is kept as the display name.
//
// Idempotent and portable (SQLite, MySQL, PostgreSQL). On MySQL name_key
// uses a binary collation: the default *_ci collations also fold accents,
// which would wrongly merge e.g. "café" and "cafe".
type TagCaseInsensitiveScript struct{}

type tagKeyColumn struct {
	NameKey string `gorm:"type:varchar(64);not null;default:''"`
}

func (tagKeyColumn) TableName() string { return "tags" }

type tagKeyRow struct {
	Id      uint64
	Name    string
	NameKey string
}

const (
	tagNameKeyIndex   = "uidx_tags_name_key"
	statusTagUniqueIx = "uidx_status_tags_status_tag"
)

func (TagCaseInsensitiveScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tags") {
		return nil
	}
	if !db.Migrator().HasColumn("tags", "name_key") {
		if err := db.Migrator().AddColumn(&tagKeyColumn{}, "NameKey"); err != nil {
			return err
		}
	}
	if dialect.IsMySQL(db) {
		if err := db.Exec("ALTER TABLE tags MODIFY name_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT ''").Error; err != nil {
			return err
		}
	}

	// Group every tag by its key; the lowest id of a group survives.
	keeper := map[string]uint64{}
	keyFix := map[uint64]string{}
	touched := map[uint64]bool{}
	var last uint64
	for {
		var rows []tagKeyRow
		if err := db.Table("tags").Select("id, name, name_key").Where("id > ?", last).Order("id").Limit(1000).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			last = row.Id
			key := models.TagKey(row.Name)
			if key == "" {
				// Cannot be addressed by any name; would collide on ''.
				if err := dropTag(db, row.Id); err != nil {
					return err
				}
				continue
			}
			into, dup := keeper[key]
			if !dup {
				keeper[key] = row.Id
				if row.NameKey != key {
					keyFix[row.Id] = key
				}
				continue
			}
			if err := mergeTagInto(db, row.Id, into); err != nil {
				return err
			}
			touched[into] = true
		}
	}
	for id, key := range keyFix {
		if err := db.Table("tags").Where("id = ?", id).Update("name_key", key).Error; err != nil {
			return err
		}
	}

	if db.Migrator().HasTable("status_tags") {
		ids, err := dedupeLinks(db, "status_tags", "status_id")
		if err != nil {
			return err
		}
		for _, id := range ids {
			touched[id] = true
		}
	}
	if db.Migrator().HasTable("featured_tags") {
		if _, err := dedupeLinks(db, "featured_tags", "account_id"); err != nil {
			return err
		}
		// Merged featured tags show the surviving tag's spelling.
		if db.Migrator().HasColumn("featured_tags", "name") {
			for id := range touched {
				if err := db.Exec("UPDATE featured_tags SET name = (SELECT name FROM tags WHERE tags.id = featured_tags.tag_id) WHERE tag_id = ?", id).Error; err != nil {
					return err
				}
			}
		}
	}
	for id := range touched {
		if err := recountTagStatuses(db, id); err != nil {
			return err
		}
	}

	if !db.Migrator().HasIndex("tags", tagNameKeyIndex) {
		if err := db.Exec("CREATE UNIQUE INDEX " + tagNameKeyIndex + " ON tags (name_key)").Error; err != nil {
			return err
		}
	}
	if db.Migrator().HasTable("status_tags") && !db.Migrator().HasIndex("status_tags", statusTagUniqueIx) {
		if err := db.Exec("CREATE UNIQUE INDEX " + statusTagUniqueIx + " ON status_tags (status_id, tag_id)").Error; err != nil {
			return err
		}
	}
	return nil
}

type linkGroup struct {
	OwnerId uint64
	TagId   uint64
	KeepId  uint64
}

// dedupeLinks deletes all but the oldest row of every (owner, tag_id) pair
// in table and returns the tag ids that had duplicates.
func dedupeLinks(db *gorm.DB, table, owner string) ([]uint64, error) {
	var groups []linkGroup
	err := db.Table(table).
		Select(owner + " AS owner_id, tag_id, MIN(id) AS keep_id").
		Group(owner + ", tag_id").
		Having("COUNT(*) > 1").
		Scan(&groups).Error
	if err != nil {
		return nil, err
	}
	seen := map[uint64]bool{}
	tags := make([]uint64, 0)
	for _, g := range groups {
		if err := db.Exec("DELETE FROM "+table+" WHERE "+owner+" = ? AND tag_id = ? AND id <> ?", g.OwnerId, g.TagId, g.KeepId).Error; err != nil {
			return nil, err
		}
		if !seen[g.TagId] {
			seen[g.TagId] = true
			tags = append(tags, g.TagId)
		}
	}
	return tags, nil
}

func (TagCaseInsensitiveScript) Down(*gorm.DB) error { return nil }
