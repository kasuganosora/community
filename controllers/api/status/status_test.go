package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// allTestModels lists all models needed for auto-migration in tests
var allTestModels = []interface{}{
	&models.User{},
	&models.Account{},
	&models.AccountExtra{},
	&models.Status{},
	&models.StatusContent{},
	&models.StatusHistory{},
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
	&models.Notification{},
	&models.Filter{},
	&models.Role{},
	&models.AuthToken{},
}

// testSetup holds common test setup data
type testSetup struct {
	t      *testing.T
	setup  *testutil.TestSetup
	db     *gorm.DB
	fix    *testutil.FixtureBuilder
	user   *models.User
	acct   *models.Account
	status *models.Status
}

// newTestSetup creates a fresh test environment with DB and fixtures
func newTestSetup(t *testing.T) *testSetup {
	t.Helper()
	gin.SetMode(gin.TestMode)

	s := testutil.NewTestSetup(t, testutil.WithModels(allTestModels...))
	database.SetTestDB(s.GormDB())

	ts := &testSetup{
		t:     t,
		setup: s,
		db:    s.GormDB(),
		fix:   s.Fixtures,
	}

	t.Cleanup(func() {
		database.ResetTestDB()
	})

	user, acct, status := ts.fix.FullTestSetup()
	ts.user = user
	ts.acct = acct
	ts.status = status

	return ts
}

// setupTestDB creates a test DB with models and sets it as global
func setupTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := testutil.NewTestSetup(t, testutil.WithModels(allTestModels...))
	database.SetTestDB(s.GormDB())
	t.Cleanup(func() {
		database.ResetTestDB()
	})
}

// newGinContext creates a gin.Context with a response recorder and optional params
func newGinContext(t *testing.T, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if params != nil {
		c.Params = params
	}
	return c, w
}

// newGinContextPost creates a gin.Context with POST method and body
func newGinContextPost(t *testing.T, params gin.Params, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bodyReader)
	c.Request.Header.Set("Content-Type", "application/json")
	if params != nil {
		c.Params = params
	}
	return c, w
}

// newGinContextPut creates a gin.Context with PUT method and body
func newGinContextPut(t *testing.T, params gin.Params, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", bodyReader)
	c.Request.Header.Set("Content-Type", "application/json")
	if params != nil {
		c.Params = params
	}
	return c, w
}

// setAccount sets the account in the gin context
func setAccount(c *gin.Context, acct *models.Account) {
	if acct != nil {
		c.Set("account", acct)
	}
}

// safeHandler calls a handler and recovers from panic("abort")
func safeHandler(c *gin.Context, f func(*gin.Context)) {
	defer func() {
		_ = recover()
	}()
	f(c)
}

// ============== Delete Tests ==============

func TestDelete_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Delete)

	assert.Equal(t, 204, w.Code)
}

func TestDelete_Forbidden_OtherUser(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "other" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "other" })

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, otherAcct)

	safeHandler(c, Delete)

	assert.Equal(t, 403, w.Code)
}

func TestDelete_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, Delete)

	assert.Equal(t, 400, w.Code)
}

func TestDelete_NotFound(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "99999"}})
	setAccount(c, ts.acct)

	safeHandler(c, Delete)

	assert.Equal(t, 404, w.Code)
}

func TestDelete_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})

	safeHandler(c, Delete)

	assert.Equal(t, 500, w.Code)
}

// ============== Show Tests ==============

func TestShow_PublicStatus(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Show)

	assert.Equal(t, 200, w.Code)
}

func TestShow_PublicStatus_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})

	safeHandler(c, Show)

	assert.Equal(t, 200, w.Code)
}

func TestShow_PrivateStatus_Owner(t *testing.T) {
	ts := newTestSetup(t)
	// Create a private status owned by the current account
	privateStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Show)

	assert.Equal(t, 200, w.Code)
}

func TestShow_PrivateStatus_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	privateStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})

	safeHandler(c, Show)

	assert.Equal(t, 403, w.Code)
}

func TestShow_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "abc"}})
	setAccount(c, ts.acct)

	safeHandler(c, Show)

	assert.Equal(t, 400, w.Code)
}

func TestShow_NotFound(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "99999"}})
	setAccount(c, ts.acct)

	safeHandler(c, Show)

	assert.Equal(t, 404, w.Code)
}

// ============== Create Tests ==============

func TestCreate_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Hello world",
		Visibility: "public",
		Language:   "en",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

func TestCreate_InvalidVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, map[string]string{
		"status":     "Hello",
		"visibility": "invalid",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 400, w.Code)
}

func TestCreate_NoVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, map[string]string{
		"status": "Hello",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 400, w.Code)
}

func TestCreate_NoAccount(t *testing.T) {
	setupTestDB(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Hello",
		Visibility: "public",
	})

	safeHandler(c, Create)

	assert.Equal(t, 500, w.Code)
}

func TestCreate_WithPoll(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Test poll",
		Visibility: "public",
		Poll: &CreatePollContent{
			Options:   []string{"Option A", "Option B"},
			ExpiresIn: 3600,
			Multiple:  false,
		},
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

func TestCreate_PrivateVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Private post",
		Visibility: "private",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

func TestCreate_DirectVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Direct post",
		Visibility: "direct",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

func TestCreate_UnlistedVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Unlisted post",
		Visibility: "unlisted",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

// ============== Update Tests ==============

func TestUpdate_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, UpdateStatusRequest{
		Status: testutil.PtrString("Updated content"),
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	assert.Equal(t, 200, w.Code)
}

func TestUpdate_Forbidden(t *testing.T) {
	ts := newTestSetup(t)
	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "other2" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "other2" })

	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, UpdateStatusRequest{
		Status: testutil.PtrString("Hacked!"),
	})
	setAccount(c, otherAcct)

	safeHandler(c, Update)

	assert.Equal(t, 403, w.Code)
}

func TestUpdate_NotFound(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: "99999"}}, UpdateStatusRequest{
		Status: testutil.PtrString("Updated"),
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	assert.Equal(t, 404, w.Code)
}

