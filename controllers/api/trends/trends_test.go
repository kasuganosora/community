package trends

import (
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
	"github.com/kasuganosora/community/db/database"
	redisTest "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/models"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// Package-level mock Redis client and mock handle.
// cache.GetCache() uses sync.Once, so the client injected in TestMain is
// the one the cache singleton locks onto for the entire test binary.
var (
	redisClient *goredis.Client
	redisMock   redismock.ClientMock
)

// TestMain injects a mock Redis client so cache.GetCache() doesn't panic.
func TestMain(m *testing.M) {
	client, mock := redismock.NewClientMock()
	redisClient = client
	redisMock = mock
	mock.MatchExpectationsInOrder(false)
	redisTest.SetRedisesForTest([]*goredis.Client{client})
	os.Exit(m.Run())
}

// -----------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------

// newTestContext creates a gin.Context with a GET request and query string.
func newTestContext(t *testing.T, path, query string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	url := path
	if query != "" {
		url = path + "?" + query
	}

	c.Request = httptest.NewRequest(http.MethodGet, url, nil) //nolint:noctx

	return c, w
}

// setupTestDB creates an in-memory SQLite DB with all tables needed by the
// trends handlers and serializer, then installs it as the global test DB.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:trends_test_%d?mode=memory&cache=shared", time.Now().UnixNano())

	gormDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	err = gormDB.AutoMigrate(
		&models.Tag{},
		&models.Status{},
		&models.StatusContent{},
		&models.StatusHistory{},
		&models.Account{},
		&models.AccountExtra{},
		&models.AccountEmoji{},
		&models.User{},
		&models.UserSetting{},
		&models.UserSecretData{},
		&models.Emoji{},
		&models.StatusEmoji{},
		&models.StatusTag{},
		&models.StatusToAccount{},
		&models.StatusMediaAttachment{},
		&models.MediaAttachment{},
		&models.PinStatus{},
		&models.BookmarkStatus{},
		&models.FavoriteStatus{},
		&models.MuteAccount{},
		&models.MuteStatus{},
		&models.Block{},
		&models.Follow{},
		&models.Filter{},
		&models.FilterKeyword{},
		&models.Poll{},
		&models.PollOption{},
		&models.PollOptionVote{},
		&models.Application{},
		&models.Notification{},
		&models.Conversation{},
		&models.AccountConversation{},
		&models.Role{},
		&models.AuthToken{},
		&models.SystemSetting{},
	)
	require.NoError(t, err)

	// Ensure the status_cards table exists for ListLinks tests.
	err = gormDB.Exec(`CREATE TABLE IF NOT EXISTS status_cards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		url TEXT,
		title TEXT,
		description TEXT,
		image_url TEXT,
		provider_name TEXT,
		created_at DATETIME
	)`).Error
	require.NoError(t, err)

	database.SetTestDB(gormDB)
	t.Cleanup(func() {
		database.ResetTestDB()
	})

	// Clear any leftover Redis mock expectations from previous tests.
	redisMock.ClearExpect()

	return gormDB
}

// makeTag creates a Tag fixture with the given fields.
func makeTag(name string, statusCount, accountCount uint64, lastUsed time.Time) *models.Tag {
	return &models.Tag{
		Name:             name,
		StatusCount:      statusCount,
		AccountCount:     accountCount,
		LastStatusUsedAt: &lastUsed,
	}
}

