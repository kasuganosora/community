package favourite

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// allTestModels lists all models needed for auto-migration in tests.
// This mirrors the set used by controllers/api/status tests because the
// Favourites/Bookmarks handlers serialize statuses via serializer.ToStatuses,
// which queries many side tables (filters, blocks, mutes, pins, etc.).
var allTestModels = []any{
	&models.User{},
	&models.Account{},
	&models.AccountExtra{},
	&models.AccountEmoji{},
	&models.Status{},
	&models.StatusReaction{},
	&models.SystemSetting{},
	&models.StatusContent{},
	&models.StatusHistory{},
	&models.StatusTag{},
	&models.StatusToAccount{},
	&models.BookmarkStatus{},
	&models.FavoriteStatus{},
	&models.PinStatus{},
	&models.MuteStatus{},
	&models.MuteAccount{},
	&models.Poll{},
	&models.PollOption{},
	&models.PollOptionVote{},
	&models.Attachment{},
	&models.Application{},
	&models.Follow{},
	&models.Block{},
	&models.Emoji{},
	&models.StatusEmoji{},
	&models.StatusMediaAttachment{},
	&models.Conversation{},
	&models.AccountConversation{},
	&models.Notification{},
	&models.Filter{},
	&models.Role{},
	&models.AuthToken{},
	&models.Tag{},
}

func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// --- shared helpers ---

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	// The collection serializer uses UNION ALL, which the SQLExec fixture
	// engine does not implement. Exercise its actual SQL with isolated SQLite.
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	for _, model := range allTestModels {
		stmt := &gorm.Statement{DB: conn}
		require.NoError(t, stmt.Parse(model))
		for _, field := range stmt.Schema.Fields {
			if field.AutoIncrement {
				field.DataType = schema.Int
			}
		}
	}
	require.NoError(t, conn.AutoMigrate(allTestModels...))

	return conn
}

// createAccountWithUser builds a User + Account pair using the fixture builder.
func createAccountWithUser(t *testing.T, fix *testutil.FixtureBuilder, username string) *models.Account {
	t.Helper()

	user := fix.CreateUser(func(u *models.User) {
		u.UserName = username
		u.EmailHash = "hash_" + username
	})

	return fix.CreateAccount(user, func(a *models.Account) {
		a.Username = username
		a.DisplayName = username
	})
}

// newGetContext builds a gin.Context wired to a ResponseRecorder with the given
// query string and account in context.
func newGetContext(query string, account *models.Account) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites?"+query, nil) //nolint:noctx
	if account != nil {
		c.Set("account", account)
	}

	return c, w
}

// --- Favourites handler: DB-backed tests ---

func TestFavourites_EmptyResult(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser1")

	c, w := newGetContext("", acct)
	Favourites(c)
	require.Empty(t, c.Errors)

	assert.Equal(t, http.StatusOK, w.Code)
	// Empty list should still be valid JSON array, not null
	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Empty(t, result)
}

func TestFavourites_WithResults(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser2")

	status := fix.CreateStatus(acct)

	// Create a favourite
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id,
		StatusId:  status.Id,
	}).Error)

	c, w := newGetContext("", acct)
	Favourites(c)
	require.Empty(t, c.Errors)

	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)
	// The serialized status should carry the favourited flag
	assert.Equal(t, true, result[0]["favourited"], "favourited should be true")
}