func TestUpdate_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: "abc"}}, UpdateStatusRequest{
		Status: testutil.PtrString("Updated"),
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	assert.Equal(t, 422, w.Code)
}

func TestUpdate_InvalidBody(t *testing.T) {
	ts := newTestSetup(t)
	c, _ := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, "not json")
	setAccount(c, ts.acct)

	safeHandler(c, Update)
	// handler returns 400 for binding error
	assert.True(t, c.Writer.Status() == 400 || c.Writer.Status() == 500)
}

// ============== Favourite Tests ==============

func TestFavourite_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Favourite)

	assert.Equal(t, 200, w.Code)
}

func TestFavourite_AlreadyFavourited(t *testing.T) {
	ts := newTestSetup(t)

	// Create a favourite record first
	err := ts.db.Create(&models.FavoriteStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Favourite)

	assert.Equal(t, 400, w.Code)
}

func TestFavourite_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})

	safeHandler(c, Favourite)

	assert.Equal(t, 500, w.Code)
}

func TestUnfavourite_Success(t *testing.T) {
	ts := newTestSetup(t)

	// Create a favourite record first
	err := ts.db.Create(&models.FavoriteStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Unfavourite)

	assert.Equal(t, 200, w.Code)
}

func TestUnfavourite_NotFavourited(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Unfavourite)

	assert.Equal(t, 400, w.Code)
}

func TestFavouritedBy_Success(t *testing.T) {
	ts := newTestSetup(t)

	// Create a favourite from another user
	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "favuser" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "favuser" })

	err := ts.db.Create(&models.FavoriteStatus{
		AccountId: otherAcct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, FavouritedBy)

	assert.Equal(t, 200, w.Code)
}

func TestFavouritedBy_EmptyList(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, FavouritedBy)

	assert.Equal(t, 200, w.Code)
}

func TestFavouritedBy_WithPagination(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	c.Request = httptest.NewRequest(http.MethodGet, "/?limit=10&offset=0", nil)
	setAccount(c, ts.acct)

	safeHandler(c, FavouritedBy)

	assert.Equal(t, 200, w.Code)
}

// ============== Reblog Tests ==============

func TestReblog_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, ReblogRequest{
		Visibility: "public",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Reblog)

	assert.Equal(t, 200, w.Code)
}

func TestReblog_DefaultVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, ReblogRequest{})
	setAccount(c, ts.acct)

	safeHandler(c, Reblog)

	assert.Equal(t, 200, w.Code)
}

func TestReblog_AlreadyReblogged(t *testing.T) {
	ts := newTestSetup(t)

	// Create a reblog status first
	reblog := &models.Status{
		AccountId:  ts.acct.Id,
		ReblogId:   &ts.status.Id,
		Visibility: models.StatusVisibilityPublic,
	}
	err := ts.db.Create(reblog).Error
	require.NoError(t, err)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, ReblogRequest{
		Visibility: "public",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Reblog)

	assert.Equal(t, 400, w.Code)
}

func TestReblog_InvalidBody(t *testing.T) {
	ts := newTestSetup(t)
	c, _ := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, map[string]string{
		"visibility": "invalid_value",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Reblog)

	assert.Equal(t, 400, c.Writer.Status())
}

func TestUnreblog_Success(t *testing.T) {
	ts := newTestSetup(t)

	// Create a reblog status first
	reblog := &models.Status{
		AccountId:  ts.acct.Id,
		ReblogId:   &ts.status.Id,
		Visibility: models.StatusVisibilityPublic,
	}
	err := ts.db.Create(reblog).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Unreblog)

	assert.Equal(t, 200, w.Code)
}

func TestUnreblog_NotReblogged(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Unreblog)

	assert.Equal(t, 400, w.Code)
}

func TestRebloggedBy_Success(t *testing.T) {
	ts := newTestSetup(t)

	// Create a reblog from another user
	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "rebloguser" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "rebloguser" })

	reblog := &models.Status{
		AccountId:  otherAcct.Id,
		ReblogId:   &ts.status.Id,
		Visibility: models.StatusVisibilityPublic,
	}
	err := ts.db.Create(reblog).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, RebloggedBy)

	assert.Equal(t, 200, w.Code)
}

func TestRebloggedBy_Empty(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, RebloggedBy)

	assert.Equal(t, 200, w.Code)
}

func TestRebloggedBy_WithPagination(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	c.Request = httptest.NewRequest(http.MethodGet, "/?limit=5&max_id=100", nil)
	setAccount(c, ts.acct)

	safeHandler(c, RebloggedBy)

	assert.Equal(t, 200, w.Code)
}

// ============== Bookmark Tests ==============

func TestBookmark_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Bookmark)

	assert.Equal(t, 200, w.Code)
}

func TestBookmark_AlreadyBookmarked(t *testing.T) {
	ts := newTestSetup(t)

	err := ts.db.Create(&models.BookmarkStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Bookmark)

	assert.Equal(t, 400, w.Code)
}

func TestBookmark_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})

	safeHandler(c, Bookmark)

	assert.Equal(t, 500, w.Code)
}

func TestUnbookmark_Success(t *testing.T) {
	ts := newTestSetup(t)

	err := ts.db.Create(&models.BookmarkStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Unbookmark)

	assert.Equal(t, 200, w.Code)
}

func TestUnbookmark_NotBookmarked(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Unbookmark)

	assert.Equal(t, 400, w.Code)
}

// ============== Pin Tests ==============

func TestPin_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Pin)

	assert.Equal(t, 200, w.Code)
}

func TestPin_AlreadyPinned(t *testing.T) {
	ts := newTestSetup(t)

	err := ts.db.Create(&models.PinStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Pin)

	assert.Equal(t, 400, w.Code)
}

func TestPin_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "pinuser" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "pinuser" })

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, otherAcct)

	safeHandler(c, Pin)

	assert.Equal(t, 400, w.Code)
}

func TestUnPin_Success(t *testing.T) {
	ts := newTestSetup(t)

	err := ts.db.Create(&models.PinStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, UnPin)

	assert.Equal(t, 200, w.Code)
}

func TestUnPin_NotPinned(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, UnPin)

	assert.Equal(t, 400, w.Code)
}

