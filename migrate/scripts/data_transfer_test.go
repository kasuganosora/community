package scripts

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	gmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func exerciseLegacyTransferMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, sql := range []string{
		`CREATE TABLE accounts (id BIGINT PRIMARY KEY, username VARCHAR(64) NOT NULL, note VARCHAR(255) NOT NULL)`,
		`CREATE TABLE users (id BIGINT PRIMARY KEY, username VARCHAR(255) NOT NULL)`,
		`CREATE TABLE statuses (id BIGINT PRIMARY KEY, content TEXT)`,
		`CREATE TABLE account_keys (account_id BIGINT PRIMARY KEY, private_key TEXT)`,
		`CREATE TABLE user_secret_data (user_id BIGINT PRIMARY KEY, password TEXT)`,
		`CREATE TABLE polls (id BIGINT PRIMARY KEY, expires_at DATETIME NOT NULL)`,
		`CREATE TABLE media_attachments (id BIGINT PRIMARY KEY, blurhash VARCHAR(50))`,
		`CREATE TABLE status_to_accounts (id BIGINT PRIMARY KEY, status_id BIGINT NOT NULL, account_id VARCHAR(255) NOT NULL)`,
		`INSERT INTO accounts(id,username,note) VALUES (7,'legacy','keep profile')`,
		`INSERT INTO users(id,username) VALUES (7,'legacy')`,
		`INSERT INTO statuses(id,content) VALUES (11,'keep post')`,
		`INSERT INTO account_keys(account_id,private_key) VALUES (7,'keep-key')`,
		`INSERT INTO polls(id,expires_at) VALUES (21,'2026-10-01 00:00:00')`,
		`INSERT INTO status_to_accounts(id,status_id,account_id) VALUES (31,11,'7')`,
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	for n := 0; n < 2; n++ {
		require.NoError(t, (DataTransferScript{}).Up(db))
		require.NoError(t, (MediaBlurhashCapacityScript{}).Up(db))
	}
	var note, content, key, recipient string
	require.NoError(t, db.Raw("SELECT note FROM accounts WHERE id = 7").Scan(&note).Error)
	require.Equal(t, "keep profile", note)
	require.NoError(t, db.Raw("SELECT content FROM statuses WHERE id = 11").Scan(&content).Error)
	require.Equal(t, "keep post", content)
	require.NoError(t, db.Raw("SELECT private_key FROM account_keys WHERE account_id = 7").Scan(&key).Error)
	require.Equal(t, "keep-key", key)
	require.NoError(t, db.Raw("SELECT account_id FROM status_to_accounts WHERE id = 31").Scan(&recipient).Error)
	require.Equal(t, "7", recipient)
	columns, e := db.Migrator().ColumnTypes("status_to_accounts")
	require.NoError(t, e)
	for _, column := range columns {
		if column.Name() == "account_id" {
			require.Contains(t, strings.ToLower(column.DatabaseTypeName()), "varchar")
		}
	}
	require.NoError(t, db.Exec("INSERT INTO polls(id,expires_at) VALUES (22,NULL)").Error)
	longBlurhash := strings.Repeat("A", 166)
	require.NoError(t, db.Exec("INSERT INTO media_attachments(id,blurhash) VALUES (?,?)", 41, longBlurhash).Error)
	var savedBlurhash string
	require.NoError(t, db.Raw("SELECT blurhash FROM media_attachments WHERE id = 41").Scan(&savedBlurhash).Error)
	require.Equal(t, longBlurhash, savedBlurhash)
	for _, field := range []string{"LocalActorURI", "ImportedNoteHTML"} {
		require.True(t, db.Migrator().HasColumn(&models.Account{}, field))
	}
	require.True(t, db.Migrator().HasColumn(&models.Status{}, "LocalObjectURI"))
	require.True(t, db.Migrator().HasTable(&datatransfer.Job{}))
	require.True(t, db.Migrator().HasColumn(&datatransfer.StoredPreservedRecord{}, "Scope"))
	require.NoError(t, (DataTransferScript{}).Down(db))
	var count int64
	require.NoError(t, db.Table("status_to_accounts").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestDataTransferMigrationPreservesLegacyColumns(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard, DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, e)
	sql, e := db.DB()
	require.NoError(t, e)
	sql.SetMaxOpenConns(1)
	defer sql.Close()
	exerciseLegacyTransferMigration(t, db)
}

// A disposable schema is essential: this test must never migrate a supplied
// application's live database. The DSN grants CREATE/DROP only to a local lab.
func TestDataTransferMigrationMySQLLegacyJoin(t *testing.T) {
	dsn := os.Getenv("COMMUNITY_MIGRATION_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set lab MySQL DSN to exercise actual legacy FK compatibility")
	}
	cfg, e := mysql.ParseDSN(dsn)
	require.NoError(t, e)
	cfg.DBName = ""
	db, e := gorm.Open(gmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, e)
	raw, e := db.DB()
	require.NoError(t, e)
	defer raw.Close()
	name := fmt.Sprintf("qa_transfer_migration_%d", time.Now().UnixNano())
	require.NoError(t, db.Exec("CREATE DATABASE `"+name+"`").Error)
	defer db.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	fixture, e := gorm.Open(gmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, e)
	fixtureSQL, e := fixture.DB()
	require.NoError(t, e)
	defer fixtureSQL.Close()
	exerciseLegacyTransferMigration(t, fixture)
}
