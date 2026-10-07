package serializer

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func exerciseExactReactionNames(t *testing.T, conn *gorm.DB) {
	t.Helper()
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	statement := &gorm.Statement{DB: conn}
	require.NoError(t, statement.Parse(&models.Account{}))
	columns := []string{}
	for _, field := range statement.Schema.Fields {
		if field.DBName == "" {
			continue
		}
		columnType := "TEXT"
		switch field.DataType {
		case schema.Int, schema.Uint:
			columnType = "BIGINT"
		case schema.Bool:
			columnType = "BOOLEAN"
		case schema.Time:
			columnType = "DATETIME"
		}
		if field.DBName == "id" {
			columnType = "BIGINT PRIMARY KEY"
		}
		columns = append(columns, "`"+field.DBName+"` "+columnType)
	}
	require.NoError(t, conn.Exec("CREATE TABLE accounts ("+strings.Join(columns, ",")+")").Error)
	if conn.Dialector.Name() == "sqlite" {
		require.NoError(t, conn.Exec(`CREATE TABLE status_reactions (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL,status_id INTEGER NOT NULL,name TEXT COLLATE NOCASE NOT NULL,emoji_id INTEGER,activity_uri TEXT,created_at DATETIME,UNIQUE(account_id,status_id))`).Error)
	} else {
		require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}))
		// Keep the known problematic collation: queries must work without schema
		// surgery and under the production ONLY_FULL_GROUP_BY strict mode.
		require.NoError(t, conn.Exec("SET SESSION sql_mode = 'STRICT_TRANS_TABLES,ONLY_FULL_GROUP_BY'").Error)
	}
	names := []string{"🎉", "👍", "👍🏽", "❤", "❤️", ":wave:", ":WAVE:", "👍"}
	expectedActors := map[string][]string{}
	for i, name := range names {
		id := uint64(i + 1)
		require.NoError(t, conn.Exec("INSERT INTO accounts(id,username) VALUES (?,?)", id, fmt.Sprintf("reactor_%d", id)).Error)
		require.NoError(t, conn.Create(&models.StatusReaction{AccountID: id, StatusID: 1, Name: name}).Error)
		expectedActors[name] = append([]string{strconv.FormatUint(id, 10)}, expectedActors[name]...)
	}
	// The defect is reproduced in the same real SQL fixture before exercising
	// the serializer fix (Unicode on MySQL, ASCII case on SQLite NOCASE).
	var oldGroupCount int64
	require.NoError(t, conn.Raw("SELECT COUNT(*) FROM (SELECT name FROM status_reactions GROUP BY name) old_groups").Scan(&oldGroupCount).Error)
	require.Less(t, oldGroupCount, int64(len(expectedActors)))
	groups, err := statusReactionsBatchWithDB(conn, []uint64{1, 2}, &models.Account{Id: 2})
	require.NoError(t, err)
	require.Empty(t, groups[2])
	require.Len(t, groups[1], len(expectedActors))
	counts := map[string]int64{}
	for _, group := range groups[1] {
		counts[group.Name] = group.Count
		require.Equal(t, group.Name == "👍", group.Me)
	}
	for name, actors := range expectedActors {
		require.EqualValues(t, len(actors), counts[name], "reaction %q", name)
	}
	reactions, err := StatusReactions(1, nil, true)
	require.NoError(t, err)
	require.Len(t, reactions, len(expectedActors))
	for _, reaction := range reactions {
		ids := []string{}
		for _, actor := range reaction.Accounts {
			ids = append(ids, actor.Id)
		}
		require.Equal(t, expectedActors[reaction.Name], ids, "responders for %q", reaction.Name)
	}
	// The outbound Like-current guard must not mistake a different emoji for the
	// stored reaction even when primary IDs are otherwise correct.
	var matching int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Where("account_id = ? AND status_id = ? AND "+models.ReactionNameSQL(conn)+" = ?", 1, 1, "👍").Count(&matching).Error)
	require.Zero(t, matching)
	require.NoError(t, conn.Model(&models.StatusReaction{}).Where("account_id = ? AND status_id = ? AND "+models.ReactionNameSQL(conn)+" = ?", 1, 1, "🎉").Count(&matching).Error)
	require.EqualValues(t, 1, matching)
	// Names are NOT a unique key; the intended actor/status uniqueness remains.
	require.Error(t, conn.Create(&models.StatusReaction{AccountID: 1, StatusID: 1, Name: "👍"}).Error)
}
func TestReactionNamesExactSQLite(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	defer raw.Close()
	exerciseExactReactionNames(t, conn)
}
func TestReactionNamesExactMySQL(t *testing.T) {
	path := os.Getenv("COMMUNITY_REACTION_TEST_MYSQL_DSN_FILE")
	if path == "" {
		t.Skip("set private disposable-schema MySQL DSN file for collation regression")
	}
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	cfg, err := mysqlconfig.ParseDSN(strings.TrimSpace(string(contents)))
	require.NoError(t, err)
	cfg.DBName = ""
	cfg.ParseTime = true
	server, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sql, err := server.DB()
	require.NoError(t, err)
	defer sql.Close()
	name := fmt.Sprintf("qa_reaction_collation_%d", time.Now().UnixNano())
	require.NoError(t, server.Exec("CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error)
	defer server.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	fixture, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	fixtureSQL, err := fixture.DB()
	require.NoError(t, err)
	defer fixtureSQL.Close()
	exerciseExactReactionNames(t, fixture)
}
