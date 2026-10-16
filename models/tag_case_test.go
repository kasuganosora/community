package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func tagCaseDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.AutoMigrate(&Tag{}, &StatusTag{}))
	return db
}

func TestTagKey(t *testing.T) {
	cases := map[string]string{
		"OSINT":      "osint",
		"#NewsGroup": "newsgroup",
		" AirAlert ": "airalert",
		"Ukraine":    "ukraine",
		"ÇAFÉ":       "çafé",
		"Straße":     "straße",
		"你好":         "你好",
		// NFD input folds to the same key as NFC input.
		"Cafe\u0301": "café",
	}
	for in, want := range cases {
		assert.Equal(t, want, TagKey(in), in)
	}
	assert.NotEqual(t, TagKey("café"), TagKey("cafe"), "accents are significant")
}

func TestFindTagByNameIsCaseInsensitive(t *testing.T) {
	db := tagCaseDB(t)
	require.NoError(t, db.Create(&Tag{Name: "OSINT"}).Error)

	for _, q := range []string{"osint", "OSINT", "Osint", "#oSiNt"} {
		tag, err := FindTagByName(db, q)
		require.NoError(t, err, q)
		assert.Equal(t, "OSINT", tag.Name, "display name keeps the first-seen casing")
		assert.Equal(t, "osint", tag.NameKey)
	}
	_, err := FindTagByName(db, "osint2")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// The unique key rejects a case variant inserted behind the helpers' back.
	assert.Error(t, db.Create(&Tag{Name: "Osint"}).Error)
}

func TestFindOrCreateTagsDedupesCaseVariants(t *testing.T) {
	db := tagCaseDB(t)
	first, err := FindOrCreateTag(db, "NewsGroup")
	require.NoError(t, err)

	tags, err := FindOrCreateTags(db, []string{"newsgroup", "NEWSGROUP", "UAV", "uav"})
	require.NoError(t, err)
	assert.Len(t, tags, 2)
	assert.Equal(t, first.Id, tags["newsgroup"].Id)
	assert.Equal(t, "NewsGroup", tags["newsgroup"].Name)
	assert.Equal(t, "UAV", tags["uav"].Name)

	var count int64
	require.NoError(t, db.Model(&Tag{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func TestStatusTagBindingIsCaseInsensitive(t *testing.T) {
	db := tagCaseDB(t)
	bind := func(statusID uint64, content string) {
		t.Helper()
		sc := &StatusContent{StatusId: statusID, Content: content}
		require.NoError(t, sc.bindTagsByStatusContent(db, false))
	}
	bind(1, "<p>Sirens #AirAlert #Ukraine</p>")
	bind(2, "<p>again #airalert #UKRAINE #airALERT</p>")
	bind(3, "<p>#AIRALERT</p>")

	var tags []Tag
	require.NoError(t, db.Order("id").Find(&tags).Error)
	require.Len(t, tags, 2, "case variants never create new rows")
	assert.Equal(t, "AirAlert", tags[0].Name)
	assert.EqualValues(t, 3, tags[0].StatusCount)
	assert.Equal(t, "Ukraine", tags[1].Name)
	assert.EqualValues(t, 2, tags[1].StatusCount)

	var links int64
	require.NoError(t, db.Model(&StatusTag{}).Where("status_id = ?", 2).Count(&links).Error)
	assert.EqualValues(t, 2, links, "#airalert and #airALERT link once")

	got, err := GetTagsFromText(db, "#ukraine")
	require.NoError(t, err)
	assert.Equal(t, tags[1].Id, got["ukraine"].Id)
}

// Bulk imports run with SkipHooks; the key must still be set, or the re-read
// by name_key finds nothing ("invalid tag name").
func TestFindOrCreateTagWorksWithoutHooks(t *testing.T) {
	db := tagCaseDB(t).Session(&gorm.Session{SkipHooks: true})
	tag, err := FindOrCreateTag(db, "Intro")
	require.NoError(t, err)
	assert.Equal(t, "intro", tag.NameKey)
	again, err := FindOrCreateTag(db, "intro")
	require.NoError(t, err)
	assert.Equal(t, tag.Id, again.Id)
}