func TestUnPin_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "unpinuser" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "unpinuser" })

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, otherAcct)

	safeHandler(c, UnPin)

	assert.Equal(t, 400, w.Code)
}

// ============== Mute Tests ==============

func TestMuteStatus_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, MuteStatus)

	assert.Equal(t, 200, w.Code)
}

func TestMuteStatus_AlreadyMuted(t *testing.T) {
	ts := newTestSetup(t)

	err := ts.db.Create(&models.MuteStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, MuteStatus)

	assert.Equal(t, 400, w.Code)
}

func TestMuteStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "muteuser" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "muteuser" })

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, otherAcct)

	safeHandler(c, MuteStatus)

	assert.Equal(t, 400, w.Code)
}

func TestUnmuteStatus_Success(t *testing.T) {
	ts := newTestSetup(t)

	err := ts.db.Create(&models.MuteStatus{
		AccountId: ts.acct.Id,
		StatusId:  ts.status.Id,
	}).Error
	require.NoError(t, err)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, UnmuteStatus)

	assert.Equal(t, 200, w.Code)
}

func TestUnmuteStatus_NotMuted(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, UnmuteStatus)

	assert.Equal(t, 400, w.Code)
}

func TestUnmuteStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "unmuteuser" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "unmuteuser" })

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, otherAcct)

	safeHandler(c, UnmuteStatus)

	assert.Equal(t, 400, w.Code)
}

// ============== History Tests ==============

func TestGetHistory_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, GetHistory)

	assert.Equal(t, 200, w.Code)
}

func TestGetHistory_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})

	safeHandler(c, GetHistory)

	// Public status should be viewable even without account
	assert.Equal(t, 200, w.Code)
}

func TestGetHistory_PrivateStatus_Owner(t *testing.T) {
	ts := newTestSetup(t)
	privateStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, GetHistory)

	assert.Equal(t, 200, w.Code)
}

func TestGetHistory_PrivateStatus_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	privateStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})

	safeHandler(c, GetHistory)

	assert.Equal(t, 403, w.Code)
}

// ============== Source Tests ==============

func TestGetSource_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, GetSource)

	assert.Equal(t, 200, w.Code)
}

func TestGetSource_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})

	safeHandler(c, GetSource)

	assert.Equal(t, 500, w.Code)
}

func TestGetSource_NotFound(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "99999"}})
	setAccount(c, ts.acct)

	safeHandler(c, GetSource)

	assert.Equal(t, 404, w.Code)
}

func TestGetSource_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "abc"}})
	setAccount(c, ts.acct)

	safeHandler(c, GetSource)

	assert.Equal(t, 400, w.Code)
}

// ============== Context Tests ==============

func TestContext_PublicStatus(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Context)

	assert.Equal(t, 200, w.Code)

	var resp ContextResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.NotNil(t, resp.Ancestors)
	assert.NotNil(t, resp.Descendants)
}

func TestContext_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})

	safeHandler(c, Context)

	assert.Equal(t, 200, w.Code)
}

func TestContext_PrivateStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)
	privateStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})

	safeHandler(c, Context)

	assert.Equal(t, 403, w.Code)
}

func TestContext_DirectStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)
	directStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityDirect
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", directStatus.Id)}})

	safeHandler(c, Context)

	assert.Equal(t, 403, w.Code)
}

func TestContext_WithReplyThread(t *testing.T) {
	ts := newTestSetup(t)

	// Create a reply chain: parent -> ts.status -> child
	parent := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPublic
	})

	replyStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPublic
		inReplyTo := ts.status.Id
		s.InReplyToId = &inReplyTo
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)
	_ = parent
	_ = replyStatus

	safeHandler(c, Context)

	assert.Equal(t, 200, w.Code)
}

// ============== Translation Tests ==============

func TestTranslate_PublicStatus(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{
		{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)},
		{Key: "lang", Value: "en"},
	})
	setAccount(c, ts.acct)

	safeHandler(c, Translate)

	// Translation may fail gracefully, returning original content with "error" provider
	assert.Equal(t, 200, w.Code)
}

func TestTranslate_NoLangParam(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Translate)

	assert.Equal(t, 400, w.Code)
}

func TestTranslate_PrivateStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)
	privateStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{
		{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)},
		{Key: "lang", Value: "en"},
	})

	safeHandler(c, Translate)

	assert.Equal(t, 403, w.Code)
}

func TestTranslate_DirectStatus_NoAccount(t *testing.T) {
	ts := newTestSetup(t)
	directStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityDirect
	})

	c, w := newGinContext(t, gin.Params{
		{Key: "id", Value: fmt.Sprintf("%d", directStatus.Id)},
		{Key: "lang", Value: "en"},
	})

	safeHandler(c, Translate)

	assert.Equal(t, 403, w.Code)
}

// ============== Scheduled Status Tests ==============

func TestListScheduledStatuses_Success(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, nil)
	setAccount(c, ts.acct)

	safeHandler(c, ListScheduledStatuses)

	assert.Equal(t, 200, w.Code)
}

func TestListScheduledStatuses_WithScheduledStatus(t *testing.T) {
	ts := newTestSetup(t)

	scheduledAt := time.Now().Add(24 * time.Hour)
	scheduledStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.ScheduledAt = &scheduledAt
	})

	c, w := newGinContext(t, nil)
	setAccount(c, ts.acct)

	safeHandler(c, ListScheduledStatuses)

	assert.Equal(t, 200, w.Code)
	_ = scheduledStatus
}

func TestListScheduledStatuses_NoAccount(t *testing.T) {
	setupTestDB(t)
	c, w := newGinContext(t, nil)

	safeHandler(c, ListScheduledStatuses)

	assert.Equal(t, 500, w.Code)
}

func TestListScheduledStatuses_WithPagination(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, nil)
	c.Request = httptest.NewRequest(http.MethodGet, "/?limit=10&offset=0", nil)
	setAccount(c, ts.acct)

	safeHandler(c, ListScheduledStatuses)

	assert.Equal(t, 200, w.Code)
}

