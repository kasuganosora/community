package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestMain sets up the test environment before all tests run.
// It enables test mode (so db.GetDB() does not try to connect to a real
// database) and switches gin into test mode.
func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// allTestModels lists all models needed for auto-migration in the admin tests.
var allTestModels = []any{
	&models.User{},
	&models.Account{},
	&models.AccountExtra{},
	&models.AccountEmoji{},
	&models.Emoji{},
	&models.Status{},
	&models.StatusContent{},
	&models.StatusHistory{},
	&models.StatusTag{},
	&models.StatusToAccount{},
	&models.StatusEmoji{},
	&models.StatusMediaAttachment{},
	&models.BookmarkStatus{},
	&models.FavoriteStatus{},
	&models.PinStatus{},
	&models.MuteStatus{},
	&models.MuteAccount{},
	&models.Report{},
	&models.Attachment{},
	&models.MediaAttachment{},
	&models.Application{},
	&models.Follow{},
	&models.Block{},
	&models.Filter{},
	&models.Role{},
	&models.AuthToken{},
	&models.Notification{},
	&models.Conversation{},
	&models.AccountConversation{},
	&models.PollOptionVote{},
	&models.Poll{},
	&models.PollOption{},
	&models.Tag{},
	&models.Rule{},
	&models.SystemSetting{},
	&models.Thumbnail{},
	&models.ThumbnailConfig{},
	&models.Relay{},
	&activitypub.Delivery{},
}

// adminTestEnv bundles the resources shared by every test in this package.
type adminTestEnv struct {
	gdb *gorm.DB
	fix *testutil.FixtureBuilder
}

// newAdminTestEnv creates a fresh isolated test database, sets it as the
// global GORM handle (so the production handlers that call db.GetDB() use it),
// migrates the required models and returns a fixture builder.
func newAdminTestEnv(t *testing.T) *adminTestEnv {
	t.Helper()

	s := testutil.NewTestSetup(t, testutil.WithModels(allTestModels...))
	database.SetTestDB(s.GormDB())

	t.Cleanup(func() {
		database.ResetTestDB()
	})

	return &adminTestEnv{
		gdb: s.GormDB(),
		fix: s.Fixtures,
	}
}

// ---------- gin context helpers ----------

// newJSONContext builds a gin.Context with the given params and a JSON body.
func newJSONContext(t *testing.T, method string, params gin.Params, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()

	var reader *bytes.Reader

	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader([]byte("{}"))
	}

	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", reader) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	if params != nil {
		c.Params = params
	}

	return c, w
}

// newGetContext builds a gin.Context for a GET request with the given params
// and query string.
func newGetContext(t *testing.T, params gin.Params, query string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	url := "/"
	if query != "" {
		url += "?" + query
	}

	c.Request = httptest.NewRequest(http.MethodGet, url, nil) //nolint:noctx

	if params != nil {
		c.Params = params
	}

	return c, w
}

// newDeleteContext builds a gin.Context for a DELETE request with params.
func newDeleteContext(t *testing.T, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil) //nolint:noctx

	if params != nil {
		c.Params = params
	}

	return c, w
}

// ---------- fixture helpers ----------

// createLocalAccount creates a local user+account pair and returns it.
func createLocalAccount(t *testing.T, env *adminTestEnv, username string) *models.Account {
	t.Helper()
	now := time.Now()
	user := &models.User{
		UUID:      username + "-uuid",
		UserName:  username,
		EmailHash: "hash_" + username,
		Status:    models.UserStatusNormal,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, env.gdb.Create(user).Error)

	account := &models.Account{
		UserId:      user.Id,
		Username:    username,
		DisplayName: username,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	require.NoError(t, env.gdb.Create(account).Error)

	return account
}

// createEmojiRow inserts an emoji directly into the DB.
func createEmojiRow(t *testing.T, env *adminTestEnv, shortcode, domain string) *models.Emoji {
	t.Helper()
	emoji := &models.Emoji{
		Shortcode:       shortcode,
		Url:             "https://example.com/" + shortcode + ".png",
		StaticUrl:       "https://example.com/" + shortcode + ".png",
		VisibleInPicker: true,
		Category:        "test",
		Domain:          domain,
	}
	require.NoError(t, env.gdb.Create(emoji).Error)

	return emoji
}

// createStatusRow inserts a status (with content) into the DB.
func createStatusRow(t *testing.T, env *adminTestEnv, account *models.Account) *models.Status {
	t.Helper()
	now := time.Now()
	status := &models.Status{
		AccountId:  account.Id,
		Visibility: uint8(models.StatusVisibilityPublic),
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}

	require.NoError(t, env.gdb.Create(status).Error)

	content := &models.StatusContent{
		StatusId:    status.Id,
		Content:     "hello",
		SpoilerText: "",
	}
	require.NoError(t, env.gdb.Create(content).Error)

	return status
}

// createThumbnailRow inserts a thumbnail row directly.
func createThumbnailRow(t *testing.T, env *adminTestEnv, status string) *models.Thumbnail {
	t.Helper()
	now := time.Now()
	thumb := &models.Thumbnail{
		OriginalURL:       "https://example.com/img-" + status + ".png",
		OriginalURLHash:   "hash-" + status + "-" + now.Format("150405.000000"),
		OriginalMediaType: "image",
		Status:            status,
		CreatedAt:         now,
		UpdatedAt:         now,
		ThumbnailSize:     1024,
	}
	require.NoError(t, env.gdb.Create(thumb).Error)

	return thumb
}

// createThumbnailConfigRow inserts a thumbnail config row.
func createThumbnailConfigRow(t *testing.T, env *adminTestEnv, key, value string) {
	t.Helper()
	cfg := &models.ThumbnailConfig{
		Key:   key,
		Value: value,
	}
	require.NoError(t, env.gdb.Create(cfg).Error)
}

// uitoa converts a uint64 to a string (avoids importing strconv everywhere).
func uitoa(n uint64) string {
	return strconv.FormatUint(n, 10)
}

// parseTimeRFC3339 parses an RFC3339 time and fails the test on error.
func parseTimeRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return tt
}

// ginContextWithBody builds a gin.Context with a raw (possibly invalid) JSON body.
func ginContextWithBody(t *testing.T, method string, params gin.Params, body string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, "/", bytes.NewReader([]byte(body))) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	if params != nil {
		c.Params = params
	}

	return c
}
