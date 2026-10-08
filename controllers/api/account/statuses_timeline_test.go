package account

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountTimelineFiltersBeforeLimitAndUsesCursorOrder(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(
		&models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{},
		&models.Application{}, &models.MediaAttachment{}, &models.Emoji{}, &models.StatusEmoji{},
		&models.StatusToAccount{}, &models.Poll{}, &models.PollOption{}, &models.PollOptionVote{},
		&models.Tag{}, &models.StatusTag{}, &models.Follow{}, &models.Block{}, &models.SystemSetting{},
		&models.PinStatus{}, &models.FavoriteStatus{}, &models.BookmarkStatus{}, &models.MuteStatus{},
		&models.MuteAccount{}, &models.Filter{}, &models.FilterKeyword{}, &models.StatusReaction{},
	))
	writer := conn.Session(&gorm.Session{SkipHooks: true})
	owner, viewer, other := &models.Account{Id: 1, Username: "owner"}, &models.Account{Id: 2, Username: "reader"}, &models.Account{Id: 3, Username: "other"}
	for _, a := range []*models.Account{owner, viewer, other} {
		require.NoError(t, writer.Create(a).Error)
	}
	visibilities := []uint8{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted, models.StatusVisibilityPrivate, models.StatusVisibilityDirect, models.StatusVisibilityPrivate, models.StatusVisibilityDirect, models.StatusVisibilityPublic, models.StatusVisibilityPublic, models.StatusVisibilityPublic}
	for i, visibility := range visibilities {
		id := uint64(i + 1)
		// Deliberately reverse timestamps: ID cursors must not skip imported posts.
		created := time.Now().Add(-time.Duration(id) * time.Hour)
		s := &models.Status{Id: id, AccountId: owner.Id, Visibility: visibility, CreatedAt: &created, UpdatedAt: &created}
		if id == 8 {
			s.ScheduledAt = &created
		}
		if id == 9 {
			s.DeletedAt = &created
		}
		require.NoError(t, writer.Create(s).Error)
		require.NoError(t, writer.Create(&models.StatusContent{StatusId: id, Content: fmt.Sprint("post ", id)}).Error)
	}
	for _, entry := range []models.StatusToAccount{{StatusId: 4, AccountId: other.Id}, {StatusId: 5, AccountId: viewer.Id}, {StatusId: 6, AccountId: viewer.Id}} {
		require.NoError(t, writer.Create(&entry).Error)
	}
	request := func(account *models.Account, query string) []string {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/accounts/1/statuses?"+query, nil)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		if account != nil {
			c.Set("authenticated", true)
			c.Set("account", account)
		}
		AccountStatuses(c)
		require.Empty(t, c.Errors)
		require.Equal(t, 200, w.Code)
		var rows []struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows), w.Body.String())
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids
	}
	require.Equal(t, []string{"7", "2"}, request(nil, "limit=2"))
	require.Equal(t, []string{"1"}, request(nil, "limit=2&max_id=2"))
	require.Equal(t, []string{"7", "2"}, request(nil, "limit=2&since_id=1"))
	require.Equal(t, []string{"7", "6", "5", "2", "1"}, request(viewer, "limit=80"))
	follow := &models.Follow{Id: 1, AccountId: viewer.Id, FollowId: owner.Id, Status: models.FollowStatusRequested}
	require.NoError(t, writer.Create(follow).Error)
	require.Equal(t, []string{"7", "6", "5", "2", "1"}, request(viewer, "limit=80"))
	require.NoError(t, writer.Model(follow).Update("status", models.FollowStatusFollowed).Error)
	require.Equal(t, []string{"7", "6", "5", "3", "2", "1"}, request(viewer, "limit=80"))
	require.Equal(t, []string{"7", "6", "5", "4", "3", "2", "1"}, request(owner, "limit=80"))
	// Revocation must take effect on the next request, without a timeline TTL.
	require.NoError(t, writer.Delete(follow).Error)
	require.Equal(t, []string{"7", "6", "5", "2", "1"}, request(viewer, "limit=80"))
	// A newest public reblog with an inaccessible original must not turn the
	// account's first page into an empty/short page and hide older originals.
	now := time.Now()
	originalID := uint64(10)
	for _, status := range []*models.Status{
		{Id: originalID, AccountId: other.Id, Visibility: models.StatusVisibilityPrivate, CreatedAt: &now, UpdatedAt: &now},
		{Id: 11, AccountId: owner.Id, ReblogId: &originalID, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now},
	} {
		require.NoError(t, writer.Create(status).Error)
		require.NoError(t, writer.Create(&models.StatusContent{StatusId: status.Id, Content: "hidden original"}).Error)
	}
	require.Equal(t, []string{"7", "2"}, request(nil, "limit=2"))
	require.Equal(t, []string{"7", "6"}, request(viewer, "limit=2"))
	require.Equal(t, []string{"5", "2"}, request(viewer, "limit=2&max_id=6"))
	require.NoError(t, writer.Create(&models.Block{AccountId: owner.Id, TargetAccountId: viewer.Id}).Error)
	require.Empty(t, request(viewer, "limit=80"))
}
