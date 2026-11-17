package linkcard

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLinkCardRejectsZeroStatusIDAndNeverInsertsOrphanContent(t *testing.T) {
	e := newEnv(t)
	conn := e.conn
	now := time.Now()
	tx := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, tx.Omit("Content").Create(&models.Status{
		Id: 1, AccountId: e.acc.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
	}).Error)
	require.NoError(t, tx.Create(&models.StatusContent{
		StatusId: 1, ContentFormat: "plain", Content: e.srv.URL + "/article",
	}).Error)

	svc := e.svc

	outcome, err := svc.Process(context.Background(), 0)
	require.Error(t, err)
	require.Equal(t, Skipped, outcome)

	err = svc.cacheOnContent(conn, 0, &models.StatusCard{Url: e.srv.URL + "/article", Title: "Example"})
	require.Error(t, err)

	var row1 models.StatusContent
	require.NoError(t, conn.First(&row1, 1).Error)
	require.Empty(t, row1.Card, "Process(0) must never mutate status 1")

	require.NoError(t, tx.Omit("Content").Create(&models.Status{
		Id: 2, AccountId: e.acc.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
	}).Error)
	outcome, err = svc.Process(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, Skipped, outcome)

	var total int64
	require.NoError(t, conn.Model(&models.StatusContent{}).Count(&total).Error)
	require.EqualValues(t, 1, total)
}
