package scripts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	gmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A real remote Misskey emoji name from the maid.lat rehearsal shape: 113
// characters, far beyond the old emojis.shortcode varchar(36).
var longRemoteShortcode = "blobcat_" + strings.Repeat("ablobcatrainbow_", 6) + strings.Repeat("x", 113-8-16*6)

var widthTestModels = []any{&models.User{}, &models.UserSecretData{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{}, &models.AccountKey{}, &models.AccountFederationKey{},
	&models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.StatusEmoji{}, &models.StatusTag{}, &models.Tag{}, &models.MediaAttachment{}, &models.Emoji{},
	&models.AccountEmoji{}, &models.Conversation{}, &models.AccountConversation{}, &models.Follow{}, &models.Block{}, &models.MuteAccount{}, &models.List{}, &models.ListAccount{},
	&models.ListFollowAccount{}, &models.Poll{}, &models.PollOption{}, &models.PollOptionVote{}, &models.StatusReaction{}, &models.FavoriteStatus{}, &models.BookmarkStatus{},
	&models.PinStatus{}, &models.FederationOutbox{}, &models.RecommendationFeedback{}, &datatransfer.ImportReceipt{}, &datatransfer.StoredPreservedRecord{},
	&datatransfer.ImportedMediaAlias{}, &models.UserTwoFactor{}, &models.UserBackupCode{}}

// widthTestDB returns the target database: MySQL/MariaDB when
// COMMUNITY_MIGRATION_TEST_MYSQL_DSN is set, PostgreSQL when
// TEST_DATABASE_URL is set, SQLite otherwise.
func widthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if dsn := os.Getenv("COMMUNITY_MIGRATION_TEST_MYSQL_DSN"); dsn != "" {
		cfg, err := mysql.ParseDSN(dsn)
		require.NoError(t, err)
		cfg.DBName = ""
		admin, err := gorm.Open(gmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
		require.NoError(t, err)
		name := fmt.Sprintf("qa_widths_%d", time.Now().UnixNano())
		require.NoError(t, admin.Exec("CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4").Error)
		t.Cleanup(func() {
			admin.Exec("DROP DATABASE `" + name + "`")
			if raw, err := admin.DB(); err == nil {
				raw.Close()
			}
		})
		cfg.DBName, cfg.ParseTime = name, true
		conn, err := gorm.Open(gmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard, DisableForeignKeyConstraintWhenMigrating: true})
		require.NoError(t, err)
		require.NoError(t, conn.AutoMigrate(widthTestModels...))
		return conn
	}
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(widthTestModels...))
	if dialect.IsSQLite(conn) {
		for _, table := range []string{"accounts", "follows"} {
			var ddl string
			require.NoError(t, conn.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&ddl).Error)
			require.NoError(t, conn.Exec("DROP TABLE "+table).Error)
			require.NoError(t, conn.Exec(strings.ReplaceAll(ddl, "bigint(20)", "integer")).Error)
		}
	}
	return conn
}