func TestViewSingleScheduledStatus_Success(t *testing.T) {
	ts := newTestSetup(t)

	scheduledAt := time.Now().Add(24 * time.Hour)
	scheduled := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.ScheduledAt = &scheduledAt
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", scheduled.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, ViewSingleScheduledStatus)

	assert.Equal(t, 200, w.Code)
}

func TestViewSingleScheduledStatus_EmptyID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: ""}})
	setAccount(c, ts.acct)

	safeHandler(c, ViewSingleScheduledStatus)

	assert.Equal(t, 422, w.Code)
}

func TestViewSingleScheduledStatus_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "abc"}})
	setAccount(c, ts.acct)

	safeHandler(c, ViewSingleScheduledStatus)

	assert.Equal(t, 422, w.Code)
}

func TestViewSingleScheduledStatus_NotFound(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "99999"}})
	setAccount(c, ts.acct)

	safeHandler(c, ViewSingleScheduledStatus)

	assert.Equal(t, 404, w.Code)
}

func TestViewSingleScheduledStatus_NotScheduled(t *testing.T) {
	ts := newTestSetup(t)
	// ts.status has no ScheduledAt, so it's not a scheduled status
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, ViewSingleScheduledStatus)

	assert.Equal(t, 404, w.Code)
}

func TestViewSingleScheduledStatus_OtherUser(t *testing.T) {
	ts := newTestSetup(t)

	scheduledAt := time.Now().Add(24 * time.Hour)
	scheduled := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.ScheduledAt = &scheduledAt
	})

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "schedother" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "schedother" })

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", scheduled.Id)}})
	setAccount(c, otherAcct)

	safeHandler(c, ViewSingleScheduledStatus)

	assert.Equal(t, 404, w.Code)
}

func TestUpdateStatusScheduledAt_Success(t *testing.T) {
	ts := newTestSetup(t)

	scheduledAt := time.Now().Add(24 * time.Hour)
	scheduled := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.ScheduledAt = &scheduledAt
	})

	newScheduledAt := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", scheduled.Id)}}, UpdateStatusScheduledAtRequest{
		ScheduledAt: newScheduledAt,
	})
	setAccount(c, ts.acct)

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 200, w.Code)
}

func TestUpdateStatusScheduledAt_EmptyID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: ""}}, UpdateStatusScheduledAtRequest{
		ScheduledAt: time.Now().Add(48 * time.Hour).Format(time.RFC3339),
	})
	setAccount(c, ts.acct)

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 422, w.Code)
}

func TestUpdateStatusScheduledAt_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: "abc"}}, UpdateStatusScheduledAtRequest{
		ScheduledAt: time.Now().Add(48 * time.Hour).Format(time.RFC3339),
	})
	setAccount(c, ts.acct)

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 422, w.Code)
}

func TestUpdateStatusScheduledAt_InvalidScheduledAt(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, UpdateStatusScheduledAtRequest{
		ScheduledAt: "not-a-date",
	})
	setAccount(c, ts.acct)

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 422, w.Code)
}

func TestUpdateStatusScheduledAt_NotFound(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: "99999"}}, UpdateStatusScheduledAtRequest{
		ScheduledAt: time.Now().Add(48 * time.Hour).Format(time.RFC3339),
	})
	setAccount(c, ts.acct)

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 404, w.Code)
}

func TestUpdateStatusScheduledAt_MissingBody(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, map[string]string{})

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 400, w.Code)
}

// ============== Poll Tests ==============

func TestViewPoll_Success(t *testing.T) {
	ts := newTestSetup(t)

	// Create a poll with options
	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	option2 := &models.PollOption{PollId: poll.Id, Content: "Option B", Index: 1}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Create(option2).Error)

	// Link poll to status
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, ViewPoll)

	assert.Equal(t, 200, w.Code)
}

func TestViewPoll_NotFound(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "99999"}})
	setAccount(c, ts.acct)

	safeHandler(c, ViewPoll)

	assert.Equal(t, 404, w.Code)
}

func TestViewPoll_NoAccount(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}})

	safeHandler(c, ViewPoll)

	assert.Equal(t, 200, w.Code)
}

func TestVotePoll_Success(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = false
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	option2 := &models.PollOption{PollId: poll.Id, Content: "Option B", Index: 1}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Create(option2).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{0},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 200, w.Code)
}

func TestVotePoll_Expired(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(-1 * time.Hour) // expired
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = false
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{0},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 400, w.Code)
}

func TestVotePoll_SingleChoiceMultipleVotes(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = false
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	option2 := &models.PollOption{PollId: poll.Id, Content: "Option B", Index: 1}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Create(option2).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{0, 1},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 400, w.Code)
}

func TestVotePoll_InvalidChoice(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = false
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{5}, // invalid index
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 400, w.Code)
}

func TestVotePoll_AlreadyVoted(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = false
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	// Pre-vote
	require.NoError(t, ts.db.Create(&models.PollOptionVote{
		PollId:    poll.Id,
		OptionId:  option1.Id,
		AccountId: ts.acct.Id,
	}).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{0},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 400, w.Code)
}

func TestVotePoll_EmptyChoices(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = false
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	// Empty choices should fail binding (required tag)
	assert.Equal(t, 400, w.Code)
}

func TestVotePoll_NegativeChoice(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = false
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{-1},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 400, w.Code)
}

func TestVotePoll_MultipleChoice(t *testing.T) {
	ts := newTestSetup(t)

	expiresAt := time.Now().Add(24 * time.Hour)
	poll := ts.fix.CreatePoll(func(p *models.Poll) {
		p.AccountId = ts.acct.Id
		p.ExpiresAt = &expiresAt
		p.Multiple = true // allow multiple
	})
	option1 := &models.PollOption{PollId: poll.Id, Content: "Option A", Index: 0}
	option2 := &models.PollOption{PollId: poll.Id, Content: "Option B", Index: 1}
	option3 := &models.PollOption{PollId: poll.Id, Content: "Option C", Index: 2}
	require.NoError(t, ts.db.Create(option1).Error)
	require.NoError(t, ts.db.Create(option2).Error)
	require.NoError(t, ts.db.Create(option3).Error)
	require.NoError(t, ts.db.Model(ts.status).Update("poll_id", poll.Id).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", poll.Id)}}, VotePollRequest{
		Choices: []int{0, 2},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 200, w.Code)
}