// makeAccount creates a minimal Account fixture.
func makeAccount(id uint64, username string) *models.Account {
	now := time.Now()

	return &models.Account{
		Id:          id,
		Username:    username,
		DisplayName: username,
		Acct:        username + "@__LOCAL__",
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
}

// makeStatus creates a public Status fixture suitable for trending.
func makeStatus(id, accountID uint64, content string, reblogs, favourites uint64, createdAt time.Time) *models.Status {
	return &models.Status{
		Id:              id,
		AccountId:       accountID,
		Visibility:      models.StatusVisibilityPublic,
		ReblogsCount:    reblogs,
		FavouritesCount: favourites,
		Content: models.StatusContent{
			Content: content,
		},
		CreatedAt: &createdAt,
		UpdatedAt: &createdAt,
	}
}

// createStatusCard inserts a row into the status_cards table.
func createStatusCard(t *testing.T, gormDB *gorm.DB, url, title, desc, image, provider string, createdAt time.Time) {
	t.Helper()
	err := gormDB.Exec(
		`INSERT INTO status_cards (url, title, description, image_url, provider_name, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		url, title, desc, image, provider, createdAt,
	).Error
	require.NoError(t, err)
}

// parseJSONArray unmarshals the response body into []map[string]any.
func parseJSONArray(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var result []map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	return result
}

// =====================================================================
// ListTags – Query parameter parsing
// =====================================================================

func TestListTags_DefaultLimitAndOffset(t *testing.T) {
	gormDB := setupTestDB(t)

	// Insert a few tags
	now := time.Now()
	for i := range 5 {
		tag := makeTag(fmt.Sprintf("tag%d", i), uint64(10-i), uint64(i+1), now)
		require.NoError(t, gormDB.Create(tag).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 5)
}

func TestListTags_CustomLimit(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 15 {
		tag := makeTag(fmt.Sprintf("tag%d", i), uint64(100-i), 1, now)
		require.NoError(t, gormDB.Create(tag).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/tags", "limit=3")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 3, "limit=3 should return at most 3 tags")
}

func TestListTags_LimitCappedAt20(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 25 {
		tag := makeTag(fmt.Sprintf("tag%d", i), uint64(25-i), 1, now)
		require.NoError(t, gormDB.Create(tag).Error)
	}

	// limit=50 exceeds the max of 20, so it should be capped to 10 (default)
	// Actually, the code checks parsed <= 20, so 50 is rejected and default 10 is used.
	c, w := newTestContext(t, "/api/v1/trends/tags", "limit=50")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 10, "limit=50 should fall back to default 10")
}

func TestListTags_InvalidLimitFallsBackToDefault(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 15 {
		tag := makeTag(fmt.Sprintf("tag%d", i), uint64(15-i), 1, now)
		require.NoError(t, gormDB.Create(tag).Error)
	}

	tests := []struct {
		name  string
		limit string
	}{
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext(t, "/api/v1/trends/tags", "limit="+tt.limit)
			c.Request.Host = "example.com"
			ListTags(c)
			require.Equal(t, http.StatusOK, w.Code)
			result := parseJSONArray(t, w)
			assert.Len(t, result, 10, "invalid limit should fall back to default 10")
		})
	}
}

func TestListTags_Offset(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 15 {
		tag := makeTag(fmt.Sprintf("tag%d", i), uint64(15-i), 1, now)
		require.NoError(t, gormDB.Create(tag).Error)
	}

	// offset=5, limit=5 should return tags ranked 6-10
	c, w := newTestContext(t, "/api/v1/trends/tags", "limit=5&offset=5")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 5)
}

func TestListTags_InvalidOffsetIgnored(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 3 {
		tag := makeTag(fmt.Sprintf("tag%d", i), uint64(3-i), 1, now)
		require.NoError(t, gormDB.Create(tag).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/tags", "offset=abc")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 3, "invalid offset should default to 0")
}

// =====================================================================
// ListTags – Sorting and filtering
// =====================================================================

func TestListTags_SortedByStatusCountDesc(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	tags := []*models.Tag{
		makeTag("low", 5, 1, now),
		makeTag("high", 100, 1, now),
		makeTag("mid", 50, 1, now),
	}
	for _, tag := range tags {
		require.NoError(t, gormDB.Create(tag).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 3)

	// Sorted by status_count DESC: high(100) > mid(50) > low(5)
	assert.Equal(t, "high", result[0]["name"])
	assert.Equal(t, "mid", result[1]["name"])
	assert.Equal(t, "low", result[2]["name"])

	// Verify history contains correct uses count
	history, ok := result[0]["history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)
	firstHistory, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "100", firstHistory["uses"])
}

func TestListTags_FiltersZeroStatusCount(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	// A tag with status_count > 0 should appear; one with 0 should not.
	require.NoError(t, gormDB.Create(makeTag("active", 10, 1, now)).Error)
	require.NoError(t, gormDB.Create(makeTag("inactive", 0, 1, now)).Error)

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 1)
	assert.Equal(t, "active", result[0]["name"])
}

func TestListTags_FiltersOldTags(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	tenDaysAgo := now.AddDate(0, 0, -10)
	// Recent tag should appear; old tag (>7 days) should not.
	require.NoError(t, gormDB.Create(makeTag("recent", 10, 1, now)).Error)
	require.NoError(t, gormDB.Create(makeTag("old", 10, 1, tenDaysAgo)).Error)

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 1)
	assert.Equal(t, "recent", result[0]["name"])
}

func TestListTags_EmptyResult(t *testing.T) {
	setupTestDB(t)

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Empty(t, result)
}

func TestListTags_ResponseStructure(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	require.NoError(t, gormDB.Create(makeTag("golang", 42, 7, now)).Error)

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 1)

	tag := result[0]
	assert.Equal(t, "golang", tag["name"])
	assert.Equal(t, "https://example.com/tags/golang", tag["url"])

	history, ok := tag["history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)

	entry, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "42", entry["uses"])
	assert.Equal(t, "7", entry["accounts"])
	assert.Equal(t, "1", entry["_version"])
}

// =====================================================================
// ListTags – Cache behavior
// =====================================================================

func TestListTags_CacheHit(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { redisMock.ClearExpect() })

	// Pre-populate cache: GET returns cached data (cache hit).
	cached := []gin.H{
		{
			"name": "cachedtag",
			"url":  "https://example.com/tags/cachedtag",
		},
	}
	cachedJSON, err := json.Marshal(cached)
	require.NoError(t, err)

	// KeyTrendingTags == "trending:tags", and the handler formats
	// "%s:tags:%d:%d" → "trending:tags:tags:10:0"; the cache layer then
	// prepends its "community:" prefix.
	redisMock.ExpectGet("community:trending:tags:tags:10:0").SetVal(string(cachedJSON))

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 1)
	assert.Equal(t, "cachedtag", result[0]["name"])

	// Verify that the cache expectation was consumed.
	assert.NoError(t, redisMock.ExpectationsWereMet())
}

func TestListTags_CacheMissPopulatesCache(t *testing.T) {
	gormDB := setupTestDB(t)
	t.Cleanup(func() { redisMock.ClearExpect() })

	now := time.Now()
	require.NoError(t, gormDB.Create(makeTag("freshtag", 5, 1, now)).Error)

	// Cache miss: GET returns redis.Nil (key not found).
	// KeyTrendingTags == "trending:tags", handler formats
	// "%s:tags:%d:%d" → "trending:tags:tags:10:0"; cache adds "community:".
	redisMock.ExpectGet("community:trending:tags:tags:10:0").SetErr(goredis.Nil)

	// On a cache miss the handler writes the result back to cache. The cache
	// layer adds a random TTL jitter (jitterTTL), so we cannot DeepEqual-match
	// the (jittered) expiration. A custom matcher verifies only the key.
	redisMock.CustomMatch(func(expected, actual []interface{}) error {
		if len(actual) >= 2 {
			if k, ok := actual[1].(string); ok && k == "community:trending:tags:tags:10:0" {
				return nil
			}
		}

		return fmt.Errorf("unexpected set key: %v", actual)
	}).ExpectSet("community:trending:tags:tags:10:0", "", time.Minute).SetVal("OK")

	c, w := newTestContext(t, "/api/v1/trends/tags", "")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 1)
	assert.Equal(t, "freshtag", result[0]["name"])

	// The GET expectation should be fulfilled.
	assert.NoError(t, redisMock.ExpectationsWereMet())
}

// =====================================================================
// ListStatuses – Query parameter parsing
// =====================================================================

func TestListStatuses_DefaultLimitAndOffset(t *testing.T) {
	gormDB := setupTestDB(t)

	// Create account and a few public statuses
	account := makeAccount(1, "alice")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := range 3 {
		s := makeStatus(uint64(i+1), 1, "hello", uint64(i), uint64(i), now)
		require.NoError(t, gormDB.Create(s).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/statuses", "")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 3)
}

func TestListStatuses_CustomLimit(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "bob")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := range 5 {
		s := makeStatus(uint64(i+1), 1, "content", uint64(5-i), 0, now)
		require.NoError(t, gormDB.Create(s).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/statuses", "limit=2")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 2)
}

func TestListStatuses_LimitCappedAt20(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "carol")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := range 15 {
		s := makeStatus(uint64(i+1), 1, "x", uint64(15-i), 0, now)
		require.NoError(t, gormDB.Create(s).Error)
	}

	// limit=100 exceeds max of 20, falls back to default 10
	c, w := newTestContext(t, "/api/v1/trends/statuses", "limit=100")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 10)
}

func TestListStatuses_InvalidLimitFallsBackToDefault(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "dave")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := range 15 {
		s := makeStatus(uint64(i+1), 1, "x", uint64(15-i), 0, now)
		require.NoError(t, gormDB.Create(s).Error)
	}

	for _, invalidLimit := range []string{"abc", "0", "-1"} {
		c, w := newTestContext(t, "/api/v1/trends/statuses", "limit="+invalidLimit)
		ListStatuses(c)
		require.Equal(t, http.StatusOK, w.Code)
		var result []any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		assert.Len(t, result, 10)
	}
}

func TestListStatuses_Offset(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "eve")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := range 10 {
		s := makeStatus(uint64(i+1), 1, "x", uint64(10-i), 0, now)
		require.NoError(t, gormDB.Create(s).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/statuses", "limit=3&offset=5")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 3)
}

func TestListStatuses_InvalidOffsetIgnored(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "frank")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := range 3 {
		s := makeStatus(uint64(i+1), 1, "x", uint64(3-i), 0, now)
		require.NoError(t, gormDB.Create(s).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/statuses", "offset=xyz")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 3)
}

// =====================================================================
// ListStatuses – Sorting and filtering
// =====================================================================

func TestListStatuses_SortedByEngagement(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "grace")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	// reblogs+favourites determines sort order
	statuses := []*models.Status{
		makeStatus(1, 1, "low engagement", 1, 1, now),    // total = 2
		makeStatus(2, 1, "high engagement", 10, 10, now), // total = 20
		makeStatus(3, 1, "mid engagement", 5, 5, now),    // total = 10
	}
	for _, s := range statuses {
		require.NoError(t, gormDB.Create(s).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/statuses", "")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 3)

	// Sorted by (reblogs_count + favourites_count) DESC
	assert.Equal(t, "2", result[0]["id"]) // 20 total
	assert.Equal(t, "3", result[1]["id"]) // 10 total
	assert.Equal(t, "1", result[2]["id"]) // 2 total
}

func TestListStatuses_ExcludesNonPublic(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "henry")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	// Public status should appear
	require.NoError(t, gormDB.Create(makeStatus(1, 1, "public", 10, 10, now)).Error)

	// Private status should NOT appear
	privateStatus := makeStatus(2, 1, "private", 100, 100, now)
	privateStatus.Visibility = models.StatusVisibilityPrivate
	require.NoError(t, gormDB.Create(privateStatus).Error)

	c, w := newTestContext(t, "/api/v1/trends/statuses", "")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0]["id"])
}

func TestListStatuses_ExcludesReplies(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "ivan")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	// Top-level status should appear
	require.NoError(t, gormDB.Create(makeStatus(1, 1, "original", 10, 10, now)).Error)

	// Reply status should NOT appear
	replyStatus := makeStatus(2, 1, "reply", 100, 100, now)
	parentID := uint64(1)
	replyStatus.InReplyToId = &parentID
	require.NoError(t, gormDB.Create(replyStatus).Error)

	c, w := newTestContext(t, "/api/v1/trends/statuses", "")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0]["id"])
}

func TestListStatuses_FiltersOldStatuses(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "jack")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	tenDaysAgo := now.AddDate(0, 0, -10)

	// Recent status should appear
	require.NoError(t, gormDB.Create(makeStatus(1, 1, "recent", 10, 10, now)).Error)
	// Old status (>7 days) should NOT appear
	require.NoError(t, gormDB.Create(makeStatus(2, 1, "old", 100, 100, tenDaysAgo)).Error)

	c, w := newTestContext(t, "/api/v1/trends/statuses", "")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0]["id"])
}

func TestListStatuses_EmptyResult(t *testing.T) {
	setupTestDB(t)

	c, w := newTestContext(t, "/api/v1/trends/statuses", "")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Empty(t, result)
}

func TestListStatuses_ResponseContainsAccount(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "kate")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	require.NoError(t, gormDB.Create(makeStatus(1, 1, "hello world", 5, 5, now)).Error)

	c, w := newTestContext(t, "/api/v1/trends/statuses", "")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)

	// The serialized status should include an account object
	accountMap, ok := result[0]["account"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "kate", accountMap["username"])
}

// =====================================================================
// ListLinks – Query parameter parsing
// =====================================================================

func TestListLinks_DefaultLimitAndOffset(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 3 {
		createStatusCard(t, gormDB,
			fmt.Sprintf("https://example.com/article%d", i),
			fmt.Sprintf("Title %d", i),
			"desc", "img.png", "Example",
			now,
		)
	}

	c, w := newTestContext(t, "/api/v1/trends/links", "")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 3)
}

func TestListLinks_CustomLimit(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 5 {
		createStatusCard(t, gormDB,
			fmt.Sprintf("https://example.com/link%d", i),
			fmt.Sprintf("Title %d", i),
			"", "", "",
			now,
		)
	}

	c, w := newTestContext(t, "/api/v1/trends/links", "limit=2")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 2)
}

func TestListLinks_LimitCappedAt20(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 15 {
		createStatusCard(t, gormDB,
			fmt.Sprintf("https://example.com/cap%d", i),
			"Title", "", "", "",
			now,
		)
	}

	// limit=999 exceeds max of 20, falls back to default 10
	c, w := newTestContext(t, "/api/v1/trends/links", "limit=999")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 10)
}

func TestListLinks_InvalidLimitFallsBackToDefault(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 15 {
		createStatusCard(t, gormDB,
			fmt.Sprintf("https://example.com/inv%d", i),
			"Title", "", "", "",
			now,
		)
	}

	for _, invalidLimit := range []string{"abc", "0", "-1"} {
		c, w := newTestContext(t, "/api/v1/trends/links", "limit="+invalidLimit)
		ListLinks(c)
		require.Equal(t, http.StatusOK, w.Code)
		result := parseJSONArray(t, w)
		assert.Len(t, result, 10)
	}
}

func TestListLinks_Offset(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 10 {
		createStatusCard(t, gormDB,
			fmt.Sprintf("https://example.com/off%d", i),
			fmt.Sprintf("Title %d", i), "", "", "",
			now,
		)
	}

	c, w := newTestContext(t, "/api/v1/trends/links", "limit=3&offset=5")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 3)
}

func TestListLinks_InvalidOffsetIgnored(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 3 {
		createStatusCard(t, gormDB,
			fmt.Sprintf("https://example.com/io%d", i),
			"Title", "", "", "",
			now,
		)
	}

	c, w := newTestContext(t, "/api/v1/trends/links", "offset=xyz")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 3)
}

// =====================================================================
// ListLinks – Sorting and filtering
// =====================================================================

func TestListLinks_SortedBySharesCount(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	// Insert links with different share counts.
	// "popular" appears 3 times, "medium" 2 times, "rare" 1 time.
	for range 3 {
		createStatusCard(t, gormDB, "https://example.com/popular", "Popular", "", "", "", now)
	}
	for range 2 {
		createStatusCard(t, gormDB, "https://example.com/medium", "Medium", "", "", "", now)
	}
	createStatusCard(t, gormDB, "https://example.com/rare", "Rare", "", "", "", now)

	c, w := newTestContext(t, "/api/v1/trends/links", "")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 3)

	// Sorted by shares_count DESC
	assert.Equal(t, "https://example.com/popular", result[0]["url"])
	assert.Equal(t, "https://example.com/medium", result[1]["url"])
	assert.Equal(t, "https://example.com/rare", result[2]["url"])

	// Verify history uses count
	history, ok := result[0]["history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)
	entry, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "3", entry["uses"])
}

func TestListLinks_FiltersOldLinks(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	tenDaysAgo := now.AddDate(0, 0, -10)

	// Recent link should appear
	createStatusCard(t, gormDB, "https://example.com/recent", "Recent", "", "", "", now)
	// Old link (>7 days) should NOT appear
	createStatusCard(t, gormDB, "https://example.com/old", "Old", "", "", "", tenDaysAgo)

	c, w := newTestContext(t, "/api/v1/trends/links", "")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 1)
	assert.Equal(t, "https://example.com/recent", result[0]["url"])
}

func TestListLinks_EmptyResult(t *testing.T) {
	setupTestDB(t)

	c, w := newTestContext(t, "/api/v1/trends/links", "")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Empty(t, result)
}

func TestListLinks_ResponseStructure(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	createStatusCard(t, gormDB,
		"https://example.com/article",
		"Test Article",
		"A description",
		"https://example.com/img.png",
		"TestProvider",
		now,
	)

	c, w := newTestContext(t, "/api/v1/trends/links", "")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	require.Len(t, result, 1)

	link := result[0]
	assert.Equal(t, "https://example.com/article", link["url"])
	assert.Equal(t, "Test Article", link["title"])
	assert.Equal(t, "A description", link["description"])
	assert.Equal(t, "https://example.com/img.png", link["image"])
	assert.Equal(t, "TestProvider", link["provider_name"])
	assert.Nil(t, link["published_at"])

	history, ok := link["history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)

	entry, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "1", entry["uses"])
	assert.Equal(t, "1", entry["accounts"])
}

// =====================================================================
// ListLinks – Table-not-exist graceful handling
// =====================================================================

func TestListLinks_TableNotExistReturnsEmpty(t *testing.T) {
	gormDB := setupTestDB(t)

	// Drop the status_cards table to simulate it not existing.
	require.NoError(t, gormDB.Exec("DROP TABLE IF EXISTS status_cards").Error)

	c, w := newTestContext(t, "/api/v1/trends/links", "")
	ListLinks(c)

	// Should return 200 with empty array, not 500.
	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Empty(t, result)
}

// =====================================================================
// Limit edge cases (shared logic across handlers)
// =====================================================================

func TestListTags_LimitExactly20(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 20 {
		require.NoError(t, gormDB.Create(makeTag(fmt.Sprintf("t%d", i), uint64(20-i), 1, now)).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/tags", "limit=20")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 20, "limit=20 is the max allowed value")
}

func TestListTags_Limit1(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 5 {
		require.NoError(t, gormDB.Create(makeTag(fmt.Sprintf("t%d", i), uint64(5-i), 1, now)).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/tags", "limit=1")
	c.Request.Host = "example.com"

	ListTags(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 1)
}

func TestListStatuses_LimitExactly20(t *testing.T) {
	gormDB := setupTestDB(t)

	account := makeAccount(1, "limit20")
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := range 20 {
		require.NoError(t, gormDB.Create(makeStatus(uint64(i+1), 1, "x", uint64(20-i), 0, now)).Error)
	}

	c, w := newTestContext(t, "/api/v1/trends/statuses", "limit=20")
	ListStatuses(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 20)
}

func TestListLinks_LimitExactly20(t *testing.T) {
	gormDB := setupTestDB(t)

	now := time.Now()
	for i := range 20 {
		createStatusCard(t, gormDB,
			fmt.Sprintf("https://example.com/ex20_%d", i),
			"Title", "", "", "",
			now,
		)
	}

	c, w := newTestContext(t, "/api/v1/trends/links", "limit=20")
	ListLinks(c)

	require.Equal(t, http.StatusOK, w.Code)
	result := parseJSONArray(t, w)
	assert.Len(t, result, 20)
}
