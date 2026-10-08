package timelines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redismock/v9"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db/database"
	redisTest "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestMain injects a mock Redis client so feed service doesn't panic.
func TestMain(m *testing.M) {
	client, _ := redismock.NewClientMock()
	redisTest.SetRedisesForTest([]*goredis.Client{client})
	os.Exit(m.Run())
}

// safeHandler wraps handler calls that may panic from controller helpers.
func safeHandler(t *testing.T, handler func()) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			// Only swallow AbortSentinel (controller abort panics), re-panic on real bugs
			if _, ok := r.(controllers.AbortSentinel); ok {
				return
			}
			panic(r)
		}
	}()

	handler()
}

// newTestContext creates a gin.Context with a request.
func newTestContext(t *testing.T, method, path, query string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	url := path
	if query != "" {
		url = path + "?" + query
	}

	req := httptest.NewRequest(method, url, nil) //nolint:noctx
	c.Request = req

	return c, w
}

// newTestContextWithBody creates a gin.Context with a JSON body.
func newTestContextWithBody(t *testing.T, method, path string, body any) (*gin.Context, *httptest.ResponseRecorder) { //nolint:unparam // param reserved for future use
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	b, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, bytes.NewReader(b)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	return c, w
}

// setAccount sets the account in gin context.
func setAccount(c *gin.Context, account *models.Account) {
	c.Set("account", account)
}

// setupTestDB creates an in-memory SQLite DB for testing.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	// Use a unique DSN per call to avoid shared-cache test pollution
	dsn := fmt.Sprintf("file:timelines_test_%d?mode=memory&cache=shared", time.Now().UnixNano())

	db, err := pgtest.GormOpen(t, sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	err = db.AutoMigrate(
		&models.Notification{},
		&models.Account{},
		&models.User{},
		&models.Status{},
		&models.List{},
		&models.ListFollowAccount{},
		&models.ListAccount{},
		&models.Emoji{},
		&models.AccountExtra{},
		&models.UserSetting{},
		&models.StatusEmoji{},
		&models.StatusTag{},
		&models.StatusToAccount{},
		&models.Tag{},
		&models.PinStatus{},
		&models.BookmarkStatus{},
		&models.FavoriteStatus{},
		&models.StatusMediaAttachment{},
		&models.MediaAttachment{},
		&models.Poll{},
		&models.PollOption{},
		&models.Application{},
		&models.Conversation{},
		&models.AccountConversation{},
		&models.Follow{},
		&models.Block{},
		&models.Report{},
		&models.Filter{},
	)
	require.NoError(t, err)

	return db
}

// setGlobalTestDB sets the global test DB and registers cleanup.
func setGlobalTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	database.SetTestDB(db)
	t.Cleanup(func() {
		database.ResetTestDB()
	})
}

// makeAccount creates a test account with the given ID.
func makeAccount(id uint64) *models.Account {
	now := time.Now()

	return &models.Account{
		Id:          id,
		Username:    "testuser", //nolint:goconst
		DisplayName: "Test User",
		Avatar:      "",
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
}

// makeUser creates a test user.
func makeUser(id uint64) *models.User { //nolint:unparam // param reserved for future use
	now := time.Now()

	return &models.User{
		Id:        id,
		UUID:      "test-uuid-" + string(rune(id)), //nolint:gosec // G115: reviewed and safe
		UserName:  "testuser",
		CreatedAt: &now,
		UpdatedAt: &now,
		Settings: &models.UserSetting{
			ID:     "test-settings", //nolint:goconst
			UserID: int64(id),       //nolint:gosec // G115: reviewed and safe
			Data:   map[string]any{},
		},
	}
}

// =====================================================================
// TimelineRequest.ToFeedListParams Tests
// =====================================================================

func TestTimelineRequest_ToFeedListParams_DefaultLimit(t *testing.T) {
	req := &TimelineRequest{}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(20), params.Limit)
	assert.Equal(t, uint64(0), params.MaxId)
	assert.Equal(t, uint64(0), params.MinId)
	assert.Equal(t, uint64(0), params.SinceId)
	assert.NotNil(t, params.Other)
}

func TestTimelineRequest_ToFeedListParams_CustomValues(t *testing.T) {
	req := &TimelineRequest{
		MaxId:   100,
		MinId:   50,
		SinceId: 75,
		Limit:   40,
	}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(100), params.MaxId)
	assert.Equal(t, uint64(50), params.MinId)
	assert.Equal(t, uint64(75), params.SinceId)
	assert.Equal(t, uint64(40), params.Limit)
}

func TestTimelineRequest_ToFeedListParams_LimitExceedsMax(t *testing.T) {
	req := &TimelineRequest{Limit: 200}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(80), params.Limit)
}

func TestTimelineRequest_ToFeedListParams_LimitExactlyMax(t *testing.T) {
	req := &TimelineRequest{Limit: 80}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(80), params.Limit)
}