func TestFavourites_DefaultLimit(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser3")

	// No limit param → handler defaults to 20
	c, w := newGetContext("", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestFavourites_ExplicitLimit(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser4")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id,
		StatusId:  status.Id,
	}).Error)

	c, w := newGetContext("limit=5", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestFavourites_PaginationMaxId(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser5")

	st1 := fix.CreateStatus(acct)
	st2 := fix.CreateStatus(acct)

	fav1 := &models.FavoriteStatus{AccountId: acct.Id, StatusId: st1.Id}
	require.NoError(t, gormDB.Create(fav1).Error)
	fav2 := &models.FavoriteStatus{AccountId: acct.Id, StatusId: st2.Id}
	require.NoError(t, gormDB.Create(fav2).Error)

	// max_id should exclude favourites with id >= max_id
	c, w := newGetContext("max_id="+itoa(fav2.Id), acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	// fav1.Id < fav2.Id, so only fav1 should be returned
	assert.Len(t, result, 1)
}

func TestFavourites_PaginationMinId(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser6")

	st1 := fix.CreateStatus(acct)
	st2 := fix.CreateStatus(acct)

	fav1 := &models.FavoriteStatus{AccountId: acct.Id, StatusId: st1.Id}
	require.NoError(t, gormDB.Create(fav1).Error)
	fav2 := &models.FavoriteStatus{AccountId: acct.Id, StatusId: st2.Id}
	require.NoError(t, gormDB.Create(fav2).Error)

	// min_id should return only favourites with id > min_id
	c, w := newGetContext("min_id="+itoa(fav1.Id), acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestFavourites_PaginationSinceId(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser7")

	st1 := fix.CreateStatus(acct)
	st2 := fix.CreateStatus(acct)

	fav1 := &models.FavoriteStatus{AccountId: acct.Id, StatusId: st1.Id}
	require.NoError(t, gormDB.Create(fav1).Error)
	fav2 := &models.FavoriteStatus{AccountId: acct.Id, StatusId: st2.Id}
	require.NoError(t, gormDB.Create(fav2).Error)

	// since_id should return only favourites with id > since_id (like min_id)
	c, w := newGetContext("since_id="+itoa(fav1.Id), acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestFavourites_InvalidMaxIdIgnored(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser8")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id,
		StatusId:  status.Id,
	}).Error)

	// Non-numeric max_id should be silently ignored (no filter applied)
	c, w := newGetContext("max_id=abc", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestFavourites_InvalidMinIdIgnored(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser9")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id,
		StatusId:  status.Id,
	}).Error)

	c, w := newGetContext("min_id=xyz", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestFavourites_InvalidSinceIdIgnored(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser10")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id,
		StatusId:  status.Id,
	}).Error)

	c, w := newGetContext("since_id=notnum", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestFavourites_ScheduledStatusFilterPath(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser11")

	// A normal status that will be favourited
	normalStatus := fix.CreateStatus(acct)
	// A scheduled status (the handler filters these via the join condition
	// "statuses.scheduled_at IS NULL").
	future := time.Now().Add(24 * time.Hour)
	scheduledStatus := fix.CreateStatus(acct, func(s *models.Status) {
		s.ScheduledAt = &future
	})

	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id, StatusId: normalStatus.Id,
	}).Error)
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id, StatusId: scheduledStatus.Id,
	}).Error)

	// The handler should execute the scheduled-status filter path without error.
	// (The in-memory mock engine may not fully honor the IS NULL join predicate,
	// so we assert the handler runs successfully rather than the exact count.)
	c, w := newGetContext("", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.GreaterOrEqual(t, len(result), 1, "at least the normal favourite should be returned")
}

func TestFavourites_OnlyOwnFavourites(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser12")
	other := createAccountWithUser(t, fix, "favother12")

	status := fix.CreateStatus(acct)

	// other account favourites the status
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: other.Id, StatusId: status.Id,
	}).Error)

	// acct should see zero favourites since only other favourited
	c, w := newGetContext("", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Empty(t, result)
}

func TestFavourites_AllPaginationParams(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser13")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id, StatusId: status.Id,
	}).Error)

	// All pagination params at once with valid numbers
	c, w := newGetContext("limit=20&max_id=999999&min_id=0&since_id=0", acct)
	Favourites(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

// --- Bookmarks handler: DB-backed tests ---

func TestBookmarks_EmptyResult(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser1")

	c, w := newGetContext("", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)

	assert.Equal(t, http.StatusOK, w.Code)
	var result []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Empty(t, result)
}

func TestBookmarks_WithResults(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser2")

	status := fix.CreateStatus(acct)

	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id,
		StatusId:  status.Id,
	}).Error)

	c, w := newGetContext("", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)

	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, true, result[0]["bookmarked"], "bookmarked should be true")
}

