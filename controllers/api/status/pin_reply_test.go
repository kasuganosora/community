package status

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

func setupPinReplyDB(t *testing.T) (*gorm.DB, *models.Account, *models.Account) {
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
	))
	prev := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(prev) })

	now := time.Now().UTC()
	author := &models.Account{Id: 1, Username: "author", Acct: "author", DisplayName: "Author", CreatedAt: &now, UpdatedAt: &now}
	replier := &models.Account{Id: 2, Username: "replier", Acct: "replier", DisplayName: "Replier", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{author, replier}).Error)
	return conn, author, replier
}

func TestContextSortHotAuthorOnlyAndPinReply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conn, author, replier := setupPinReplyDB(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	rootID := uint64(10)

	t1 := now.Add(time.Minute)
	t2 := now.Add(2 * time.Minute)
	t3 := now.Add(3 * time.Minute)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Status{
		{Id: rootID, AccountId: author.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "root"}},
		{Id: 11, AccountId: replier.Id, InReplyToId: &rootID, Visibility: models.StatusVisibilityPublic, FavouritesCount: 1, CreatedAt: &t1, UpdatedAt: &t1, Content: models.StatusContent{Content: "first reply"}},
		{Id: 12, AccountId: replier.Id, InReplyToId: &rootID, Visibility: models.StatusVisibilityPublic, FavouritesCount: 25, ReblogsCount: 10, CreatedAt: &t2, UpdatedAt: &t2, Content: models.StatusContent{Content: "hot reply"}},
		{Id: 13, AccountId: author.Id, InReplyToId: &rootID, Visibility: models.StatusVisibilityPublic, FavouritesCount: 0, CreatedAt: &t3, UpdatedAt: &t3, Content: models.StatusContent{Content: "author followup"}},
	}).Error)

	newRouter := func(viewer *models.Account) *gin.Engine {
		r := gin.New()
		r.Use(controllers.AbortRecoveryMiddleware())
		r.Use(func(c *gin.Context) {
			if viewer != nil {
				c.Set("account", viewer)
			}
			c.Next()
		})
		r.GET("/statuses/:id/context", Context)
		r.POST("/statuses/:id/pin_reply", PinReply)
		r.DELETE("/statuses/:id/pin_reply", UnpinReply)
		return r
	}

	authorRouter := newRouter(author)
	replierRouter := newRouter(replier)

	// 1. sort=hot places reply 12 first.
	w := httptest.NewRecorder()
	authorRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/statuses/10/context?sort=hot", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var ctxHot ContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ctxHot))
	require.Len(t, ctxHot.Descendants, 3)
	require.Equal(t, "12", ctxHot.Descendants[0].Id)

	// 2. author_only=true keeps only author's reply 13.
	w = httptest.NewRecorder()
	authorRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/statuses/10/context?author_only=true", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var ctxAuthor ContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ctxAuthor))
	require.Len(t, ctxAuthor.Descendants, 1)
	require.Equal(t, "13", ctxAuthor.Descendants[0].Id)

	// 3. Non-author cannot pin reply (403).
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/statuses/10/pin_reply", bytes.NewBufferString(`{"reply_id":"11"}`))
	req.Header.Set("Content-Type", "application/json")
	replierRouter.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)

	// 4. Author pins reply 13 -> promoted to first even under sort=hot.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/statuses/10/pin_reply", bytes.NewBufferString(`{"reply_id":"13"}`))
	req.Header.Set("Content-Type", "application/json")
	authorRouter.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	authorRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/statuses/10/context?sort=hot", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var ctxPinned ContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ctxPinned))
	require.NotNil(t, ctxPinned.PinnedReplyId)
	require.Equal(t, "13", *ctxPinned.PinnedReplyId)
	require.Equal(t, "13", ctxPinned.Descendants[0].Id)
	require.Equal(t, "12", ctxPinned.Descendants[1].Id)

	// 5. Unpin restores normal hot order.
	w = httptest.NewRecorder()
	authorRouter.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/statuses/10/pin_reply", nil))
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	authorRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/statuses/10/context?sort=hot", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var ctxUnpinned ContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ctxUnpinned))
	require.Nil(t, ctxUnpinned.PinnedReplyId)
	require.Equal(t, "12", ctxUnpinned.Descendants[0].Id)
}