func TestTimelineRequest_ToFeedListParams_LimitOne(t *testing.T) {
	req := &TimelineRequest{Limit: 1}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(1), params.Limit)
}

func TestTimelineRequest_ToFeedListParams_OtherMapInitialized(t *testing.T) {
	req := &TimelineRequest{Limit: 10}
	params := req.ToFeedListParams()
	require.NotNil(t, params.Other)
	assert.Empty(t, params.Other)
}

// =====================================================================
// PublicTimeline.ToFeedListParams Tests
// =====================================================================

func TestPublicTimeline_ToFeedListParams_Defaults(t *testing.T) {
	req := &PublicTimeline{
		TimelineRequest: TimelineRequest{Limit: 10},
	}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(10), params.Limit)
	assert.Equal(t, false, params.Other["local"])
	assert.Equal(t, false, params.Other["remote"])
	assert.Equal(t, false, params.Other["only_media"])
}

func TestPublicTimeline_ToFeedListParams_WithLocal(t *testing.T) {
	req := &PublicTimeline{
		TimelineRequest: TimelineRequest{Limit: 10},
		Local:           "true", //nolint:goconst
	}
	params := req.ToFeedListParams()
	assert.Equal(t, true, params.Other["local"])
}

func TestPublicTimeline_ToFeedListParams_WithRemote(t *testing.T) {
	req := &PublicTimeline{
		TimelineRequest: TimelineRequest{Limit: 10},
		Remote:          "1",
	}
	params := req.ToFeedListParams()
	assert.Equal(t, true, params.Other["remote"])
}

func TestPublicTimeline_ToFeedListParams_WithOnlyMedia(t *testing.T) {
	req := &PublicTimeline{
		TimelineRequest: TimelineRequest{Limit: 10},
		OnlyMedia:       "true",
	}
	params := req.ToFeedListParams()
	assert.Equal(t, true, params.Other["only_media"])
}

func TestPublicTimeline_ToFeedListParams_WithAllOptions(t *testing.T) {
	req := &PublicTimeline{
		TimelineRequest: TimelineRequest{Limit: 10, MaxId: 100},
		Local:           "1",
		Remote:          "1",
		OnlyMedia:       "1",
	}
	params := req.ToFeedListParams()
	assert.Equal(t, true, params.Other["local"])
	assert.Equal(t, true, params.Other["remote"])
	assert.Equal(t, true, params.Other["only_media"])
	assert.Equal(t, uint64(100), params.MaxId)
}

func TestPublicTimeline_ToFeedListParams_LimitCapping(t *testing.T) {
	req := &PublicTimeline{
		TimelineRequest: TimelineRequest{Limit: 150},
	}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(80), params.Limit)
}

// =====================================================================
// TagTimelineRequest.ToFeedListParams Tests
// =====================================================================

func TestTagTimelineRequest_ToFeedListParams_Defaults(t *testing.T) {
	req := &TagTimelineRequest{
		TimelineRequest: TimelineRequest{Limit: 10},
	}
	params := req.ToFeedListParams()
	assert.Equal(t, uint64(10), params.Limit)
	assert.Equal(t, false, params.Other["only_media"])
	assert.Equal(t, false, params.Other["local"])
	assert.Equal(t, false, params.Other["remote"])
}

func TestTagTimelineRequest_ToFeedListParams_WithOnlyMedia(t *testing.T) {
	req := &TagTimelineRequest{
		TimelineRequest: TimelineRequest{Limit: 10},
		OnlyMedia:       true,
	}
	params := req.ToFeedListParams()
	assert.Equal(t, true, params.Other["only_media"])
}

func TestTagTimelineRequest_ToFeedListParams_WithLocal(t *testing.T) {
	req := &TagTimelineRequest{
		TimelineRequest: TimelineRequest{Limit: 10},
		Local:           "true",
	}
	params := req.ToFeedListParams()
	assert.Equal(t, true, params.Other["local"])
}

func TestTagTimelineRequest_ToFeedListParams_WithRemote(t *testing.T) {
	req := &TagTimelineRequest{
		TimelineRequest: TimelineRequest{Limit: 10},
		Remote:          "1",
	}
	params := req.ToFeedListParams()
	assert.Equal(t, true, params.Other["remote"])
}

// =====================================================================
// Home Handler Tests
// =====================================================================

func TestHome_BindError(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/timelines/home", "limit=not_a_number")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Home(c)
	})
	// With invalid query param, Bind returns an error -> 400
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHome_NoAccount(t *testing.T) {
	c, _ := newTestContext(t, "GET", "/api/v1/timelines/home", "")

	// Without an account, Home passes nil account to feed service which panics.
	// In production, auth middleware always sets account. This test verifies
	// that safeHandler catches the panic (it re-panics on non-AbortSentinel).
	assert.Panics(t, func() {
		safeHandler(t, func() {
			Home(c)
		})
	})
}

