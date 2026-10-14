package scripts

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
)

func TestRecoverTagName(t *testing.T) {
	cases := map[string]string{
		"<span>AirAlert</span></a></p>": "AirAlert",
		"<span>horror</span></a></p>":   "horror",
		"#golang":                       "golang",
		"rust,":                         "rust",
		"39;ll":                         "",
		"34;prompters,&#34;":            "",
		"&lt;script&gt;":                "script",
		"a b":                           "",
		"123":                           "",
	}
	for in, want := range cases {
		assert.Equal(t, want, RecoverTagName(in), in)
	}
}

func TestCleanInvalidTags(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&models.Tag{}, &models.StatusTag{}, &followedTagRow{}))
	require.NoError(t, gdb.Exec("CREATE TABLE featured_tags (id INTEGER PRIMARY KEY, account_id BIGINT NOT NULL, tag_id BIGINT NOT NULL, name VARCHAR(64) NOT NULL)").Error)

	now := time.Now()
	raw := gdb.Session(&gorm.Session{SkipHooks: true})
	for id, name := range map[uint64]string{
		1: "AirAlert",                      // valid, merge target
		2: "<span>airalert</span></a></p>", // recoverable, merged into 1 (keep its spelling)
		3: "39;ll",                         // garbage, dropped
		4: "34;prompters,&#34;",            // garbage, dropped
		5: "#travel",                       // recoverable, renamed
		6: "你好",                            // valid
	} {
		require.NoError(t, raw.Create(&models.Tag{Id: id, Name: name, NameKey: models.TagKey(name), StatusCount: 1, CreatedAt: &now, UpdatedAt: &now}).Error)
	}
	links := []models.StatusTag{
		{StatusId: 10, TagId: 1}, {StatusId: 10, TagId: 2}, // duplicate after merge
		{StatusId: 11, TagId: 2}, {StatusId: 12, TagId: 3}, {StatusId: 13, TagId: 4},
		{StatusId: 14, TagId: 5}, {StatusId: 15, TagId: 6},
	}
	require.NoError(t, gdb.Create(&links).Error)
	require.NoError(t, gdb.Create(&followedTagRow{AccountId: 1, TagId: 3, CreatedAt: &now}).Error)
	require.NoError(t, gdb.Create(&followedTagRow{AccountId: 2, TagId: 2, CreatedAt: &now}).Error)
	require.NoError(t, gdb.Exec("INSERT INTO featured_tags (id, account_id, tag_id, name) VALUES (1, 1, 5, '#travel'), (2, 1, 4, 'x'), (3, 2, 2, '<span>airalert</span>')").Error)

	require.NoError(t, CleanInvalidTagsScript{}.Up(gdb))
	require.NoError(t, CleanInvalidTagsScript{}.Up(gdb), "idempotent")

	var tags []models.Tag
	require.NoError(t, gdb.Order("id").Find(&tags).Error)
	names := map[uint64]string{}
	for _, tag := range tags {
		names[tag.Id] = tag.Name
		assert.True(t, models.ValidTagName(tag.Name), tag.Name)
	}
	assert.Equal(t, map[uint64]string{1: "AirAlert", 5: "travel", 6: "你好"}, names)
	assert.EqualValues(t, 2, tags[0].StatusCount, "merged links are recounted")

	var statusLinks []models.StatusTag
	require.NoError(t, gdb.Order("status_id").Find(&statusLinks).Error)
	got := map[uint64]uint64{}
	for _, l := range statusLinks {
		got[l.StatusId] = l.TagId
	}
	assert.Len(t, statusLinks, 4)
	assert.Equal(t, map[uint64]uint64{10: 1, 11: 1, 14: 5, 15: 6}, got)

	var followed []followedTagRow
	require.NoError(t, gdb.Find(&followed).Error)
	require.Len(t, followed, 1)
	assert.Equal(t, uint64(1), followed[0].TagId)

	var featured []struct {
		Id, TagId uint64
		Name      string
	}
	require.NoError(t, gdb.Table("featured_tags").Select("id, tag_id, name").Order("id").Scan(&featured).Error)
	require.Len(t, featured, 2)
	assert.Equal(t, uint64(5), featured[0].TagId)
	assert.Equal(t, "travel", featured[0].Name, "renames update the cached featured label and URL")
	assert.Equal(t, uint64(1), featured[1].TagId)
	assert.Equal(t, "AirAlert", featured[1].Name, "merges use the surviving tag's spelling")
}