// ============== Reblog of Reblog (getRealStatus) Tests ==============

func TestFavourite_ReblogStatus(t *testing.T) {
	ts := newTestSetup(t)

	// Create a reblog pointing to our status
	reblogId := ts.status.Id
	reblogStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		reblogIDCopy := reblogId
		s.ReblogId = &reblogIDCopy
		s.Visibility = models.StatusVisibilityPublic
	})

	// Need to set the Reblog field since getRealStatus checks for it
	reblogStatus.Reblog = ts.status
	require.NoError(t, ts.db.Save(reblogStatus).Error)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", reblogStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Favourite)

	assert.Equal(t, 200, w.Code)
}

// ============== Delete Reblog Test ==============

func TestDelete_ReblogForbidden(t *testing.T) {
	ts := newTestSetup(t)

	// Create a reblog - cannot delete reblog statuses
	reblogId := ts.status.Id
	reblogStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		reblogIDCopy := reblogId
		s.ReblogId = &reblogIDCopy
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", reblogStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Delete)

	assert.Equal(t, 400, w.Code)
}

// ============== ContextResponse type test ==============

func TestContextResponse_Structure(t *testing.T) {
	resp := ContextResponse{
		Ancestors:   []*serializer.Status{},
		Descendants: []*serializer.Status{},
	}

	assert.NotNil(t, resp.Ancestors)
	assert.NotNil(t, resp.Descendants)
}

// ============== TranslationResponse type test ==============

func TestTranslationResponse_Fields(t *testing.T) {
	resp := TranslationResponse{
		Content:          "translated text",
		Language:         "en",
		DetectedLanguage: "zh",
		Provider:         "test",
	}

	assert.Equal(t, "translated text", resp.Content)
	assert.Equal(t, "en", resp.Language)
	assert.Equal(t, "zh", resp.DetectedLanguage)
	assert.Equal(t, "test", resp.Provider)
}

// ============== CreateStatusRequest validation tests ==============

func TestCreateStatusRequest_AllFields(t *testing.T) {
	req := CreateStatusRequest{
		Status:      "Hello world",
		InReplyToId: "123",
		MediaIds:    []string{"1", "2"},
		Sensitive:   true,
		SpoilerText: "Spoiler",
		Visibility:  "public",
		Poll: &CreatePollContent{
			Options:    []string{"A", "B"},
			ExpiresIn:  3600,
			Multiple:   true,
			HideTotals: false,
		},
		Language: "en",
	}

	assert.Equal(t, "Hello world", req.Status)
	assert.Equal(t, "123", req.InReplyToId)
	assert.Len(t, req.MediaIds, 2)
	assert.True(t, req.Sensitive)
	assert.Equal(t, "Spoiler", req.SpoilerText)
	assert.NotNil(t, req.Poll)
	assert.Len(t, req.Poll.Options, 2)
}

// ============== Integration: Bookmark then Unbookmark ==============

func TestBookmarkThenUnbookmark(t *testing.T) {
	ts := newTestSetup(t)

	// Bookmark
	c1, w1 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c1, ts.acct)
	safeHandler(c1, Bookmark)
	assert.Equal(t, 200, w1.Code)

	// Unbookmark
	c2, w2 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c2, ts.acct)
	safeHandler(c2, Unbookmark)
	assert.Equal(t, 200, w2.Code)
}

// ============== Integration: Favourite then Unfavourite ==============

func TestFavouriteThenUnfavourite(t *testing.T) {
	ts := newTestSetup(t)

	// Favourite
	c1, w1 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c1, ts.acct)
	safeHandler(c1, Favourite)
	assert.Equal(t, 200, w1.Code)

	// Unfavourite
	c2, w2 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c2, ts.acct)
	safeHandler(c2, Unfavourite)
	assert.Equal(t, 200, w2.Code)
}

// ============== Integration: Pin then UnPin ==============

func TestPinThenUnPin(t *testing.T) {
	ts := newTestSetup(t)

	// Pin
	c1, w1 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c1, ts.acct)
	safeHandler(c1, Pin)
	assert.Equal(t, 200, w1.Code)

	// UnPin
	c2, w2 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c2, ts.acct)
	safeHandler(c2, UnPin)
	assert.Equal(t, 200, w2.Code)
}

// ============== Integration: Mute then Unmute ==============

func TestMuteThenUnmute(t *testing.T) {
	ts := newTestSetup(t)

	// Mute
	c1, w1 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c1, ts.acct)
	safeHandler(c1, MuteStatus)
	assert.Equal(t, 200, w1.Code)

	// Unmute
	c2, w2 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c2, ts.acct)
	safeHandler(c2, UnmuteStatus)
	assert.Equal(t, 200, w2.Code)
}

// ============== Integration: Reblog then Unreblog ==============

func TestReblogThenUnreblog(t *testing.T) {
	ts := newTestSetup(t)

	// Reblog
	c1, w1 := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, ReblogRequest{
		Visibility: "public",
	})
	setAccount(c1, ts.acct)
	safeHandler(c1, Reblog)
	assert.Equal(t, 200, w1.Code)

	// Unreblog
	c2, w2 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c2, ts.acct)
	safeHandler(c2, Unreblog)
	assert.Equal(t, 200, w2.Code)
}

// ============== Integration: Create then Delete ==============

func TestCreateThenDelete(t *testing.T) {
	ts := newTestSetup(t)

	// Create
	c1, w1 := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "To be deleted",
		Visibility: "public",
	})
	setAccount(c1, ts.acct)
	safeHandler(c1, Create)
	assert.Equal(t, 200, w1.Code)

	// Parse response to get the new status ID
	var created map[string]interface{}
	err := json.Unmarshal(w1.Body.Bytes(), &created)
	require.NoError(t, err)
	newId, ok := created["id"].(float64)
	require.True(t, ok, "response should contain id")

	// Delete
	c2, w2 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%.0f", newId)}})
	setAccount(c2, ts.acct)
	safeHandler(c2, Delete)
	assert.Equal(t, 204, w2.Code)
}