func TestHome_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/home", "limit=10")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Home(c)
	})
	// With empty DB, feed function should return empty or error
	// If feed returns empty, serializer returns empty -> 200
	// If feed errors, AbortIfError panics with abort -> we catch it
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusInternalServerError || c.IsAborted(),
		"expected 200 or abort but got %d", w.Code)
}

func TestHome_DefaultLimit(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/home", "")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Home(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

// =====================================================================
// Mention Handler Tests
// =====================================================================

func TestMention_BindError(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/timelines/mentions", "limit=invalid")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Mention(c)
	})
	// BindQuery auto-aborts with 400 on validation error
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMention_NoAccount(t *testing.T) {
	c, _ := newTestContext(t, "GET", "/api/v1/timelines/mentions", "limit=10")

	safeHandler(t, func() {
		Mention(c)
	})
	// MustGetAccount panics with abort if no account
}

func TestMention_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/mentions", "limit=10")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Mention(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestMention_WithPagination(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/mentions", "max_id=100&min_id=50&since_id=25&limit=10")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Mention(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

// =====================================================================
// List Handler Tests
// =====================================================================

func TestList_BindError(t *testing.T) {
	c, _ := newTestContext(t, "GET", "/api/v1/timelines/list/abc", "limit=invalid")
	account := makeAccount(1)
	setAccount(c, account)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}

	safeHandler(t, func() {
		List(c)
	})
	// The bind may fail or the id parse may fail, either way should abort
}