func TestPlanInvalidTagsIsReadOnly(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&models.Tag{}, &models.StatusTag{}))
	now := time.Now()
	raw := gdb.Session(&gorm.Session{SkipHooks: true})
	for id, name := range map[uint64]string{1: "horror", 2: "<span>horror</span></a></p>", 3: "39;ll", 4: "#travel"} {
		require.NoError(t, raw.Create(&models.Tag{Id: id, Name: name, NameKey: models.TagKey(name), CreatedAt: &now, UpdatedAt: &now}).Error)
	}
	require.NoError(t, gdb.Create(&[]models.StatusTag{{StatusId: 1, TagId: 2}, {StatusId: 2, TagId: 2}}).Error)

	plan, err := PlanInvalidTags(gdb)
	require.NoError(t, err)
	require.Len(t, plan, 3)
	assert.Equal(t, InvalidTagAction{Id: 2, Name: "<span>horror</span></a></p>", Action: "merge", Target: "horror", TargetId: 1, Links: 2}, plan[0])
	assert.Equal(t, "delete", plan[1].Action)
	assert.Equal(t, InvalidTagAction{Id: 4, Name: "#travel", Action: "rename", Target: "travel"}, plan[2])

	var count int64
	require.NoError(t, gdb.Model(&models.Tag{}).Count(&count).Error)
	assert.EqualValues(t, 4, count, "plan must not write")
}

func TestPlanInvalidTagsIncludesEarlierRenames(t *testing.T) {
	for _, hasKey := range []bool{false, true} {
		t.Run(fmt.Sprintf("name_key_%t", hasKey), func(t *testing.T) {
			gdb, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := gdb.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, gdb.Exec("CREATE TABLE tags (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL, status_count BIGINT NOT NULL DEFAULT 0)").Error)
			if hasKey {
				require.NoError(t, gdb.Exec("ALTER TABLE tags ADD COLUMN name_key VARCHAR(64) NOT NULL DEFAULT ''").Error)
			}
			require.NoError(t, gdb.AutoMigrate(&models.StatusTag{}))
			for id := uint64(1); id <= 502; id++ {
				name := fmt.Sprintf("tag_%d", id)
				switch id {
				case 1:
					name = "<span>horror</span>"
				case 2:
					name = "<b>horror</b>"
				case 502:
					name = "<i>horror</i>"
				}
				row := map[string]any{"id": id, "name": name}
				if hasKey {
					row["name_key"] = models.TagKey(name)
				}
				require.NoError(t, gdb.Table("tags").Create(row).Error)
			}
			require.NoError(t, gdb.Create(&[]models.StatusTag{{StatusId: 1, TagId: 1}, {StatusId: 2, TagId: 2}, {StatusId: 3, TagId: 502}}).Error)

			plan, err := PlanInvalidTags(gdb)
			require.NoError(t, err)
			require.Len(t, plan, 3)
			assert.Equal(t, InvalidTagAction{Id: 1, Name: "<span>horror</span>", Action: "rename", Target: "horror", Links: 1}, plan[0])
			assert.Equal(t, InvalidTagAction{Id: 2, Name: "<b>horror</b>", Action: "merge", Target: "horror", TargetId: 1, Links: 1}, plan[1])
			assert.Equal(t, InvalidTagAction{Id: 502, Name: "<i>horror</i>", Action: "merge", Target: "horror", TargetId: 1, Links: 1}, plan[2], "retain virtual state across pages")
			var original tagRow
			require.NoError(t, gdb.Table("tags").Where("id = ?", 1).Scan(&original).Error)
			assert.Equal(t, "<span>horror</span>", original.Name, "planning does not perform the rename")

			require.NoError(t, CleanInvalidTagsScript{}.Up(gdb))
			var survivors []tagRow
			require.NoError(t, gdb.Table("tags").Where("id IN ?", []uint64{1, 2, 502}).Scan(&survivors).Error)
			assert.Equal(t, []tagRow{{Id: 1, Name: "horror"}}, survivors, "actual cleanup agrees with the plan")
			var links int64
			require.NoError(t, gdb.Model(&models.StatusTag{}).Where("tag_id = ?", 1).Count(&links).Error)
			assert.EqualValues(t, 3, links)
		})
	}
}