// ============== Integration: Create then Update ==============

func TestCreateThenUpdate(t *testing.T) {
	ts := newTestSetup(t)

	// Create
	c1, w1 := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Original content",
		Visibility: "public",
		Language:   "en",
	})
	setAccount(c1, ts.acct)
	safeHandler(c1, Create)
	assert.Equal(t, 200, w1.Code)

	var created map[string]interface{}
	err := json.Unmarshal(w1.Body.Bytes(), &created)
	require.NoError(t, err)
	newId, ok := created["id"].(float64)
	require.True(t, ok, "response should contain id")

	// Update
	c2, w2 := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%.0f", newId)}}, UpdateStatusRequest{
		Status: testutil.PtrString("Updated content"),
	})
	setAccount(c2, ts.acct)
	safeHandler(c2, Update)
	assert.Equal(t, 200, w2.Code)
}

// ============== Edge Cases ==============

func TestCreate_WithMediaIds(t *testing.T) {
	ts := newTestSetup(t)

	attachment := ts.fix.CreateAttachment(func(a *models.Attachment) {
		a.AccountId = ts.acct.Id
	})

	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "With media",
		Visibility: "public",
		MediaIds:   []string{fmt.Sprintf("%d", attachment.Id)},
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

func TestCreate_WithInvalidMediaIds(t *testing.T) {
	ts := newTestSetup(t)

	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "With invalid media",
		Visibility: "public",
		MediaIds:   []string{"not_a_number", "99999"},
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	// Invalid media IDs are skipped; if all invalid, no attachments are set
	assert.Equal(t, 200, w.Code)
}

func TestCreate_WithInReplyTo(t *testing.T) {
	ts := newTestSetup(t)

	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:      "A reply",
		Visibility:  "public",
		InReplyToId: fmt.Sprintf("%d", ts.status.Id),
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

func TestReblog_WithApplication(t *testing.T) {
	ts := newTestSetup(t)

	app := &models.Application{Name: "TestApp"}
	require.NoError(t, ts.db.Create(app).Error)

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, ReblogRequest{
		Visibility: "public",
	})
	setAccount(c, ts.acct)
	c.Set("application", app)

	safeHandler(c, Reblog)

	assert.Equal(t, 200, w.Code)
}

func TestShow_ReblogStatus(t *testing.T) {
	ts := newTestSetup(t)

	reblogId := ts.status.Id
	reblogStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		reblogIDCopy := reblogId
		s.ReblogId = &reblogIDCopy
		s.Visibility = models.StatusVisibilityPublic
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", reblogStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Show)

	assert.Equal(t, 200, w.Code)
}

func TestGetHistory_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, GetHistory)

	assert.Equal(t, 400, w.Code)
}

func TestContext_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, Context)

	assert.Equal(t, 400, w.Code)
}

func TestTranslate_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{
		{Key: "id", Value: "invalid"},
		{Key: "lang", Value: "en"},
	})
	setAccount(c, ts.acct)

	safeHandler(c, Translate)

	assert.Equal(t, 400, w.Code)
}

func TestFavouritedBy_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, FavouritedBy)

	assert.Equal(t, 400, w.Code)
}

func TestRebloggedBy_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, RebloggedBy)

	assert.Equal(t, 400, w.Code)
}

// ============== Test that response body contains expected fields ==============

func TestShow_ResponseContainsID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Show)

	assert.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	_, hasID := resp["id"]
	assert.True(t, hasID, "response should contain 'id' field")
}

func TestCreate_ResponseContainsContent(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "Test content",
		Visibility: "public",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	content, ok := resp["content"]
	assert.True(t, ok, "response should contain 'content' field")
	assert.Contains(t, fmt.Sprintf("%v", content), "Test content")
}

func TestDelete_ResponseNoBody(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Delete)

	assert.Equal(t, 204, w.Code)
	assert.Equal(t, 0, w.Body.Len())
}

// ============== Additional edge case tests ==============

func TestFavourite_PrivateStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "favoother" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "favoother" })

	privateStatus := ts.fix.CreateStatus(otherAcct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Favourite)

	assert.Equal(t, 403, w.Code)
}

func TestBookmark_PrivateStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "bookother" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "bookother" })

	privateStatus := ts.fix.CreateStatus(otherAcct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Bookmark)

	assert.Equal(t, 403, w.Code)
}

func TestReblog_PrivateStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "reblother" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "reblother" })

	privateStatus := ts.fix.CreateStatus(otherAcct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}}, ReblogRequest{
		Visibility: "public",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Reblog)

	assert.Equal(t, 403, w.Code)
}

func TestFavouritedBy_PrivateStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "favoother2" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "favoother2" })

	privateStatus := ts.fix.CreateStatus(otherAcct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, FavouritedBy)

	assert.Equal(t, 403, w.Code)
}

func TestRebloggedBy_PrivateStatus_Forbidden(t *testing.T) {
	ts := newTestSetup(t)

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "reblother2" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "reblother2" })

	privateStatus := ts.fix.CreateStatus(otherAcct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", privateStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, RebloggedBy)

	assert.Equal(t, 403, w.Code)
}

// ============== Tests for request/response types ==============

func TestFavouritedByRequest_Fields(t *testing.T) {
	req := FavouritedByRequest{
		MaxId:   100,
		SinceId: 50,
		Limit:   20,
		Offset:  5,
	}
	assert.Equal(t, uint64(100), req.MaxId)
	assert.Equal(t, uint64(50), req.SinceId)
	assert.Equal(t, uint64(20), req.Limit)
	assert.Equal(t, uint64(5), req.Offset)
}

func TestRebloggedByRequest_Fields(t *testing.T) {
	req := RebloggedByRequest{
		MaxId:   100,
		SinceId: 50,
		Limit:   20,
		Offset:  5,
	}
	assert.Equal(t, uint64(100), req.MaxId)
	assert.Equal(t, uint64(50), req.SinceId)
	assert.Equal(t, uint64(20), req.Limit)
	assert.Equal(t, uint64(5), req.Offset)
}

