package scripts

import (
	"fmt"
	"strings"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// ImportColumnWidthsScript widens varchar columns that federated / migrated
// data overflowed. The maid.lat rehearsal failed importing 257 remote emoji
// shortcodes longer than emojis.shortcode varchar(36) (longest 113; Misskey
// allows 128). The audit against Misskey column widths found the same gap
// for statuses.origin_uri/origin_url (255 vs Misskey note.uri/url 512),
// media_attachments.remote_url (500 vs drive_file.url 512, and remote URLs
// can be longer), media_attachments.content_type (100 vs drive_file.type
// 128), accounts.username (64 vs Misskey user.username 128) and accounts.acct
// (username@host: 128 vs 128+1+128).
//
// Index notes (MySQL/MariaDB, utf8mb4 = 4 bytes per char, InnoDB DYNAMIC
// row format = 3072-byte key limit):
//   - emojis (shortcode 255, domain 255) unique = 2040 bytes; the single
//     idx_emojis_shortcode = 1020 bytes;
//   - accounts.acct 320 = 1280 bytes, accounts.username 128 = 512 bytes;
//   - none of the 2048-wide columns is indexed.
//
// Tables still on COMPACT/REDUNDANT (767-byte keys) are converted to DYNAMIC
// first. PostgreSQL widens varchar in place (no rewrite, indexes untouched);
// SQLite does not enforce varchar widths, so it needs nothing.
//
// Idempotent: a column already at least as wide (or text) is left alone.
type ImportColumnWidthsScript struct{}

type widenColumn struct {
	table, column string
	width         int
}

var importColumnWidths = []widenColumn{
	{"emojis", "shortcode", 255},
	{"statuses", "origin_uri", 2048},
	{"statuses", "origin_url", 2048},
	{"media_attachments", "remote_url", 2048},
	{"media_attachments", "content_type", 128},
	{"accounts", "username", 128},
	{"accounts", "acct", 320},
}

func (ImportColumnWidthsScript) Up(db *gorm.DB) error {
	name := dialect.Name(db)
	if name != dialect.MySQL && name != dialect.Postgres {
		return nil
	}
	dynamic := map[string]bool{}
	for _, c := range importColumnWidths {
		if !db.Migrator().HasTable(c.table) || !db.Migrator().HasColumn(c.table, c.column) {
			continue
		}
		var err error
		if name == dialect.Postgres {
			err = widenPostgres(db, c)
		} else {
			if !dynamic[c.table] {
				if err = ensureDynamicRowFormat(db, c.table); err != nil {
					return err
				}
				dynamic[c.table] = true
			}
			err = widenMySQL(db, c)
		}
		if err != nil {
			return fmt.Errorf("widen %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// Down keeps the wider columns: narrowing would truncate or reject data.
func (ImportColumnWidthsScript) Down(*gorm.DB) error { return nil }

func widenPostgres(db *gorm.DB, c widenColumn) error {
	var row struct {
		DataType string
		Length   *int
	}
	if err := db.Raw(`SELECT data_type, character_maximum_length AS length FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, c.table, c.column).Scan(&row).Error; err != nil {
		return err
	}
	if !strings.Contains(row.DataType, "character") || row.Length == nil || *row.Length >= c.width {
		return nil
	}
	return db.Exec(fmt.Sprintf(`ALTER TABLE %q ALTER COLUMN %q TYPE varchar(%d)`, c.table, c.column, c.width)).Error
}

func ensureDynamicRowFormat(db *gorm.DB, table string) error {
	var format string
	if err := db.Raw(`SELECT COALESCE(ROW_FORMAT, '') FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table).Scan(&format).Error; err != nil {
		return err
	}
	switch strings.ToLower(format) {
	case "compact", "redundant":
		return db.Exec(fmt.Sprintf("ALTER TABLE `%s` ROW_FORMAT=DYNAMIC", table)).Error
	}
	return nil
}

func widenMySQL(db *gorm.DB, c widenColumn) error {
	var row struct {
		DataType   string
		Length     *int64
		Nullable   string
		Default    *string
		Charset    *string
		Collation  *string
		ColumnType string
	}
	if err := db.Raw("SELECT DATA_TYPE AS data_type, CHARACTER_MAXIMUM_LENGTH AS length, IS_NULLABLE AS nullable, COLUMN_DEFAULT AS `default`, "+
		"CHARACTER_SET_NAME AS charset, COLLATION_NAME AS collation, COLUMN_TYPE AS column_type FROM information_schema.COLUMNS "+
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?", c.table, c.column).Scan(&row).Error; err != nil {
		return err
	}
	if t := strings.ToLower(row.DataType); (t != "varchar" && t != "char") || row.Length == nil || *row.Length >= int64(c.width) {
		return nil
	}
	def := fmt.Sprintf("varchar(%d)", c.width)
	if row.Charset != nil && *row.Charset != "" {
		def += " CHARACTER SET " + *row.Charset
	}
	if row.Collation != nil && *row.Collation != "" {
		def += " COLLATE " + *row.Collation
	}
	if strings.EqualFold(row.Nullable, "NO") {
		def += " NOT NULL"
	} else {
		def += " NULL"
	}
	if row.Default != nil && !strings.EqualFold(*row.Default, "NULL") {
		v := *row.Default
		// MariaDB reports defaults as SQL literals ('x'), MySQL as raw values.
		if !strings.HasPrefix(v, "'") {
			v = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		}
		def += " DEFAULT " + v
	}
	return db.Exec(fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN `%s` %s", c.table, c.column, def)).Error
}
