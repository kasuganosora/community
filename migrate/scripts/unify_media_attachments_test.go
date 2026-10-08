package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
)

func TestUnifyMediaAttachments_MovesLinksAndDropsLegacyTables(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)

	require.NoError(t, gdb.Exec("CREATE TABLE attachments (id INTEGER PRIMARY KEY)").Error)
	require.NoError(t, gdb.AutoMigrate(&models.MediaAttachment{}, &models.StatusMediaAttachment{}))

	for _, id := range []uint64{10, 11} {
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Omit(clause.Associations).
			Create(&models.Status{Id: id, AccountId: 1}).Error)
	}
	for _, id := range []uint64{1, 2, 3} {
		require.NoError(t, gdb.Create(&models.MediaAttachment{
			Id: id, AccountId: 1, FileName: "f", FilePath: "p", ContentType: "image/png",
		}).Error)
	}
	// media 1 -> status 10, media 2 -> deleted status 99 (must stay unlinked)
	require.NoError(t, gdb.Create(&models.StatusMediaAttachment{StatusId: 10, MediaId: 1}).Error)
	require.NoError(t, gdb.Create(&models.StatusMediaAttachment{StatusId: 99, MediaId: 2}).Error)

	require.NoError(t, UnifyMediaAttachmentsScript{}.Up(gdb))

	var got []models.MediaAttachment
	require.NoError(t, gdb.Order("id").Find(&got).Error)
	require.Len(t, got, 3)
	require.NotNil(t, got[0].StatusId)
	assert.Equal(t, uint64(10), *got[0].StatusId)
	assert.Nil(t, got[1].StatusId)
	assert.Nil(t, got[2].StatusId)

	m := gdb.Migrator()
	assert.False(t, m.HasTable("status_media_attachments"))
	assert.False(t, m.HasTable("attachments"))
	assert.True(t, m.HasColumn(&models.MediaAttachment{}, "remote_url"))

	// idempotent on an already-unified schema
	require.NoError(t, UnifyMediaAttachmentsScript{}.Up(gdb))
}
