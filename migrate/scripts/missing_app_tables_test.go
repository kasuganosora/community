package scripts

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
)

func missingTablesDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	conn, err := gdb.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	require.NoError(t, gdb.AutoMigrate(&models.Tag{}))
	return gdb
}

// The deployed database (32 migrations) has tags but no featured_tags.
func TestMissingAppTablesCreatesFeaturedTags(t *testing.T) {
	gdb := missingTablesDB(t)
	require.False(t, gdb.Migrator().HasTable("featured_tags"))

	require.NoError(t, MissingAppTablesScript{}.Up(gdb))
	require.NoError(t, MissingAppTablesScript{}.Up(gdb), "idempotent")

	for _, table := range []string{"featured_tags", "endorsements", "push_subscriptions", "email_domain_blocks",
		"thumbnails", "thumbnail_jobs", "thumbnail_stats", "thumbnail_configs"} {
		assert.True(t, gdb.Migrator().HasTable(table), table)
	}
	assert.True(t, gdb.Migrator().HasIndex("featured_tags", "uidx_featured_tags_account_tag"))
	for _, col := range []string{"id", "account_id", "tag_id", "name", "created_at"} {
		assert.True(t, gdb.Migrator().HasColumn("featured_tags", col), col)
	}

	now := time.Now()
	require.NoError(t, gdb.Create(&featuredTagRow{AccountId: 1, TagId: 1, Name: "osint", CreatedAt: &now}).Error)
	assert.Error(t, gdb.Create(&featuredTagRow{AccountId: 1, TagId: 1, Name: "OSINT", CreatedAt: &now}).Error,
		"one featured tag per account and tag")
	require.NoError(t, gdb.Create(&endorsementRow{AccountId: 1, TargetId: 2}).Error)
	assert.Error(t, gdb.Create(&endorsementRow{AccountId: 1, TargetId: 2}).Error)
}

// Where featured_tags already exists without constraints, duplicates and
// links to deleted tags are removed before the unique index is added.
func TestMissingAppTablesDedupesExistingFeaturedTags(t *testing.T) {
	gdb := missingTablesDB(t)
	now := time.Now()
	for _, tag := range []models.Tag{{Id: 1, Name: "osint"}, {Id: 2, Name: "uav"}} {
		tag.CreatedAt, tag.UpdatedAt = &now, &now
		require.NoError(t, gdb.Create(&tag).Error)
	}
	require.NoError(t, gdb.Exec("CREATE TABLE featured_tags (id INTEGER PRIMARY KEY, account_id BIGINT NOT NULL, tag_id BIGINT NOT NULL, name VARCHAR(64) NOT NULL)").Error)
	require.NoError(t, gdb.Exec(`INSERT INTO featured_tags (id, account_id, tag_id, name) VALUES
		(1, 1, 1, 'osint'), (2, 1, 1, 'OSINT'), (3, 1, 2, 'uav'), (4, 2, 1, 'osint'), (5, 2, 99, 'gone')`).Error)
	require.NoError(t, gdb.Exec("CREATE TABLE endorsements (id INTEGER PRIMARY KEY, account_id BIGINT NOT NULL, target_id BIGINT NOT NULL)").Error)
	require.NoError(t, gdb.Exec("INSERT INTO endorsements (id, account_id, target_id) VALUES (1, 1, 2), (2, 1, 2), (3, 1, 3)").Error)

	require.NoError(t, MissingAppTablesScript{}.Up(gdb))
	require.NoError(t, MissingAppTablesScript{}.Up(gdb), "idempotent")

	var featured []uint64
	require.NoError(t, gdb.Table("featured_tags").Order("id").Pluck("id", &featured).Error)
	assert.Equal(t, []uint64{1, 3, 4}, featured)
	var endorsed []uint64
	require.NoError(t, gdb.Table("endorsements").Order("id").Pluck("id", &endorsed).Error)
	assert.Equal(t, []uint64{1, 3}, endorsed)
	assert.True(t, gdb.Migrator().HasColumn("featured_tags", "created_at"))
	assert.True(t, gdb.Migrator().HasIndex("featured_tags", "uidx_featured_tags_account_tag"))
	assert.True(t, gdb.Migrator().HasIndex("endorsements", "uidx_endorsements_account_target"))
}
