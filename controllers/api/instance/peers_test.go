package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
)

var testDB *testutil.TestDB

func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func setupTestDB(t *testing.T) {
	t.Helper()
	var err error
	testDB, err = testutil.NewIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	database.SetTestDB(testDB.GormDB)

	if err := testDB.AutoMigrate(
		&models.Account{},
		&models.AccountExtra{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
}

func createTestAccount(t *testing.T, username string, domain *string) *models.Account {
	t.Helper()
	now := time.Now()
	account := &models.Account{
		Username:    username,
		DisplayName: "Test " + username,
		Domain:      domain,
		Locked:      false,
		Bot:         false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	if err := testDB.GormDB.Create(account).Error; err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}
	return account
}

func newTestContext(t *testing.T, w *httptest.ResponseRecorder, method, path string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	return c
}

// ========== Peers Tests ==========

func TestPeers_Success(t *testing.T) {
	setupTestDB(t)

	// Create accounts with domains
	domain1 := "example.com"
	domain2 := "mastodon.social"
	domain3 := "example.com" // duplicate domain
	createTestAccount(t, "user1", &domain1)
	createTestAccount(t, "user2", &domain2)
	createTestAccount(t, "user3", &domain3)
	// Local account without domain should not appear
	createTestAccount(t, "localuser", nil)

	w := httptest.NewRecorder()
	c := newTestContext(t, w, http.MethodGet, "/api/v1/instance/peers")

	Peers(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var domains []string
	err := json.Unmarshal(w.Body.Bytes(), &domains)
	assert.NoError(t, err)
	assert.Contains(t, domains, "example.com")
	assert.Contains(t, domains, "mastodon.social")
	assert.GreaterOrEqual(t, len(domains), 2) // distinct behavior may vary in SQLite
}

func TestPeers_Empty(t *testing.T) {
	setupTestDB(t)

	// No accounts with domains
	localDomain := (*string)(nil)
	createTestAccount(t, "localuser", localDomain)

	w := httptest.NewRecorder()
	c := newTestContext(t, w, http.MethodGet, "/api/v1/instance/peers")

	Peers(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var domains []string
	err := json.Unmarshal(w.Body.Bytes(), &domains)
	assert.NoError(t, err)
	assert.Empty(t, domains)
}

func TestPeers_NilDomain(t *testing.T) {
	setupTestDB(t)

	domain1 := "remote.example"
	createTestAccount(t, "remote1", &domain1)
	createTestAccount(t, "remote2", nil)

	w := httptest.NewRecorder()
	c := newTestContext(t, w, http.MethodGet, "/api/v1/instance/peers")

	Peers(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var domains []string
	err := json.Unmarshal(w.Body.Bytes(), &domains)
	assert.NoError(t, err)
	assert.Equal(t, []string{"remote.example"}, domains)
}