// restoreOldShortcodeWidth puts emojis.shortcode back to the pre-fix
// varchar(36), keeping the composite unique index.
func restoreOldShortcodeWidth(t *testing.T, conn *gorm.DB) {
	t.Helper()
	switch dialect.Name(conn) {
	case dialect.Postgres:
		require.NoError(t, conn.Exec(`ALTER TABLE emojis ALTER COLUMN shortcode TYPE varchar(36)`).Error)
	case dialect.MySQL:
		require.NoError(t, conn.Exec("ALTER TABLE emojis MODIFY COLUMN shortcode varchar(36) NOT NULL").Error)
	default:
		var ddl string
		require.NoError(t, conn.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'emojis'").Scan(&ddl).Error)
		require.Contains(t, ddl, "`shortcode` varchar(255)")
		var indexes []string
		require.NoError(t, conn.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'emojis' AND sql IS NOT NULL").Scan(&indexes).Error)
		require.NoError(t, conn.Exec("DROP TABLE emojis").Error)
		require.NoError(t, conn.Exec(strings.Replace(ddl, "`shortcode` varchar(255)", "`shortcode` varchar(36)", 1)).Error)
		for _, ix := range indexes {
			require.NoError(t, conn.Exec(ix).Error)
		}
	}
}

func columnWidth(t *testing.T, conn *gorm.DB, table, column string) int64 {
	t.Helper()
	types, err := conn.Migrator().ColumnTypes(table)
	require.NoError(t, err)
	for _, ct := range types {
		if strings.EqualFold(ct.Name(), column) {
			n, _ := ct.Length()
			return n
		}
	}
	t.Fatalf("%s.%s missing", table, column)
	return 0
}

func emojiBundleSpool(t *testing.T) *datatransfer.Spool {
	t.Helper()
	base := "https://site.example"
	b := &datatransfer.Bundle{Schema: datatransfer.Schema, Scope: "site", Source: datatransfer.Source{Platform: "misskey", BaseURL: base}}
	b.Accounts = []datatransfer.Account{{ID: "r1", Username: "remote", Domain: "remote.example", ActorURI: "https://remote.example/users/r1", CreatedAt: time.Now().Add(-time.Hour)}}
	b.Emojis = []datatransfer.Emoji{
		{ID: "e-short", Shortcode: "blobcat", Domain: "remote.example", URL: "https://remote.example/emoji/blobcat.png"},
		{ID: "e-long", Shortcode: longRemoteShortcode, Domain: "remote.example", URL: "https://remote.example/emoji/long.png"},
	}
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	sp, err := datatransfer.OpenSpool(filepath.Join(t.TempDir(), "spool.sqlite3"))
	require.NoError(t, err)
	t.Cleanup(func() { sp.Close() })
	_, err = sp.ReadBundleJSON(context.Background(), bytes.NewReader(raw), nil)
	require.NoError(t, err)
	return sp
}

// The maid.lat failure end to end: on the old schema validation must block
// with the column, count, longest value and sample id (it used to pass, then
// the import died with "value too long"); after the migration the same staged
// data imports.
func TestLongRemoteEmojiShortcodeValidateThenImportAfterMigration(t *testing.T) {
	require.Len(t, longRemoteShortcode, 113)
	conn := widthTestDB(t)
	restoreOldShortcodeWidth(t, conn)
	require.EqualValues(t, 36, columnWidth(t, conn, "emojis", "shortcode"))
	sp := emojiBundleSpool(t)
	ctx := context.Background()
	opts := func() datatransfer.Options {
		// Strict: SQLite does not enforce widths, check its declared DDL anyway.
		return datatransfer.Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir(), Widths: &datatransfer.WidthCache{Strict: true}}
	}

	rep, err := datatransfer.ImportSpool(ctx, conn, sp, opts(), datatransfer.StreamOptions{ChunkSize: 1})
	require.NoError(t, err)
	require.False(t, rep.CanExecute)
	var found *datatransfer.Issue
	for i := range rep.Issues {
		if rep.Issues[i].Code == datatransfer.ColumnTooNarrowCode {
			require.Nil(t, found, "one issue per column")
			found = &rep.Issues[i]
		}
	}
	require.NotNil(t, found, "%+v", rep.Issues)
	require.Equal(t, "error", found.Severity)
	require.Equal(t, "emojis.shortcode", found.Kind)
	require.Equal(t, 1, found.Count)
	require.Equal(t, 113, found.MaxLength)
	require.EqualValues(t, 36, found.Limit)
	require.Equal(t, []string{"e-long"}, found.SampleIDs)
	require.Contains(t, found.Message, "emojis.shortcode")

	// community-transfer never reaches --apply after a failed preview; even
	// forced, the chunk holding the long shortcode is refused before any
	// write (earlier chunks commit by design and are skipped on re-run).
	_, err = datatransfer.ImportSpool(ctx, conn, sp, opts(), datatransfer.StreamOptions{ChunkSize: 1, Apply: true})
	require.Error(t, err)
	var n int64
	require.NoError(t, conn.Model(&models.Emoji{}).Where("shortcode = ?", longRemoteShortcode).Count(&n).Error)
	require.Zero(t, n)

	for i := 0; i < 2; i++ { // idempotent
		require.NoError(t, ImportColumnWidthsScript{}.Up(conn))
	}
	if dialect.IsPostgres(conn) {
		// pgx caches prepared plans per connection; a plan for SELECT * FROM
		// emojis from before the ALTER fails once ("cached plan must not
		// change result type"). community-transfer migrates before it reads,
		// and the runbook restarts the server after the migration; here the
		// idle connections are dropped instead.
		raw, err := conn.DB()
		require.NoError(t, err)
		raw.SetMaxIdleConns(0)
		raw.SetMaxIdleConns(2)
	}
	after := opts()
	if dialect.IsSQLite(conn) {
		// Widening is a no-op on SQLite (it stores any length); the default,
		// non-strict check is what production uses there.
		after.Widths = nil
	} else {
		require.EqualValues(t, 255, columnWidth(t, conn, "emojis", "shortcode"))
	}
	rep, err = datatransfer.ImportSpool(ctx, conn, sp, after, datatransfer.StreamOptions{ChunkSize: 1})
	require.NoError(t, err)
	require.True(t, rep.CanExecute, "%+v", rep.Issues)
	rep, err = datatransfer.ImportSpool(ctx, conn, sp, after, datatransfer.StreamOptions{ChunkSize: 1, Apply: true})
	if err != nil {
		for e := err; e != nil; e = errors.Unwrap(e) {
			t.Logf("cause: %v", e)
		}
	}
	require.NoError(t, err, "%+v", rep)
	var stored models.Emoji
	require.NoError(t, conn.Where("domain = ? AND shortcode = ?", "remote.example", longRemoteShortcode).First(&stored).Error)
	require.Equal(t, longRemoteShortcode, stored.Shortcode)
}

// The migration widens every audited column, keeps NOT NULL/defaults and the
// emoji unique index, and is a no-op on a second run.
func TestImportColumnWidthsScriptWidensAndIsIdempotent(t *testing.T) {
	conn := widthTestDB(t)
	if dialect.IsSQLite(conn) {
		require.NoError(t, ImportColumnWidthsScript{}.Up(conn))
		t.Skip("SQLite does not enforce varchar widths; run with TEST_DATABASE_URL or COMMUNITY_MIGRATION_TEST_MYSQL_DSN")
	}
	narrow := map[string]string{"emojis.shortcode": "varchar(36)", "statuses.origin_uri": "varchar(255)", "statuses.origin_url": "varchar(255)",
		"media_attachments.remote_url": "varchar(500)", "media_attachments.content_type": "varchar(100)", "accounts.username": "varchar(64)", "accounts.acct": "varchar(128)"}
	for col, typ := range narrow {
		table, column, _ := strings.Cut(col, ".")
		if dialect.IsPostgres(conn) {
			require.NoError(t, conn.Exec(fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s TYPE %s`, table, column, typ)).Error)
		} else {
			null := "NOT NULL"
			if column == "origin_uri" || column == "origin_url" || column == "remote_url" {
				null = "NULL"
			}
			require.NoError(t, conn.Exec(fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN `%s` %s %s", table, column, typ, null)).Error)
		}
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, ImportColumnWidthsScript{}.Up(conn))
	}
	for _, c := range importColumnWidths {
		require.EqualValues(t, c.width, columnWidth(t, conn, c.table, c.column), c.table+"."+c.column)
	}
	require.True(t, conn.Migrator().HasIndex(&models.Emoji{}, "idx_emoji_shortcode_domain"))
	// NOT NULL survived; the composite unique index still rejects duplicates.
	require.Error(t, conn.Exec(`INSERT INTO emojis (shortcode, domain, url, static_url) VALUES (NULL, 'x', 'u', 'u')`).Error)
	long := strings.Repeat("e", 255)
	require.NoError(t, conn.Exec(`INSERT INTO emojis (shortcode, domain, url, static_url) VALUES (?, 'x', 'u', 'u')`, long).Error)
	require.Error(t, conn.Exec(`INSERT INTO emojis (shortcode, domain, url, static_url) VALUES (?, 'x', 'u', 'u')`, long).Error)
	// accounts.acct keeps its NOT NULL default-less definition.
	require.Error(t, conn.Exec(`INSERT INTO accounts (username, display_name, acct) VALUES ('a', 'a', NULL)`).Error)
}