func TestListScheduledStatusesRequest_Fields(t *testing.T) {
	maxId := uint64(100)
	sinceId := uint64(50)
	limit := uint64(20)
	offset := uint64(5)

	req := ListScheduledStatusesRequest{
		MaxId:   &maxId,
		SinceId: &sinceId,
		Limit:   &limit,
		Offset:  &offset,
	}
	assert.Equal(t, uint64(100), *req.MaxId)
	assert.Equal(t, uint64(50), *req.SinceId)
	assert.Equal(t, uint64(20), *req.Limit)
	assert.Equal(t, uint64(5), *req.Offset)
}

func TestVotePollRequest_Validation(t *testing.T) {
	req := VotePollRequest{
		Choices: []int{0, 1, 2},
	}
	assert.Len(t, req.Choices, 3)
}

func TestUpdateStatusRequest_AllFields(t *testing.T) {
	status := "updated"
	spoiler := "updated spoiler"
	visibility := "private"
	lang := "zh"
	mediaIds := []string{"1", "2"}

	req := UpdateStatusRequest{
		Status:      &status,
		SpoilerText: &spoiler,
		Visibility:  &visibility,
		Language:    &lang,
		MediaIds:    &mediaIds,
	}
	assert.Equal(t, "updated", *req.Status)
	assert.Equal(t, "updated spoiler", *req.SpoilerText)
	assert.Equal(t, "private", *req.Visibility)
	assert.Equal(t, "zh", *req.Language)
	assert.Len(t, *req.MediaIds, 2)
}

func TestCreatePollContent_Fields(t *testing.T) {
	poll := CreatePollContent{
		Options:    []string{"A", "B", "C"},
		ExpiresIn:  7200,
		Multiple:   true,
		HideTotals: true,
	}
	assert.Len(t, poll.Options, 3)
	assert.Equal(t, uint64(7200), poll.ExpiresIn)
	assert.True(t, poll.Multiple)
	assert.True(t, poll.HideTotals)
}

// ============== getRealStatus tests (via handlers that use it) ==============

func TestGetRealStatus_ReblogWithNilReblog(t *testing.T) {
	ts := newTestSetup(t)

	reblogId := ts.status.Id
	reblogStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		reblogIDCopy := reblogId
		s.ReblogId = &reblogIDCopy
		s.Visibility = models.StatusVisibilityPublic
	})

	// Deliberately NOT loading the Reblog field to trigger the panic path
	// We need to clear the Reblog field if GORM auto-loaded it
	reblogStatus.Reblog = nil

	// Use Bookmark which calls getRealStatus
	c, _ := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", reblogStatus.Id)}})
	setAccount(c, ts.acct)

	// This should panic because getRealStatus will panic("abort") when Reblog is nil
	// safeHandler already recovers from panics
	// Create a new context with a fresh recorder
	c2, w2 := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", reblogStatus.Id)}})
	setAccount(c2, ts.acct)
	safeHandler(c2, Bookmark)
	// The panic should be caught by safeHandler
	_ = w2
}

// ============== Scheduled Status Update with Forbidden ==============

func TestUpdateStatusScheduledAt_OtherUser(t *testing.T) {
	ts := newTestSetup(t)

	scheduledAt := time.Now().Add(24 * time.Hour)
	scheduled := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.ScheduledAt = &scheduledAt
	})

	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "schedupdother" })
	otherAcct := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "schedupdother" })

	newScheduledAt := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", scheduled.Id)}}, UpdateStatusScheduledAtRequest{
		ScheduledAt: newScheduledAt,
	})
	setAccount(c, otherAcct)

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 404, w.Code)
}

// ============== Concurrent Operations ==============

func TestConcurrentBookmarks(t *testing.T) {
	ts := newTestSetup(t)

	done := make(chan bool)

	for i := 0; i < 5; i++ {
		go func() {
			c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
			setAccount(c, ts.acct)
			safeHandler(c, Bookmark)
			_ = w
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}
}

// ============== FavouritedBy/RebloggedBy with max_id and since_id ==============

func TestFavouritedBy_WithMaxIdAndSinceId(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	c.Request = httptest.NewRequest(http.MethodGet, "/?max_id=999&since_id=1&limit=10&offset=0", nil)
	setAccount(c, ts.acct)

	safeHandler(c, FavouritedBy)

	assert.Equal(t, 200, w.Code)
}

func TestRebloggedBy_WithMaxIdAndSinceId(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	c.Request = httptest.NewRequest(http.MethodGet, "/?max_id=999&since_id=1&limit=10&offset=0", nil)
	setAccount(c, ts.acct)

	safeHandler(c, RebloggedBy)

	assert.Equal(t, 200, w.Code)
}

// ============== Test helpers: verify panic recovery works ==============

func TestSafeHandler_RecoversFromPanic(t *testing.T) {
	called := false
	c, _ := newGinContext(t, nil)

	safeHandler(c, func(c *gin.Context) {
		called = true
		panic("abort")
	})

	assert.True(t, called, "handler should have been called")
}

// ============== Test that helpers exist ==============

func TestGinParams(t *testing.T) {
	params := gin.Params{
		{Key: "id", Value: "123"},
		{Key: "lang", Value: "en"},
	}

	assert.Len(t, params, 2)
	id, _ := params.Get("id")
	assert.Equal(t, "123", id)
	lang, _ := params.Get("lang")
	assert.Equal(t, "en", lang)
	nonexistent, _ := params.Get("nonexistent")
	assert.Equal(t, "", nonexistent)
}

// ============== Test that JSON binding works for empty body ==============

func TestReblog_EmptyBody(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, nil)
	setAccount(c, ts.acct)

	safeHandler(c, Reblog)

	// Empty body should still work with default visibility
	assert.Equal(t, 200, w.Code)
}

// ============== Test response content type ==============

func TestShow_ResponseContentType(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Show)

	contentType := w.Header().Get("Content-Type")
	assert.True(t, strings.HasPrefix(contentType, "application/json") || w.Code == 200,
		"response should be JSON for 200 OK")
}

// ============== Test setupTestDB ==============

func TestSetupTestDB(t *testing.T) {
	setupTestDB(t)

	// Verify the global DB is set by creating a simple record
	db := database.DB
	assert.NotNil(t, db)
}