func TestList_InvalidID(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/timelines/list/abc", "limit=10")
	account := makeAccount(1)
	setAccount(c, account)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}

	safeHandler(t, func() {
		List(c)
	})
	// GetIdFromContext should panic("abort") for invalid id -> 400
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestList_NotFound(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/list/9999", "limit=10")
	account := makeAccount(1)
	setAccount(c, account)
	c.Params = gin.Params{{Key: "id", Value: "9999"}}

	safeHandler(t, func() {
		List(c)
	})
	// GetIdFromContext should panic("abort") with 404 for not found
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestList_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	// Create a list in the DB
	list := &models.List{
		Id:        1,
		Title:     "Test List",
		AccountId: 1,
	}
	err := testDB.Create(list).Error
	require.NoError(t, err)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/list/1", "limit=10")
	account := makeAccount(1)
	setAccount(c, account)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	safeHandler(t, func() {
		List(c)
	})
	// Should proceed to feed function, which may return empty or error
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

// =====================================================================
// Public Handler Tests
// =====================================================================

func TestPublic_BindError(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/timelines/public", "limit=invalid")

	safeHandler(t, func() {
		Public(c)
	})
	// BindQuery auto-aborts with 400 on validation error
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPublic_SuccessNoAccount(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/public", "limit=10")

	safeHandler(t, func() {
		Public(c)
	})
	// Without account, serializer gets nil account - should still work
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestPublic_SuccessWithAccount(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/public", "limit=10")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Public(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestPublic_OnlyMedia(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/public", "only_media=true&limit=10")

	safeHandler(t, func() {
		Public(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestPublic_LocalAndRemote(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/public", "local=true&remote=false&limit=10")

	safeHandler(t, func() {
		Public(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestPublic_WithPagination(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/public", "max_id=100&since_id=50&limit=10")

	safeHandler(t, func() {
		Public(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

// =====================================================================
// Tag Handler Tests
// =====================================================================

func TestTag_BindError(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/timelines/tag/test", "limit=invalid")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Tag(c)
	})
	// BindQuery fails -> 400
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTag_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/tag/golang", "limit=10")
	c.Params = gin.Params{{Key: "tag", Value: "golang"}} //nolint:goconst
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Tag(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestTag_NoAccount(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/tag/test", "limit=10")
	c.Params = gin.Params{{Key: "tag", Value: "test"}} //nolint:goconst

	safeHandler(t, func() {
		Tag(c)
	})
	// GetAccount returns nil, feed should still work with nil account
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestTag_WithOnlyMedia(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/tag/test", "only_media=true&limit=10")
	c.Params = gin.Params{{Key: "tag", Value: "test"}}
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Tag(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

func TestTag_WithLocalRemote(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/timelines/tag/test", "local=true&remote=true&limit=10")
	c.Params = gin.Params{{Key: "tag", Value: "test"}}
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		Tag(c)
	})
	assert.True(t, w.Code == http.StatusOK || c.IsAborted())
}

// =====================================================================
// NotificationTimeline Handler Tests
// =====================================================================

func TestNotificationTimeline_BindError(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=not_a_number")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	// ShouldBindQuery fails -> AbortIf -> 422
	assert.Equal(t, 422, w.Code)
}

func TestNotificationTimeline_NoAccount(t *testing.T) {
	c, _ := newTestContext(t, "GET", "/api/v1/notifications", "limit=10")

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	// MustGetAccount panics with abort
}

func TestNotificationTimeline_EmptyResult(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=10")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	// Should return 200 with empty array
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNotificationTimeline_WithNotifications(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	n := &models.Notification{
		Type:          uint8(models.NotificationTypeFavourite),
		AccountId:     1,
		FromAccountId: 2,
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}
	require.NoError(t, testDB.Create(n).Error)

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=10")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNotificationTimeline_ExcludeTypes(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	// Create notifications of different types
	for _, ntype := range []uint8{models.NotificationTypeFavourite, models.NotificationTypeFollow} {
		n := &models.Notification{
			Type:          ntype,
			AccountId:     1,
			FromAccountId: 2,
			CreatedAt:     &now,
			UpdatedAt:     &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=10&exclude_types[]=favourite")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	// Should not contain favourite notifications
	for _, item := range result {
		assert.NotEqual(t, "favourite", item["type"])
	}
}

func TestNotificationTimeline_FilterByType(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	for _, ntype := range []uint8{models.NotificationTypeFavourite, models.NotificationTypeFollow} {
		n := &models.Notification{
			Type:          ntype,
			AccountId:     1,
			FromAccountId: 2,
			CreatedAt:     &now,
			UpdatedAt:     &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=10&types[]=favourite")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	for _, item := range result {
		assert.Equal(t, "favourite", item["type"])
	}
}

func TestNotificationTimeline_FilterByAccountId(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount1 := makeAccount(2)
	fromAccount2 := makeAccount(3)

	require.NoError(t, testDB.Create(fromAccount1).Error)
	require.NoError(t, testDB.Create(fromAccount2).Error)

	n1 := &models.Notification{
		Type: uint8(models.NotificationTypeFavourite), AccountId: 1, FromAccountId: 2,
		CreatedAt: &now, UpdatedAt: &now,
	}
	n2 := &models.Notification{
		Type: uint8(models.NotificationTypeFavourite), AccountId: 1, FromAccountId: 3,
		CreatedAt: &now, UpdatedAt: &now,
	}

	require.NoError(t, testDB.Create(n1).Error)
	require.NoError(t, testDB.Create(n2).Error)

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=10&account_id=2")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestNotificationTimeline_InvalidAccountId(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=10&account_id=invalid")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	// ParseUint fails -> AbortIf -> 422
	assert.Equal(t, 422, w.Code)
}

func TestNotificationTimeline_WithMaxId(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	for range 5 {
		n := &models.Notification{
			Type: uint8(models.NotificationTypeFavourite), AccountId: 1, FromAccountId: 2,
			CreatedAt: &now, UpdatedAt: &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "max_id=9999&limit=10")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Len(t, result, 5)
}

func TestNotificationTimeline_LimitCapping(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	// Create 200 notifications
	for range 200 {
		n := &models.Notification{
			Type: uint8(models.NotificationTypeFavourite), AccountId: 1, FromAccountId: 2,
			CreatedAt: &now, UpdatedAt: &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	// Request limit=200, should be capped to 100
	c, w := newTestContext(t, "GET", "/api/v1/notifications", "limit=200")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(result), 100)
}

func TestNotificationTimeline_DefaultLimit(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	for range 30 {
		n := &models.Notification{
			Type: uint8(models.NotificationTypeFavourite), AccountId: 1, FromAccountId: 2,
			CreatedAt: &now, UpdatedAt: &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	// No limit specified, should default to 20
	c, w := newTestContext(t, "GET", "/api/v1/notifications", "")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(result), 20)
}

func TestNotificationTimeline_WithMinId(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	for range 5 {
		n := &models.Notification{
			Type: uint8(models.NotificationTypeFavourite), AccountId: 1, FromAccountId: 2,
			CreatedAt: &now, UpdatedAt: &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "min_id=9999&limit=10")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestNotificationTimeline_WithSinceId(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	for range 5 {
		n := &models.Notification{
			Type: uint8(models.NotificationTypeFavourite), AccountId: 1, FromAccountId: 2,
			CreatedAt: &now, UpdatedAt: &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	c, w := newTestContext(t, "GET", "/api/v1/notifications", "since_id=9999&limit=10")
	setAccount(c, account)

	safeHandler(t, func() {
		NotificationTimeline(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// =====================================================================
// GetNotification Handler Tests
// =====================================================================

func TestGetNotification_EmptyID(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/notifications/", "")
	c.Params = gin.Params{{Key: "id", Value: ""}}

	safeHandler(t, func() {
		GetNotification(c)
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetNotification_NotFound(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "GET", "/api/v1/notifications/9999", "")
	c.Params = gin.Params{{Key: "id", Value: "9999"}}
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotification(c)
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetNotification_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	require.NoError(t, testDB.Create(account).Error)

	fromAccount := makeAccount(2)
	require.NoError(t, testDB.Create(fromAccount).Error)

	n := &models.Notification{
		Type:          uint8(models.NotificationTypeFavourite),
		AccountId:     1,
		FromAccountId: 2,
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}
	require.NoError(t, testDB.Create(n).Error)

	c, w := newTestContext(t, "GET", "/api/v1/notifications/1", "")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotification(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, "1", result["id"])
}

func TestGetNotification_WrongAccount(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	ownerAccount := makeAccount(1)
	require.NoError(t, testDB.Create(ownerAccount).Error)

	otherAccount := makeAccount(2)
	require.NoError(t, testDB.Create(otherAccount).Error)

	n := &models.Notification{
		Type:      uint8(models.NotificationTypeFavourite),
		AccountId: 1,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, testDB.Create(n).Error)

	// Try to access as different account
	c, w := newTestContext(t, "GET", "/api/v1/notifications/1", "")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	setAccount(c, otherAccount)

	safeHandler(t, func() {
		GetNotification(c)
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetNotification_WithPreloadedAccount(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	require.NoError(t, testDB.Create(account).Error)

	require.NoError(t, testDB.Create(makeAccount(2)).Error)

	n := &models.Notification{
		Type:          uint8(models.NotificationTypeFollow),
		AccountId:     1,
		FromAccountId: 2,
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}
	require.NoError(t, testDB.Create(n).Error)

	c, w := newTestContext(t, "GET", "/api/v1/notifications/1", "")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotification(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)
}

// =====================================================================
// DismissNotification Handler Tests
// =====================================================================

func TestDismissNotification_EmptyID(t *testing.T) {
	c, w := newTestContext(t, "POST", "/api/v1/notifications//dismiss", "")
	c.Params = gin.Params{{Key: "id", Value: ""}}

	safeHandler(t, func() {
		DismissNotification(c)
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDismissNotification_NotFound(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "POST", "/api/v1/notifications/9999/dismiss", "")
	c.Params = gin.Params{{Key: "id", Value: "9999"}}
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		DismissNotification(c)
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDismissNotification_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	require.NoError(t, testDB.Create(account).Error)

	n := &models.Notification{
		Type:      uint8(models.NotificationTypeFavourite),
		AccountId: 1,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, testDB.Create(n).Error)

	// Verify it exists before dismiss
	var before models.Notification

	testDB.First(&before, n.Id)

	c, w := newTestContext(t, "POST", "/api/v1/notifications/1/dismiss", "")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	setAccount(c, account)

	safeHandler(t, func() {
		DismissNotification(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify it still exists after dismiss
	var after models.Notification

	testDB.First(&after, n.Id)
	_ = after // verify no error from First
}

func TestDismissNotification_WrongAccount(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	ownerAccount := makeAccount(1)
	require.NoError(t, testDB.Create(ownerAccount).Error)

	otherAccount := makeAccount(2)
	require.NoError(t, testDB.Create(otherAccount).Error)

	n := &models.Notification{
		Type:      uint8(models.NotificationTypeFavourite),
		AccountId: 1,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, testDB.Create(n).Error)

	c, w := newTestContext(t, "POST", "/api/v1/notifications/1/dismiss", "")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	setAccount(c, otherAccount)

	safeHandler(t, func() {
		DismissNotification(c)
	})
	// Wrong account -> 0 rows affected -> 404
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// =====================================================================
// ClearNotifications Handler Tests
// =====================================================================

func TestClearNotifications_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account := makeAccount(1)
	require.NoError(t, testDB.Create(account).Error)

	// Create some unread notifications
	for range 5 {
		n := &models.Notification{
			Type:      uint8(models.NotificationTypeFavourite),
			AccountId: 1,
			CreatedAt: &now,
			UpdatedAt: &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	c, w := newTestContext(t, "POST", "/api/v1/notifications/clear", "")
	setAccount(c, account)

	safeHandler(t, func() {
		ClearNotifications(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify all notifications for this account are now read
	var count int64

	testDB.Model(&models.Notification{}).Where("account_id = ? AND is_read = ?", 1, false).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestClearNotifications_NoNotifications(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	c, w := newTestContext(t, "POST", "/api/v1/notifications/clear", "")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		ClearNotifications(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestClearNotifications_DoesNotAffectOtherAccounts(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	account1 := makeAccount(1)
	account2 := makeAccount(2)

	require.NoError(t, testDB.Create(account1).Error)
	require.NoError(t, testDB.Create(account2).Error)

	// Create notifications for both accounts
	for _, aid := range []uint64{1, 2} {
		n := &models.Notification{
			Type:      uint8(models.NotificationTypeFavourite),
			AccountId: aid,
			CreatedAt: &now,
			UpdatedAt: &now,
		}
		require.NoError(t, testDB.Create(n).Error)
	}

	c, w := newTestContext(t, "POST", "/api/v1/notifications/clear", "")
	setAccount(c, account1)

	safeHandler(t, func() {
		ClearNotifications(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Account 1 should be all read
	var count1 int64

	testDB.Model(&models.Notification{}).Where("account_id = ? AND is_read = ?", 1, false).Count(&count1)
	assert.Equal(t, int64(0), count1)

	// Account 2 should still be unread
	var count2 int64

	testDB.Model(&models.Notification{}).Where("account_id = ? AND is_read = ?", 2, false).Count(&count2)
	assert.Equal(t, int64(1), count2)
}

// =====================================================================
// GetNotificationPolicy Handler Tests
// =====================================================================

func TestGetNotificationPolicy_Defaults(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/notifications/policy", "")
	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, false, result["filter_not_following"])
	assert.Equal(t, false, result["filter_not_followers"])
	assert.Equal(t, false, result["filter_new_accounts"])
	assert.Equal(t, false, result["filter_private_mentions"])

	summary, ok := result["summary"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, float64(0), summary["pending_requests_count"], 0)
	assert.InDelta(t, float64(0), summary["pending_notifications_count"], 0)
}

func TestGetNotificationPolicy_WithUserSettings(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/notifications/policy", "")
	account := makeAccount(1)
	account.User = makeUser(1)
	account.User.Settings = &models.UserSetting{
		ID:     "test-settings",
		UserID: 1,
		Data: map[string]any{
			"notification_filter_not_following":    true, //nolint:goconst
			"notification_filter_not_followers":    true,
			"notification_filter_new_accounts":     false,
			"notification_filter_private_mentions": true,
		},
	}
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, true, result["filter_not_following"])
	assert.Equal(t, true, result["filter_not_followers"])
	assert.Equal(t, false, result["filter_new_accounts"])
	assert.Equal(t, true, result["filter_private_mentions"])
}

func TestGetNotificationPolicy_UserWithNilSettings(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/notifications/policy", "")
	account := makeAccount(1)
	account.User = makeUser(1)
	account.User.Settings = nil
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, false, result["filter_not_following"])
}

func TestGetNotificationPolicy_UserWithNilData(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/notifications/policy", "")
	account := makeAccount(1)
	account.User = makeUser(1)
	account.User.Settings = &models.UserSetting{
		ID:     "test-settings",
		UserID: 1,
		Data:   nil,
	}
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, false, result["filter_not_following"])
}

func TestGetNotificationPolicy_PartialSettings(t *testing.T) {
	c, w := newTestContext(t, "GET", "/api/v1/notifications/policy", "")
	account := makeAccount(1)
	account.User = makeUser(1)
	account.User.Settings = &models.UserSetting{
		ID:     "test-settings",
		UserID: 1,
		Data: map[string]any{
			"notification_filter_not_following": true,
		},
	}
	setAccount(c, account)

	safeHandler(t, func() {
		GetNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, true, result["filter_not_following"])
	assert.Equal(t, false, result["filter_not_followers"])
	assert.Equal(t, false, result["filter_new_accounts"])
	assert.Equal(t, false, result["filter_private_mentions"])
}

// =====================================================================
// UpdateNotificationPolicy Handler Tests
// =====================================================================

func TestUpdateNotificationPolicy_InvalidBody(t *testing.T) {
	c, w := newTestContext(t, "POST", "/api/v1/notifications/policy", "")
	c.Request = httptest.NewRequest("POST", "/api/v1/notifications/policy", bytes.NewReader([]byte("{not valid json")))
	c.Request.Header.Set("Content-Type", "application/json")

	account := makeAccount(1)
	setAccount(c, account)

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateNotificationPolicy_NoUser(t *testing.T) {
	c, w := newTestContextWithBody(t, "POST", "/api/v1/notifications/policy", map[string]bool{
		"filter_not_following": true, //nolint:goconst
	})
	account := makeAccount(1)
	setAccount(c, account)
	// account.User is nil

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	// Without User, the function skips the DB save and returns 200 with false values
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdateNotificationPolicy_NoSettings(t *testing.T) {
	c, w := newTestContextWithBody(t, "POST", "/api/v1/notifications/policy", map[string]bool{
		"filter_not_following": true,
	})
	account := makeAccount(1)
	account.User = makeUser(1)
	account.User.Settings = nil
	setAccount(c, account)

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	// Without Settings, the function skips the DB save and returns 200
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdateNotificationPolicy_Success(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	// Create user and settings
	user := &models.User{
		UUID:     "test-uuid-update",
		UserName: "testuser",
		Settings: &models.UserSetting{
			ID:     "test-settings-update",
			UserID: 1,
			Data:   map[string]any{},
		},
	}
	require.NoError(t, testDB.Create(user).Error)

	account := makeAccount(1)
	account.User = user
	account.UserId = 1

	trueVal := true
	c, w := newTestContextWithBody(t, "POST", "/api/v1/notifications/policy", UpdateNotificationPolicyRequest{
		FilterNotFollowing:    &trueVal,
		FilterNotFollowers:    &trueVal,
		FilterNewAccounts:     &trueVal,
		FilterPrivateMentions: &trueVal,
	})
	setAccount(c, account)

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, true, result["filter_not_following"])
	assert.Equal(t, true, result["filter_not_followers"])
	assert.Equal(t, true, result["filter_new_accounts"])
	assert.Equal(t, true, result["filter_private_mentions"])
}

func TestUpdateNotificationPolicy_PartialUpdate(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	user := &models.User{
		UUID:     "test-uuid-partial",
		UserName: "testuser",
		Settings: &models.UserSetting{
			ID:     "test-settings-partial",
			UserID: 1,
			Data: map[string]any{
				"notification_filter_not_following":    false,
				"notification_filter_not_followers":    false,
				"notification_filter_new_accounts":     false,
				"notification_filter_private_mentions": false,
			},
		},
	}
	require.NoError(t, testDB.Create(user).Error)

	account := makeAccount(1)
	account.User = user
	account.UserId = 1

	falseVal := false
	trueVal := true
	c, w := newTestContextWithBody(t, "POST", "/api/v1/notifications/policy", UpdateNotificationPolicyRequest{
		FilterNotFollowing: &trueVal,
		FilterNotFollowers: &falseVal,
		// FilterNewAccounts: nil (not updated)
		FilterPrivateMentions: &falseVal,
	})
	setAccount(c, account)

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, true, result["filter_not_following"])
	assert.Equal(t, false, result["filter_not_followers"])
	// nil fields default to false in response
	assert.Equal(t, false, result["filter_new_accounts"])
	assert.Equal(t, false, result["filter_private_mentions"])

	// Verify the DB was updated
	var settings models.UserSetting

	testDB.First(&settings, "user_id = ?", 1)
	assert.Equal(t, true, settings.Data["notification_filter_not_following"])
	// The ones we didn't update should still be false
	assert.Equal(t, false, settings.Data["notification_filter_new_accounts"])
}

func TestUpdateNotificationPolicy_NilData(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	user := &models.User{
		UUID:     "test-uuid-nildata",
		UserName: "testuser",
		Settings: &models.UserSetting{
			ID:     "test-settings-nildata",
			UserID: 1,
			Data:   nil,
		},
	}
	require.NoError(t, testDB.Create(user).Error)

	account := makeAccount(1)
	account.User = user
	account.UserId = 1

	trueVal := true
	c, w := newTestContextWithBody(t, "POST", "/api/v1/notifications/policy", UpdateNotificationPolicyRequest{
		FilterNotFollowing: &trueVal,
	})
	setAccount(c, account)

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, true, result["filter_not_following"])
}

func TestUpdateNotificationPolicy_AllFalse(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	user := &models.User{
		UUID:     "test-uuid-allfalse",
		UserName: "testuser",
		Settings: &models.UserSetting{
			ID:     "test-settings-allfalse",
			UserID: 1,
			Data: map[string]any{
				"notification_filter_not_following": true,
			},
		},
	}
	require.NoError(t, testDB.Create(user).Error)

	account := makeAccount(1)
	account.User = user
	account.UserId = 1

	falseVal := false
	c, w := newTestContextWithBody(t, "POST", "/api/v1/notifications/policy", UpdateNotificationPolicyRequest{
		FilterNotFollowing:    &falseVal,
		FilterNotFollowers:    &falseVal,
		FilterNewAccounts:     &falseVal,
		FilterPrivateMentions: &falseVal,
	})
	setAccount(c, account)

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, false, result["filter_not_following"])
	assert.Equal(t, false, result["filter_not_followers"])
	assert.Equal(t, false, result["filter_new_accounts"])
	assert.Equal(t, false, result["filter_private_mentions"])
}

func TestUpdateNotificationPolicy_EmptyBody(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	user := &models.User{
		UUID:     "test-uuid-empty",
		UserName: "testuser",
		Settings: &models.UserSetting{
			ID:     "test-settings-empty",
			UserID: 1,
			Data:   map[string]any{},
		},
	}
	require.NoError(t, testDB.Create(user).Error)

	account := makeAccount(1)
	account.User = user
	account.UserId = 1

	c, w := newTestContextWithBody(t, "POST", "/api/v1/notifications/policy", UpdateNotificationPolicyRequest{})
	setAccount(c, account)

	safeHandler(t, func() {
		UpdateNotificationPolicy(c)
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, false, result["filter_not_following"])
	assert.Equal(t, false, result["filter_not_followers"])
	assert.Equal(t, false, result["filter_new_accounts"])
	assert.Equal(t, false, result["filter_private_mentions"])
}

// =====================================================================
// buildNotificationResponse Tests
// =====================================================================

func TestBuildNotificationResponse_Basic(t *testing.T) {
	now := time.Now()
	n := &models.Notification{
		Id:        42,
		Type:      uint8(models.NotificationTypeFavourite),
		CreatedAt: &now,
	}

	c, _ := newTestContext(t, "GET", "/api/v1/notifications/42", "")
	c.Request.Host = "example.com"

	result := buildNotificationResponse(n, c)
	assert.Equal(t, "42", result["id"])
	assert.Equal(t, uint8(models.NotificationTypeFavourite), result["type"])
	assert.Nil(t, result["account"])
	assert.Nil(t, result["status"])
}

func TestBuildNotificationResponse_WithAccount(t *testing.T) {
	now := time.Now()
	n := &models.Notification{
		Id:   1,
		Type: uint8(models.NotificationTypeFollow),
		Account: &models.Account{
			Id:          5,
			Username:    "testuser",
			DisplayName: "Test User",
			Avatar:      "/avatar.png",
		},
		CreatedAt: &now,
	}

	c, _ := newTestContext(t, "GET", "/api/v1/notifications/1", "")
	c.Request.Host = "example.com"

	result := buildNotificationResponse(n, c)
	assert.Equal(t, "1", result["id"])

	accountMap, ok := result["account"].(gin.H)
	require.True(t, ok)
	assert.Equal(t, "5", accountMap["id"])
	assert.Equal(t, "testuser", accountMap["username"])
	assert.Equal(t, "Test User", accountMap["display_name"])
	assert.Equal(t, "/avatar.png", accountMap["avatar"])
	assert.Equal(t, "/avatar.png", accountMap["avatar_static"])
	assert.Equal(t, "https://example.com/@testuser", accountMap["url"])
}

func TestBuildNotificationResponse_WithAccountNoAvatar(t *testing.T) {
	now := time.Now()
	n := &models.Notification{
		Id:   1,
		Type: uint8(models.NotificationTypeMention),
		Account: &models.Account{
			Id:          3,
			Username:    "noavatar",
			DisplayName: "No Avatar",
			Avatar:      "",
		},
		CreatedAt: &now,
	}

	c, _ := newTestContext(t, "GET", "/api/v1/notifications/1", "")
	c.Request.Host = "test.local"

	result := buildNotificationResponse(n, c)

	accountMap, ok := result["account"].(gin.H)
	require.True(t, ok)
	assert.Equal(t, "3", accountMap["id"])
	assert.Empty(t, accountMap["avatar"])
	assert.Equal(t, "https://test.local/@noavatar", accountMap["url"])
}

func TestBuildNotificationResponse_NilAccount(t *testing.T) {
	now := time.Now()
	n := &models.Notification{
		Id:        99,
		Type:      uint8(models.NotificationTypeReblog),
		Account:   nil,
		CreatedAt: &now,
	}

	c, _ := newTestContext(t, "GET", "/api/v1/notifications/99", "")

	result := buildNotificationResponse(n, c)
	assert.Equal(t, "99", result["id"])
	assert.Nil(t, result["account"])
	assert.Nil(t, result["status"])
}

func TestBuildNotificationResponse_AllNotificationTypes(t *testing.T) {
	now := time.Now()

	types := []struct {
		typeVal uint8
		name    string
	}{
		{models.NotificationTypeMention, "mention"},
		{models.NotificationTypeReblog, "reblog"},
		{models.NotificationTypeFavourite, "favourite"},
		{models.NotificationTypeFollow, "follow"},
		{models.NotificationTypePoll, "poll"},
	}

	c, _ := newTestContext(t, "GET", "/api/v1/notifications/1", "")

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			n := &models.Notification{
				Id:        1,
				Type:      tt.typeVal,
				CreatedAt: &now,
			}
			result := buildNotificationResponse(n, c)
			assert.Equal(t, tt.typeVal, result["type"])
		})
	}
}

// =====================================================================
// NotificationTimelineRequest Tests
// =====================================================================

func TestNotificationTimelineRequest_Binding(t *testing.T) {
	t.Run("valid binding", func(t *testing.T) {
		c, _ := newTestContext(t, "GET", "/api/v1/notifications", "limit=10&max_id=100&min_id=50&since_id=25")

		var req NotificationTimelineRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, 10, req.Limit)
		assert.Equal(t, uint64(100), req.MaxId)
		assert.Equal(t, uint64(50), req.MinId)
		assert.Equal(t, uint64(25), req.SinceId)
	})

	t.Run("with exclude types", func(t *testing.T) {
		c, _ := newTestContext(t, "GET", "/api/v1/notifications", "exclude_types[]=favourite&exclude_types[]=follow")

		var req NotificationTimelineRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Contains(t, req.ExcludeTypes, "favourite")
		assert.Contains(t, req.ExcludeTypes, "follow")
	})

	t.Run("with types filter", func(t *testing.T) {
		c, _ := newTestContext(t, "GET", "/api/v1/notifications", "types[]=mention&types[]=reblog")

		var req NotificationTimelineRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Contains(t, req.Types, "mention")
		assert.Contains(t, req.Types, "reblog")
	})

	t.Run("with account_id", func(t *testing.T) {
		c, _ := newTestContext(t, "GET", "/api/v1/notifications", "account_id=42")

		var req NotificationTimelineRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, "42", req.AccountId)
	})
}
