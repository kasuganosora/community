package favourite

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupFolderTestDB(t *testing.T) (*gorm.DB, *models.Account, *models.Account, *models.Status) {
	t.Helper()
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
		&models.BookmarkFolder{},
		&models.BookmarkFolderItem{},
	))
	prev := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(prev) })

	now := time.Now().UTC()
	owner := &models.Account{Id: 1, Username: "alice", Acct: "alice", DisplayName: "Alice", CreatedAt: &now, UpdatedAt: &now}
	other := &models.Account{Id: 2, Username: "bob", Acct: "bob", DisplayName: "Bob", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{owner, other}).Error)

	st := &models.Status{
		Id:         100,
		AccountId:  owner.Id,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
		Content:    models.StatusContent{Content: "<p>hello folder</p>"},
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(st).Error)
	return conn, owner, other, st
}

func TestBookmarkFoldersCRUDAndPrivacy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, owner, other, st := setupFolderTestDB(t)

	newRouter := func(viewer *models.Account) *gin.Engine {
		r := gin.New()
		r.Use(controllers.AbortRecoveryMiddleware())
		r.Use(func(c *gin.Context) {
			if viewer != nil {
				c.Set("account", viewer)
			}
			c.Next()
		})
		r.GET("/bookmark_folders", ListBookmarkFolders)
		r.GET("/bookmark_folders/:id", GetBookmarkFolder)
		r.POST("/bookmark_folders", CreateBookmarkFolder)
		r.PUT("/bookmark_folders/:id", UpdateBookmarkFolder)
		r.DELETE("/bookmark_folders/:id", DeleteBookmarkFolder)
		r.POST("/bookmark_folders/:id/statuses", AddStatusToBookmarkFolder)
		r.DELETE("/bookmark_folders/:id/statuses/:status_id", RemoveStatusFromBookmarkFolder)
		r.GET("/bookmark_folders/:id/statuses", BookmarkFolderStatuses)
		return r
	}

	ownerRouter := newRouter(owner)
	otherRouter := newRouter(other)
	anonRouter := newRouter(nil)

	// 1. Create private folder.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bookmark_folders", bytes.NewBufferString(`{"title":"Tech Notes","description":"Go & DB","is_public":false}`))
	req.Header.Set("Content-Type", "application/json")
	ownerRouter.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var created bookmarkFolderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.Equal(t, "Tech Notes", created.Title)
	require.False(t, created.IsPublic)

	// 2. Add status idempotently (twice -> items_count remains 1).
	for range 2 {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/bookmark_folders/"+created.Id+"/statuses", bytes.NewBufferString(`{"status_id":"100"}`))
		req.Header.Set("Content-Type", "application/json")
		ownerRouter.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}

	// 3. List folders with ?status_id=100 reports has_status=true and items_count=1.
	w = httptest.NewRecorder()
	ownerRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bookmark_folders?status_id=100", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var listed []bookmarkFolderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.Len(t, listed, 1)
	require.Equal(t, uint64(1), listed[0].ItemsCount)
	require.NotNil(t, listed[0].HasStatus)
	require.True(t, *listed[0].HasStatus)

	// 4. Private folder metadata & statuses are hidden from non-owners (404), visible to owner (200).
	w = httptest.NewRecorder()
	otherRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bookmark_folders/"+created.Id, nil))
	require.Equal(t, http.StatusNotFound, w.Code)

	w = httptest.NewRecorder()
	otherRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bookmark_folders/"+created.Id+"/statuses", nil))
	require.Equal(t, http.StatusNotFound, w.Code)

	w = httptest.NewRecorder()
	ownerRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bookmark_folders/"+created.Id+"/statuses", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"100"`)

	// 5. Make folder public and clear description to "" -> anonymous visitor can read metadata & statuses.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/bookmark_folders/"+created.Id, bytes.NewBufferString(`{"is_public":true,"description":""}`))
	req.Header.Set("Content-Type", "application/json")
	ownerRouter.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var updatedFolder bookmarkFolderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updatedFolder))
	require.Equal(t, "", updatedFolder.Description)

	w = httptest.NewRecorder()
	anonRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bookmark_folders/"+created.Id, nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"Tech Notes"`)

	w = httptest.NewRecorder()
	anonRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bookmark_folders/"+created.Id+"/statuses", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"100"`)
	require.Contains(t, w.Header().Get("Link"), `rel="prev"`)

	// 6. Non-owner cannot delete folder (403); owner removes status and deletes folder.
	w = httptest.NewRecorder()
	otherRouter.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/bookmark_folders/"+created.Id, nil))
	require.Equal(t, http.StatusForbidden, w.Code)

	w = httptest.NewRecorder()
	ownerRouter.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/bookmark_folders/"+created.Id+"/statuses/100", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var afterRemove bookmarkFolderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &afterRemove))
	require.Equal(t, uint64(0), afterRemove.ItemsCount)

	w = httptest.NewRecorder()
	ownerRouter.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/bookmark_folders/"+created.Id, nil))
	require.Equal(t, http.StatusOK, w.Code)
	_ = st
}
