package account

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountStatusesTriggersRemoteProfileBackfill(t *testing.T) {
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
	domain := "remote.example"
	require.NoError(t, writer.Create(&models.Account{Id: 1, Username: "local"}).Error)
	require.NoError(t, writer.Create(&models.Account{Id: 2, Username: "alice", Domain: &domain, OriginId: "https://remote.example/users/alice"}).Error)

	var calls int
	var next func() (string, <-chan struct{})
	prevHook, prevWait := profileBackfill, ProfileBackfillWait
	profileBackfill = func(_ context.Context, a *models.Account) (string, <-chan struct{}) {
		calls++
		require.Equal(t, "https://remote.example/users/alice", a.OriginId)
		return next()
	}
	ProfileBackfillWait = 50 * time.Millisecond
	t.Cleanup(func() { profileBackfill, ProfileBackfillWait = prevHook, prevWait })

	request := func(id, query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/accounts/"+id+"/statuses"+query, nil)
		c.Params = gin.Params{{Key: "id", Value: id}}
		AccountStatuses(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		return w
	}

	// Still running after the wait: the client is told to poll.
	next = func() (string, <-chan struct{}) { return activitypub.ProfileBackfillPending, make(chan struct{}) }
	require.Equal(t, "pending", request("2", "").Header().Get("X-Profile-Backfill"))

	// Finished within the wait: the stored outcome is reported.
	finished := make(chan struct{})
	close(finished)
	outcomes := []string{activitypub.ProfileBackfillPending, activitypub.ProfileBackfillUnavailable}
	next = func() (string, <-chan struct{}) {
		s := outcomes[0]
		outcomes = outcomes[1:]
		if s == activitypub.ProfileBackfillPending {
			return s, finished
		}
		return s, nil
	}
	require.Equal(t, "unavailable", request("2", "?pinned=true").Header().Get("X-Profile-Backfill"))
	calls = 0

	// Local accounts and later pages never backfill.
	require.Empty(t, request("1", "").Header().Get("X-Profile-Backfill"))
	require.Empty(t, request("2", "?max_id=10").Header().Get("X-Profile-Backfill"))
	require.Zero(t, calls)
}