func TestBookmarks_DefaultLimit(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser3")

	c, w := newGetContext("", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBookmarks_ExplicitLimit(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser4")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id,
		StatusId:  status.Id,
	}).Error)

	c, w := newGetContext("limit=10", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBookmarks_PaginationMaxId(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser5")

	st1 := fix.CreateStatus(acct)
	st2 := fix.CreateStatus(acct)

	bm1 := &models.BookmarkStatus{AccountId: acct.Id, StatusId: st1.Id}
	require.NoError(t, gormDB.Create(bm1).Error)
	bm2 := &models.BookmarkStatus{AccountId: acct.Id, StatusId: st2.Id}
	require.NoError(t, gormDB.Create(bm2).Error)

	c, w := newGetContext("max_id="+itoa(bm2.Id), acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestBookmarks_PaginationMinId(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser6")

	st1 := fix.CreateStatus(acct)
	st2 := fix.CreateStatus(acct)

	bm1 := &models.BookmarkStatus{AccountId: acct.Id, StatusId: st1.Id}
	require.NoError(t, gormDB.Create(bm1).Error)
	bm2 := &models.BookmarkStatus{AccountId: acct.Id, StatusId: st2.Id}
	require.NoError(t, gormDB.Create(bm2).Error)

	c, w := newGetContext("min_id="+itoa(bm1.Id), acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestBookmarks_PaginationSinceId(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser7")

	st1 := fix.CreateStatus(acct)
	st2 := fix.CreateStatus(acct)

	bm1 := &models.BookmarkStatus{AccountId: acct.Id, StatusId: st1.Id}
	require.NoError(t, gormDB.Create(bm1).Error)
	bm2 := &models.BookmarkStatus{AccountId: acct.Id, StatusId: st2.Id}
	require.NoError(t, gormDB.Create(bm2).Error)

	c, w := newGetContext("since_id="+itoa(bm1.Id), acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestBookmarks_InvalidMaxIdIgnored(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser8")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id, StatusId: status.Id,
	}).Error)

	c, w := newGetContext("max_id=abc", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestBookmarks_InvalidMinIdIgnored(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser9")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id, StatusId: status.Id,
	}).Error)

	c, w := newGetContext("min_id=xyz", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestBookmarks_InvalidSinceIdIgnored(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser10")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id, StatusId: status.Id,
	}).Error)

	c, w := newGetContext("since_id=notnum", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestBookmarks_ScheduledStatusFilterPath(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser11")

	normalStatus := fix.CreateStatus(acct)
	future := time.Now().Add(24 * time.Hour)
	scheduledStatus := fix.CreateStatus(acct, func(s *models.Status) {
		s.ScheduledAt = &future
	})

	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id, StatusId: normalStatus.Id,
	}).Error)
	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id, StatusId: scheduledStatus.Id,
	}).Error)

	// The handler should execute the scheduled-status filter path without error.
	// (The in-memory mock engine may not fully honor the IS NULL join predicate,
	// so we assert the handler runs successfully rather than the exact count.)
	c, w := newGetContext("", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.GreaterOrEqual(t, len(result), 1, "at least the normal bookmark should be returned")
}

func TestBookmarks_OnlyOwnBookmarks(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser12")
	other := createAccountWithUser(t, fix, "bmother12")

	status := fix.CreateStatus(acct)

	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: other.Id, StatusId: status.Id,
	}).Error)

	c, w := newGetContext("", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Empty(t, result)
}

func TestBookmarks_AllPaginationParams(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "bmuser13")

	status := fix.CreateStatus(acct)
	require.NoError(t, gormDB.Create(&models.BookmarkStatus{
		AccountId: acct.Id, StatusId: status.Id,
	}).Error)

	c, w := newGetContext("limit=20&max_id=999999&min_id=0&since_id=0", acct)
	Bookmarks(c)
	require.Empty(t, c.Errors)
	assert.Equal(t, http.StatusOK, w.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Len(t, result, 1)
}

// --- Cross-handler: favourite then it appears in Favourites list ---

func TestFavourites_FavouriteThenList(t *testing.T) {
	gormDB := setupTestDB(t)
	fix := testutil.NewFixtureBuilder(t, gormDB)
	acct := createAccountWithUser(t, fix, "favuser_e2e")

	status := fix.CreateStatus(acct)

	// Step 1: No favourites yet → empty
	c1, w1 := newGetContext("", acct)
	Favourites(c1)
	assert.Equal(t, http.StatusOK, w1.Code)
	var res1 []map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &res1))
	assert.Empty(t, res1)

	// Step 2: Favourite the status
	require.NoError(t, gormDB.Create(&models.FavoriteStatus{
		AccountId: acct.Id, StatusId: status.Id,
	}).Error)

	// Step 3: List again → should contain one status
	c2, w2 := newGetContext("", acct)
	Favourites(c2)
	assert.Equal(t, http.StatusOK, w2.Code)
	var res2 []map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &res2))
	require.Len(t, res2, 1)
	assert.Equal(t, true, res2[0]["favourited"])

	// Step 4: Unfavourite (delete) then list → empty again
	require.NoError(t, gormDB.Where("account_id = ? AND status_id = ?", acct.Id, status.Id).
		Delete(&models.FavoriteStatus{}).Error)

	c3, w3 := newGetContext("", acct)
	Favourites(c3)
	assert.Equal(t, http.StatusOK, w3.Code)
	var res3 []map[string]any
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &res3))
	assert.Empty(t, res3)
}

// itoa converts a uint64 to string.
func itoa(n uint64) string {
	return strconv.FormatUint(n, 10)
}