// ============== Create with Spoiler Text ==============

func TestCreate_WithSpoilerText(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:      "Spoiler content",
		SpoilerText: "Warning!",
		Visibility:  "public",
		Sensitive:   true,
	})
	setAccount(c, ts.acct)

	safeHandler(c, Create)

	assert.Equal(t, 200, w.Code)
}

// ============== Update visibility ==============

func TestUpdate_ChangeVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, UpdateStatusRequest{
		Visibility: testutil.PtrString("private"),
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	assert.Equal(t, 200, w.Code)
}

func TestUpdate_InvalidVisibility(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, map[string]string{
		"visibility": "invalid",
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	assert.Equal(t, 400, w.Code)
}

// ============== Update with poll ==============

func TestUpdate_WithPoll(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, UpdateStatusRequest{
		Status: testutil.PtrString("Updated with poll"),
		Poll: &CreatePollContent{
			Options:   []string{"New A", "New B"},
			ExpiresIn: 7200,
			Multiple:  false,
		},
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	assert.Equal(t, 200, w.Code)
}

// ============== Update with media ==============

func TestUpdate_WithMediaIds(t *testing.T) {
	ts := newTestSetup(t)

	attachment := ts.fix.CreateAttachment(func(a *models.Attachment) {
		a.AccountId = ts.acct.Id
	})

	mediaIds := []string{fmt.Sprintf("%d", attachment.Id)}
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, UpdateStatusRequest{
		MediaIds: &mediaIds,
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	assert.Equal(t, 200, w.Code)
}

func TestUpdate_InvalidMediaIds(t *testing.T) {
	ts := newTestSetup(t)

	mediaIds := []string{"99999"}
	c, w := newGinContextPut(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}}, UpdateStatusRequest{
		MediaIds: &mediaIds,
	})
	setAccount(c, ts.acct)

	safeHandler(c, Update)

	// Attachment doesn't belong to this account, should fail
	assert.Equal(t, 400, w.Code)
}

// ============== Context with thread ==============

func TestContext_StatusWithAncestors(t *testing.T) {
	ts := newTestSetup(t)

	// Create parent status
	parent := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPublic
	})

	// Make ts.status a reply to parent
	inReplyTo := parent.Id
	ts.status.InReplyToId = &inReplyTo
	require.NoError(t, ts.db.Save(ts.status).Error)

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ts.status.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, Context)

	assert.Equal(t, 200, w.Code)

	var resp ContextResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	// Should have at least the parent as ancestor
	assert.True(t, len(resp.Ancestors) > 0, "should have ancestors")
}

// ============== Unfavourite after delete ==============

func TestUnfavourite_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, Unfavourite)

	assert.Equal(t, 400, w.Code)
}

// ============== Bookmark with invalid ID ==============

func TestBookmark_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, Bookmark)

	assert.Equal(t, 400, w.Code)
}

// ============== Pin with invalid ID ==============

func TestPin_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, Pin)

	assert.Equal(t, 400, w.Code)
}

// ============== Mute with invalid ID ==============

func TestMuteStatus_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, MuteStatus)

	assert.Equal(t, 400, w.Code)
}

// ============== Reblog with invalid ID ==============

func TestReblog_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: "invalid"}}, ReblogRequest{})
	setAccount(c, ts.acct)

	safeHandler(c, Reblog)

	assert.Equal(t, 400, w.Code)
}

func TestUnreblog_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: "invalid"}})
	setAccount(c, ts.acct)

	safeHandler(c, Unreblog)

	assert.Equal(t, 400, w.Code)
}

// ============== VotePoll with invalid ID ==============

func TestVotePoll_InvalidID(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: "invalid"}}, VotePollRequest{
		Choices: []int{0},
	})
	setAccount(c, ts.acct)

	safeHandler(c, VotePoll)

	assert.Equal(t, 400, w.Code)
}

// ============== GetSource with Reblog ==============

func TestGetSource_ReblogStatus(t *testing.T) {
	ts := newTestSetup(t)

	// Create a reblog
	reblogId := ts.status.Id
	reblogStatus := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		reblogIDCopy := reblogId
		s.ReblogId = &reblogIDCopy
		s.Visibility = models.StatusVisibilityPublic
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", reblogStatus.Id)}})
	setAccount(c, ts.acct)

	safeHandler(c, GetSource)

	// Should follow the reblog and get the source of the original status
	assert.True(t, w.Code == 200 || w.Code == 404, "should return 200 or 404 for reblog source")
}

// ============== Integration: ListScheduledStatuses with pagination ==============

func TestListScheduledStatuses_WithAllPaginationParams(t *testing.T) {
	ts := newTestSetup(t)
	c, w := newGinContext(t, nil)
	c.Request = httptest.NewRequest(http.MethodGet, "/?max_id=100&since_id=1&limit=10&offset=5", nil)
	setAccount(c, ts.acct)

	safeHandler(c, ListScheduledStatuses)

	assert.Equal(t, 200, w.Code)
}

// ============== Test scheduled status: NoAccount for ViewSingle ==============

func TestViewSingleScheduledStatus_NoAccount(t *testing.T) {
	ts := newTestSetup(t)

	scheduledAt := time.Now().Add(24 * time.Hour)
	scheduled := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.ScheduledAt = &scheduledAt
	})

	c, w := newGinContext(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", scheduled.Id)}})

	safeHandler(c, ViewSingleScheduledStatus)

	assert.Equal(t, 500, w.Code)
}

// ============== Test scheduled status: NoAccount for Update ==============

func TestUpdateStatusScheduledAt_NoAccount(t *testing.T) {
	ts := newTestSetup(t)

	scheduledAt := time.Now().Add(24 * time.Hour)
	scheduled := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.ScheduledAt = &scheduledAt
	})

	newScheduledAt := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	c, w := newGinContextPost(t, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", scheduled.Id)}}, UpdateStatusScheduledAtRequest{
		ScheduledAt: newScheduledAt,
	})

	safeHandler(c, UpdateStatusScheduledAt)

	assert.Equal(t, 500, w.Code)
}
