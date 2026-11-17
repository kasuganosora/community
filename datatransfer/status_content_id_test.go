package datatransfer

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImporterWritesExplicitParentStatusIDAndSkipsPureReblogContent(t *testing.T) {
	conn, b, o := personalFixture(t)
	now := time.Now()

	tx := conn.Session(&gorm.Session{SkipHooks: true})
	for _, id := range []uint64{1, 2, 3, 20000} {
		require.NoError(t, tx.Omit("Content").Create(&models.Status{
			Id: id, AccountId: o.OwnerAccountID, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
		}).Error)
		require.NoError(t, tx.Create(&models.StatusContent{
			StatusId: id, ContentFormat: "plain", Content: "preexisting",
		}).Error)
	}

	b.Posts = []Post{
		{ID: "orig-text", AuthorID: "a", Text: "imported text post", Visibility: "public", CreatedAt: now},
		{ID: "orig-empty", AuthorID: "a", Text: "", Visibility: "public", CreatedAt: now.Add(time.Second)},
		{ID: "pure-boost", AuthorID: "a", ReblogID: "orig-text", Text: "", Visibility: "public", CreatedAt: now.Add(2 * time.Second)},
	}

	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	require.Equal(t, 3, report.Created["post"])

	var imported []models.Status
	require.NoError(t, conn.Where("id > ?", 20000).Order("id ASC").Find(&imported).Error)
	require.Len(t, imported, 3)

	for _, st := range imported[:2] {
		var sc models.StatusContent
		require.NoError(t, conn.First(&sc, st.Id).Error)
		require.Equal(t, st.Id, sc.StatusId)
	}

	require.NotNil(t, imported[2].ReblogId)
	var boostContent int64
	require.NoError(t, conn.Model(&models.StatusContent{}).Where("status_id = ?", imported[2].Id).Count(&boostContent).Error)
	require.Zero(t, boostContent)
}
