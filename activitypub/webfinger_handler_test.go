package activitypub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupWebfingerDB creates a test DB and injects it into the global db.GetDB(),
// so that WebFinger handler (which uses db.GetDB()) works during the test.
// It also resets the domain setting to "localhost" for deterministic results.
func setupWebfingerDB(t *testing.T) *testutil.TestDB {
	t.Helper()
	testDB := testutil.QuickDBSetup(t,
		&models.Account{},
		&models.AccountExtra{},
		&models.SystemSetting{},
	)
	database.SetTestDB(testDB.GormDB)
	t.Cleanup(database.ResetTestDB)
	// Explicitly set domain to localhost for deterministic test behavior.
	// systemsettings caches globally, so we must override any value left by
	// previous tests.
	systemsettings.Set(systemsettings.KeyDomain, "localhost")
	return testDB
}

// newWebfingerRequest creates a gin test context with the given query params.
func newWebfingerRequest(t *testing.T, target, query string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, target+query, nil)
	if body != nil {
		c.Request = httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	}
	return c, w
}

func TestWebFingerMissingResource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupWebfingerDB(t)

	c, w := newWebfingerRequest(t, "/.well-known/webfinger", "", nil)
	WebFinger(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "resource parameter required")
}

func TestWebFingerInvalidFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupWebfingerDB(t)

	// Not an acct: resource
	c, w := newWebfingerRequest(t, "/.well-known/webfinger", "?resource=https://example.com", nil)
	WebFinger(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Malformed acct (no @)
	c2, w2 := newWebfingerRequest(t, "/.well-known/webfinger", "?resource=acct:useronly", nil)
	WebFinger(c2)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "invalid acct format")
}

func TestWebFingerDomainMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupWebfingerDB(t)

	// Domain does not match localhost (default test domain)
	c, w := newWebfingerRequest(t, "/.well-known/webfinger", "?resource=acct:alice@other.com", nil)
	WebFinger(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWebFingerUserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupWebfingerDB(t)

	// Insert a user so the table is valid
	account := &models.Account{Username: "realuser"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	// Query for a different user on the correct domain
	c, w := newWebfingerRequest(t, "/.well-known/webfinger", "?resource=acct:ghost@localhost", nil)
	WebFinger(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWebFingerSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupWebfingerDB(t)

	account := &models.Account{Username: "alice"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	c, w := newWebfingerRequest(t, "/.well-known/webfinger", "?resource=acct:alice@localhost", nil)
	WebFinger(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/jrd+json", w.Header().Get("Content-Type"))

	var resp WebFingerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "acct:alice@localhost", resp.Subject)
	assert.Contains(t, resp.Aliases, "https://localhost/users/alice")
	assert.Contains(t, resp.Aliases, "https://localhost/@alice")

	// Verify links
	var selfLink, profileLink, subscribeLink *WebFingerLink
	for i := range resp.Links {
		switch resp.Links[i].Rel {
		case "self":
			selfLink = &resp.Links[i]
		case "http://webfinger.net/rel/profile-page":
			profileLink = &resp.Links[i]
		case "http://ostatus.org/schema/1.0/subscribe":
			subscribeLink = &resp.Links[i]
		}
	}
	require.NotNil(t, selfLink)
	assert.Equal(t, "application/activity+json", selfLink.Type)
	assert.Equal(t, "https://localhost/users/alice", selfLink.Href)

	require.NotNil(t, profileLink)
	assert.Equal(t, "text/html", profileLink.Type)

	require.NotNil(t, subscribeLink)
	assert.Contains(t, subscribeLink.Href, "authorize_interaction")
}

func TestHostMeta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupWebfingerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/.well-known/host-meta", nil)

	HostMeta(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/xrd+xml")
	assert.Contains(t, w.Body.String(), "XRD")
	assert.Contains(t, w.Body.String(), "lrdd")
	assert.Contains(t, w.Body.String(), "localhost")
}

func TestNodeInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupWebfingerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/.well-known/nodeinfo", nil)

	NodeInfo(c)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	links, ok := resp["links"].([]any)
	require.True(t, ok)
	require.Len(t, links, 2)
	link := links[0].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "http://nodeinfo.diaspora.software/ns/schema/2.1", link["rel"])
	assert.Contains(t, link["href"], "/nodeinfo/2.1")
	link = links[1].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "http://nodeinfo.diaspora.software/ns/schema/2.0", link["rel"])
	assert.Contains(t, link["href"], "/nodeinfo/2.0")
}

func TestWebFingerWithCustomDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupWebfingerDB(t)

	// Set a custom domain via systemsettings
	systemsettings.Set(systemsettings.KeyDomain, "fedi.example.com")
	t.Cleanup(func() { systemsettings.Set(systemsettings.KeyDomain, "") })

	account := &models.Account{Username: "bob"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	c, w := newWebfingerRequest(t, "/.well-known/webfinger", "?resource=acct:bob@fedi.example.com", nil)
	WebFinger(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/jrd+json", w.Header().Get("Content-Type"))

	var resp WebFingerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "acct:bob@fedi.example.com", resp.Subject)
	assert.Contains(t, resp.Aliases, "https://fedi.example.com/users/bob")
}
