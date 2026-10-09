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

// legacyTagRow is the tags table as deployed with 31 migrations (no name_key).
type legacyTagRow struct {
	Id           uint64 `gorm:"primaryKey;autoIncrement"`
	Name         string `gorm:"type:varchar(64);not null;index"`
	StatusCount  uint64 `gorm:"not null;default:0"`
	AccountCount uint64 `gorm:"not null;default:0"`
	CreatedAt    *time.Time
	UpdatedAt    *time.Time
}

func (legacyTagRow) TableName() string { return "tags" }

func TestTagCaseInsensitiveMigration(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	conn, err := gdb.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	require.NoError(t, gdb.AutoMigrate(&legacyTagRow{}, &followedTagRow{}))
	// Deployed status_tags/featured_tags have no unique constraints.
	require.NoError(t, gdb.Exec("CREATE TABLE status_tags (id INTEGER PRIMARY KEY, status_id BIGINT NOT NULL, tag_id BIGINT NOT NULL)").Error)
	require.NoError(t, gdb.Exec("CREATE TABLE featured_tags (id INTEGER PRIMARY KEY, account_id BIGINT NOT NULL, tag_id BIGINT NOT NULL, name VARCHAR(64) NOT NULL)").Error)

	now := time.Now()
	for _, row := range []legacyTagRow{
		{Id: 1, Name: "osint", StatusCount: 2}, {Id: 2, Name: "OSINT", StatusCount: 2},
		{Id: 3, Name: "NewsGroup", StatusCount: 1}, {Id: 4, Name: "newsgroup", StatusCount: 1},
		{Id: 5, Name: "AirAlert", StatusCount: 1}, {Id: 6, Name: "airalert", StatusCount: 1}, {Id: 7, Name: "AIRALERT", StatusCount: 1},
		{Id: 8, Name: "café", StatusCount: 1}, {Id: 9, Name: "cafe", StatusCount: 1},
		{Id: 50, Name: "uav", StatusCount: 2}, {Id: 51, Name: "Ukraine", StatusCount: 2},
	} {
		row.CreatedAt, row.UpdatedAt = &now, &now
		require.NoError(t, gdb.Create(&row).Error)
	}
	require.NoError(t, gdb.Exec(`INSERT INTO status_tags (id, status_id, tag_id) VALUES
		(1, 10, 1), (2, 11, 1), (3, 10, 2), (4, 12, 2),
		(5, 20, 3), (6, 21, 4),
		(7, 30, 5), (8, 30, 6), (9, 30, 7), (10, 31, 7),
		(11, 40, 8), (12, 41, 9),
		(13, 144, 50), (14, 144, 50), (15, 144, 51), (16, 144, 51)`).Error)
	for _, f := range []followedTagRow{{AccountId: 1, TagId: 1}, {AccountId: 1, TagId: 2}, {AccountId: 2, TagId: 2}, {AccountId: 3, TagId: 6}} {
		f.CreatedAt = &now
		require.NoError(t, gdb.Create(&f).Error)
	}
	require.NoError(t, gdb.Exec(`INSERT INTO featured_tags (id, account_id, tag_id, name) VALUES
		(1, 1, 3, 'NewsGroup'), (2, 1, 4, 'newsgroup'), (3, 2, 4, 'newsgroup'), (4, 3, 50, 'uav'), (5, 3, 50, 'uav')`).Error)

	require.NoError(t, TagCaseInsensitiveScript{}.Up(gdb))
	require.NoError(t, TagCaseInsensitiveScript{}.Up(gdb), "idempotent")

	var tags []models.Tag
	require.NoError(t, gdb.Order("id").Find(&tags).Error)
	byID := map[uint64]models.Tag{}
	for _, tag := range tags {
		byID[tag.Id] = tag
		assert.Equal(t, models.TagKey(tag.Name), tag.NameKey, tag.Name)
	}
	require.Len(t, tags, 7, "osint, NewsGroup, AirAlert, café, cafe, uav, Ukraine")
	for id, want := range map[uint64]struct {
		name  string
		count uint64
	}{
		1: {"osint", 3}, 3: {"NewsGroup", 2}, 5: {"AirAlert", 2}, 8: {"café", 1}, 9: {"cafe", 1}, 50: {"uav", 1}, 51: {"Ukraine", 1},
	} {
		require.Contains(t, byID, id)
		assert.Equal(t, want.name, byID[id].Name, "oldest row and its spelling survive")
		assert.EqualValues(t, want.count, byID[id].StatusCount, "status_count recounted for %s", want.name)
	}

	type link struct{ Owner, TagId uint64 }
	links := func(table, owner string) []link {
		var got []link
		require.NoError(t, gdb.Table(table).Select(owner+" AS owner, tag_id").Order(owner+", tag_id").Scan(&got).Error)
		return got
	}
	assert.Equal(t, []link{{10, 1}, {11, 1}, {12, 1}, {20, 3}, {21, 3}, {30, 5}, {31, 5}, {40, 8}, {41, 9}, {144, 50}, {144, 51}},
		links("status_tags", "status_id"), "no duplicate links, including status 144")
	assert.Equal(t, []link{{1, 1}, {2, 1}, {3, 5}}, links("followed_tags", "account_id"))
	assert.Equal(t, []link{{1, 3}, {2, 3}, {3, 50}}, links("featured_tags", "account_id"))
	var featuredNames []string
	require.NoError(t, gdb.Table("featured_tags").Order("id").Pluck("name", &featuredNames).Error)
	assert.Equal(t, []string{"NewsGroup", "NewsGroup", "uav"}, featuredNames)

	var dupGroups int64
	require.NoError(t, gdb.Raw("SELECT COUNT(*) FROM (SELECT name_key FROM tags GROUP BY name_key HAVING COUNT(*) > 1) d").Scan(&dupGroups).Error)
	assert.Zero(t, dupGroups)

	// Lookups through the new key, and the unique indexes stop recurrence.
	tag, err := models.FindTagByName(gdb, "AIRALERT")
	require.NoError(t, err)
	assert.EqualValues(t, 5, tag.Id)
	assert.Error(t, gdb.Create(&models.Tag{Name: "Osint"}).Error)
	assert.Error(t, gdb.Exec("INSERT INTO status_tags (id, status_id, tag_id) VALUES (100, 144, 50)").Error)
	created, err := models.FindOrCreateTag(gdb, "NEWSGROUP")
	require.NoError(t, err)
	assert.EqualValues(t, 3, created.Id)
}
