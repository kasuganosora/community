package search

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchHashtagsIsCaseInsensitive(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, gdb.AutoMigrate(&models.Tag{}))
	prev := database.GetDB()
	database.SetTestDB(gdb)
	t.Cleanup(func() {
		if prev != nil {
			database.SetTestDB(prev)
		} else {
			database.ResetTestDB()
		}
		_ = sqlDB.Close()
	})

	for _, name := range []string{"OSINT", "osint_tools", "Ukraine"} {
		require.NoError(t, gdb.Create(&models.Tag{Name: name}).Error)
	}
	for _, q := range []string{"osint", "#OSInt", "OSINT"} {
		got := searchHashtags(SearchRequest{Q: q, Limit: 10})
		names := make([]string, 0, len(got))
		for _, tag := range got {
			names = append(names, tag.Name)
		}
		assert.ElementsMatch(t, []string{"OSINT", "osint_tools"}, names, q)
	}
	got := searchHashtags(SearchRequest{Q: "UKR", Limit: 10})
	require.Len(t, got, 1)
	assert.Equal(t, "Ukraine", got[0].Name)
}
