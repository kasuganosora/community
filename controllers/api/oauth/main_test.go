package oauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestMain sets up the test environment before all tests run.
// It enables test mode (so db.GetDB() does not try to connect to a real
// database) and switches gin into test mode.
func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// allTestModels lists all models needed for auto-migration in the oauth tests.
// We use glebarez/sqlite (pure-Go SQLite) instead of the sqlexec in-memory
// driver because the sqlexec driver double-encodes the custom
// ApplicationScope type, making it impossible to round-trip Application rows.
var allTestModels = []any{
	&models.User{},
	&models.Account{},
	&models.AccountExtra{},
	&models.AccountEmoji{},
	&models.ApplicationUserAuth{},
	&models.AuthToken{},
	&models.UserSecretData{},
	&OAuthApplication{},
	&OAuthAuthorizationCode{},
	&models.MisskeyAuthSession{},
	&models.PasskeyCredential{},
	&models.WebAuthnSession{},
}

// oauthTestEnv bundles the resources shared by every test in this package.
type oauthTestEnv struct {
	gdb *gorm.DB
	fix *testutil.FixtureBuilder
}

// newOAuthTestEnv creates a fresh in-memory SQLite database, sets it as the
// global GORM handle (so the production handlers that call db.GetDB() use it),
// migrates the required models and returns a fixture builder.
func newOAuthTestEnv(t *testing.T) *oauthTestEnv {
	t.Helper()

	// Use a unique DSN per call to avoid shared-cache test pollution.
	dsn := fmt.Sprintf("file:oauth_test_%d?mode=memory&cache=shared", time.Now().UnixNano())

	gormDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	err = gormDB.AutoMigrate(allTestModels...)
	require.NoError(t, err)

	database.SetTestDB(gormDB)

	// Ensure systemsettings.GetBool returns false for email verification
	// check, so the password grant path that depends on it is predictable.
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)

	t.Cleanup(func() {
		database.ResetTestDB()
	})

	return &oauthTestEnv{
		gdb: gormDB,
		fix: testutil.NewFixtureBuilder(t, gormDB),
	}
}

// setupTestDB creates a test DB and sets it as the global handle, but does
// not return the env (for tests that only need DB access and don't use
// fixtures directly).
func setupTestDB(t *testing.T) {
	t.Helper()
	_ = newOAuthTestEnv(t)
}

// ---------- gin context helpers ----------

// newJSONContext builds a gin.Context with the given method and a JSON body.
func newJSONContext(t *testing.T, method string, body any) (*gin.Context, *httptest.ResponseRecorder) {
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

	return c, w
}

// newFormContext builds a gin.Context with form-encoded data.
func newFormContext(t *testing.T, method string, form url.Values) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()

	var body *bytes.Reader
	if form != nil {
		body = bytes.NewReader([]byte(form.Encode()))
	} else {
		body = bytes.NewReader(nil)
	}

	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", body) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c, w
}

// newGETContext builds a gin.Context for a GET request with query parameters.
func newGETContext(t *testing.T, query url.Values) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()

	reqURL := "/"
	if query != nil {
		reqURL += "?" + query.Encode()
	}

	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, reqURL, nil) //nolint:noctx

	return c, w
}

// ---------- fixture helpers ----------

// createTestApp creates an OAuth application for testing and returns it.
func createTestApp(t *testing.T, env *oauthTestEnv, name, redirectURI string) *OAuthApplication {
	t.Helper()
	now := time.Now()

	app := &OAuthApplication{
		Application: models.Application{
			Name:        name,
			Website:     "https://example.com",
			RedirectUri: redirectURI,
			IsEnabled:   true,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		},
		ClientId:     "test-client-id-" + testutil.RandomString(8),
		ClientSecret: "test-client-secret-" + testutil.RandomString(8),
	}

	err := env.gdb.Create(app).Error
	require.NoError(t, err)

	return app
}

// createAuthCode creates an OAuth authorization code for testing.
func createAuthCode(t *testing.T, env *oauthTestEnv, appID, accountID uint64, redirectURI, scopes string, expired bool) *OAuthAuthorizationCode {
	t.Helper()

	var expiresAt *time.Time
	if expired {
		past := time.Now().Add(-1 * time.Hour)
		expiresAt = &past
	} else {
		future := time.Now().Add(10 * time.Minute)
		expiresAt = &future
	}

	code := &OAuthAuthorizationCode{
		Code:          "auth-code-" + testutil.RandomString(16),
		ApplicationId: appID,
		AccountId:     accountID,
		RedirectUri:   redirectURI,
		Scopes:        scopes,
		ExpiresAt:     expiresAt,
		Used:          false,
	}

	err := env.gdb.Create(code).Error
	require.NoError(t, err)

	return code
}

// createAccountInContext creates a user+account pair and returns the account
// pointer for use in the gin context ("account" key).
// Note: glebarez/sqlite does not auto-increment `bigint(20)` primary keys
// (SQLite only auto-increments INTEGER PRIMARY KEY), so we assign an explicit
// monotonically increasing Id to the account after creation.
func createAccountInContext(t *testing.T, env *oauthTestEnv) any {
	t.Helper()
	user := env.fix.CreateUser()
	account := env.fix.CreateAccount(user)
	// glebarez/sqlite leaves bigint(20) PKs at 0; assign an explicit id.
	if account.Id == 0 {
		var maxId uint64
		env.gdb.Model(&models.Account{}).Select("COALESCE(MAX(id), 0)").Row().Scan(&maxId)
		account.Id = maxId + 1
		env.gdb.Model(&models.Account{}).Where("user_id = ?", account.UserId).Update("id", account.Id)
	}
	return account
}
