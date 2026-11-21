package history

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db/database"
	redisdb "github.com/kasuganosora/community/db/redis"
	historysvc "github.com/kasuganosora/community/history"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHistoryHTTPEndpointsAndVisibilityFiltering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		historysvc.Close()
		redisdb.SetUniversalRedis(nil)
		_ = client.Close()
	})
	redisdb.SetUniversalRedis(client)

	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(
		&models.Account{},
		&models.AccountExtra{},
		&models.Status{},
		&models.StatusContent{},
		&models.MediaAttachment{},
		&models.StatusToAccount{},
		&models.StatusTag{},
		&models.Tag{},
		&models.Poll{},
		&models.PollOption{},
		&models.Emoji{},
		&models.StatusEmoji{},
		&models.Application{},
		&models.Block{},
		&models.DomainBlock{},
		&models.Filter{},
		&models.FavoriteStatus{},
		&models.BookmarkStatus{},
		&models.MuteStatus{},
		&models.MuteAccount{},
		&models.PinStatus{},
		&models.Follow{},
		&models.StatusHistory{},
		&models.StatusReaction{},
	))
	prevDB := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(prevDB) })

	now := time.Now().UTC()
	viewer := &models.Account{Id: 1, Username: "viewer", Acct: "viewer", DisplayName: "Viewer", CreatedAt: &now, UpdatedAt: &now}
	stranger := &models.Account{Id: 2, Username: "stranger", Acct: "stranger", DisplayName: "Stranger", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{viewer, stranger}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Status{
		{Id: 10, AccountId: stranger.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "public post"}},
		{Id: 11, AccountId: stranger.Id, Visibility: models.StatusVisibilityPrivate, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "private post"}},
	}).Error)

	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.Use(func(c *gin.Context) {
		c.Set("account", viewer)
		c.Next()
	})
	r.GET("/history", List)
	r.POST("/history", Record)
	r.DELETE("/history/:status_id", Remove)
	r.DELETE("/history", Clear)
	r.GET("/history/settings", GetSettings)
	r.PUT("/history/settings", UpdateSettings)

	// Record both public (10) and private (11) statuses.
	for _, sid := range []string{"10", "11"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/history", bytes.NewBufferString(`{"status_id":"`+sid+`"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}

	// List must return only visible public status 10, hiding private status 11.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var items []historyItemResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
	require.Len(t, items, 1)
	require.Equal(t, "10", items[0].Status.Id)

	// Toggle pause setting.
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/history/settings", bytes.NewBufferString(`{"paused":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history/settings", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"paused":true`)

	// Clear history.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/history", nil))
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "[]", w.Body.String())
}
