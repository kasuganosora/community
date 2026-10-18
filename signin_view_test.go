package community

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/migrate"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const hiddenText = "secret-signin-only-text"

type signinFixture struct {
	app                  *gin.Engine
	conn                 *gorm.DB
	hidden, open         models.Account
	hiddenPost, openPost models.Status
	boostOfHidden        models.Status
	viewerToken          string
}

func newSigninFixture(t *testing.T) signinFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "signin.db")), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	redisdb.SetRedisesForTest([]*goredis.Client{client})
	redisdb.SetUniversalRedis(client)
	t.Cleanup(func() { redisdb.SetRedisesForTest(nil); redisdb.SetUniversalRedis(nil) })
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	require.NoError(t, migrate.Up(conn))
	oldKey, oldOther := config.C.EncryptedKey, config.C.Other
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	config.C.Other = maps.Clone(oldOther)
	if config.C.Other == nil {
		config.C.Other = map[string]string{}
	}
	config.C.Other[systemsettings.KeyRecommendationEnabled] = "false"
	config.C.Other[systemsettings.KeyDiscoveryTrendsEnabled] = "false"
	t.Cleanup(func() { config.C.EncryptedKey, config.C.Other = oldKey, oldOther })

	f := signinFixture{conn: conn}
	accounts := []*models.Account{&f.hidden, &f.open, {}}
	for i, account := range accounts {
		user := models.User{Id: uint64(9101 + i), UUID: fmt.Sprintf("signin-%d", i), UserName: fmt.Sprintf("signin_%d", i), EmailHash: fmt.Sprintf("signin-email-%d", i)}
		require.NoError(t, conn.Create(&user).Error)
		*account = models.Account{Id: uint64(9201 + i), UserId: user.Id, Username: user.UserName, Acct: user.UserName, RequireSigninToView: i == 0}
		require.NoError(t, conn.Create(account).Error)
	}
	viewer := *accounts[2]
	f.hiddenPost = models.Status{AccountId: f.hidden.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: hiddenText}}
	f.openPost = models.Status{AccountId: f.open.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "open-text"}}
	require.NoError(t, conn.Create(&f.hiddenPost).Error)
	require.NoError(t, conn.Create(&f.openPost).Error)
	f.boostOfHidden = models.Status{AccountId: f.open.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &f.hiddenPost.Id}
	require.NoError(t, conn.Create(&f.boostOfHidden).Error)
	row, token := models.NewAuthToken(viewer.UserId, models.AuthTokenTypeOauth, 0, 0, &models.ApplicationScope{ReadScope: models.AppScopeRead}, 3600, config.C.EncryptedKey)
	require.NoError(t, conn.Create(row).Error)
	f.viewerToken = token
	f.app = gin.New()
	SetRouter(f.app)
	return f
}

func (f signinFixture) get(path, token string, header ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	response := httptest.NewRecorder()
	f.app.ServeHTTP(response, request)
	return response
}

func TestRequireSigninAPIForVisitorsAndSignedInUsers(t *testing.T) {
	f := newSigninFixture(t)
	refused := []string{
		fmt.Sprintf("/api/v1/accounts/%d/statuses", f.hidden.Id),
		fmt.Sprintf("/api/v1/statuses/%d", f.hiddenPost.Id),
		fmt.Sprintf("/api/v1/statuses/%d/context", f.hiddenPost.Id),
		fmt.Sprintf("/api/v1/statuses/%d/card", f.hiddenPost.Id),
		fmt.Sprintf("/api/v1/statuses/%d/reactions", f.hiddenPost.Id),
		fmt.Sprintf("/api/v1/statuses/%d", f.boostOfHidden.Id),
	}
	for _, path := range refused {
		t.Run("anonymous "+path, func(t *testing.T) {
			r := f.get(path, "")
			require.Equal(t, http.StatusUnauthorized, r.Code, r.Body.String())
			require.Contains(t, r.Body.String(), "SIGNIN_REQUIRED")
			require.NotContains(t, r.Body.String(), hiddenText)
		})
	}
	t.Run("anonymous profile stays visible", func(t *testing.T) {
		r := f.get(fmt.Sprintf("/api/v1/accounts/%d", f.hidden.Id), "")
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		require.Contains(t, r.Body.String(), `"require_signin_to_view":true`)
		require.Contains(t, r.Body.String(), f.hidden.Username)
	})
	t.Run("anonymous open account unaffected", func(t *testing.T) {
		r := f.get(fmt.Sprintf("/api/v1/accounts/%d/statuses", f.open.Id), "")
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		require.Contains(t, r.Body.String(), "open-text")
		require.NotContains(t, r.Body.String(), hiddenText, "a boost of a sign-in-only post is dropped")
		require.NotContains(t, r.Body.String(), `"require_signin_to_view"`, "the extension field is omitted when off")
	})
	for _, path := range []string{
		fmt.Sprintf("/api/v1/accounts/%d/statuses", f.hidden.Id),
		fmt.Sprintf("/api/v1/statuses/%d", f.hiddenPost.Id),
		fmt.Sprintf("/api/v1/statuses/%d", f.boostOfHidden.Id),
	} {
		t.Run("signed in "+path, func(t *testing.T) {
			r := f.get(path, f.viewerToken)
			require.Equal(t, http.StatusOK, r.Code, r.Body.String())
			require.Contains(t, r.Body.String(), hiddenText)
		})
	}
}

func TestRequireSigninFiltersVisitorListings(t *testing.T) {
	f := newSigninFixture(t)
	hiddenAccount := fmt.Sprintf(`{"id":"%d","username":"%s","acct":"%s","require_signin_to_view":true}`, f.hidden.Id, f.hidden.Username, f.hidden.Username)
	openAccount := fmt.Sprintf(`{"id":"%d","username":"%s","acct":"%s"}`, f.open.Id, f.open.Username, f.open.Username)
	hiddenStatus := `{"id":"1","content":"` + hiddenText + `","account":` + hiddenAccount + `}`
	openStatus := `{"id":"2","content":"open-text","account":` + openAccount + `}`
	boost := `{"id":"3","content":"","account":` + openAccount + `,"reblog":` + hiddenStatus + `}`
	quote := `{"id":"4","content":"q","account":` + openAccount + `,"quote":` + hiddenStatus + `}`
	bodies := map[string]string{
		"/api/v1/timelines/public":     "[" + hiddenStatus + "," + openStatus + "," + boost + "," + quote + "]",
		"/api/v1/timelines/tag/:tag":   "[" + hiddenStatus + "," + openStatus + "]",
		"/api/v1/trends/statuses":      "[" + hiddenStatus + "," + openStatus + "]",
		"/api/v2/search":               `{"accounts":[` + hiddenAccount + `],"statuses":[` + hiddenStatus + "," + openStatus + `],"hashtags":[]}`,
		"/api/v1/directory":            "[" + hiddenAccount + "," + openAccount + "]",
		"/api/v1/statuses/:id/context": `{"ancestors":[` + hiddenStatus + `],"descendants":[` + openStatus + `]}`,
	}
	app := gin.New()
	for path, body := range bodies {
		body := body
		app.GET(path, UGCVisitorGuard(), func(c *gin.Context) { c.Data(http.StatusOK, "application/json", []byte(body)) })
	}
	for path, body := range bodies {
		request := httptest.NewRequest(http.MethodGet, strings.NewReplacer(":tag", "x", ":id", "999999").Replace(path), nil)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, path)
		require.NotContains(t, response.Body.String(), hiddenText, path)
		if strings.Contains(body, "open-text") {
			require.Contains(t, response.Body.String(), "open-text", path)
		}
		if path == "/api/v1/directory" || path == "/api/v2/search" {
			require.Contains(t, response.Body.String(), f.hidden.Username, "accounts themselves stay listed (profile only)")
		}
	}
	// Signed-in requests are passed through unchanged.
	viewer := &models.Account{Id: 1}
	app2 := gin.New()
	app2.GET("/api/v1/timelines/public", func(c *gin.Context) { c.Set("account", viewer) }, UGCVisitorGuard(), func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", []byte(bodies["/api/v1/timelines/public"]))
	})
	response := httptest.NewRecorder()
	app2.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/timelines/public", nil))
	require.Contains(t, response.Body.String(), hiddenText)
}

func TestRequireSigninFederation(t *testing.T) {
	f := newSigninFixture(t)
	restore := activitypub.SetFetchSignatureVerifierForTest(func(c *gin.Context) error {
		if c.GetHeader("Signature") == "valid" {
			return nil
		}
		return errors.New("unsigned")
	})
	defer restore()
	ap := []string{"Accept", "application/activity+json"}
	user := "/users/" + f.hidden.Username
	gated := []string{
		user + "/outbox", user + "/outbox?page=true", user + "/followers", user + "/following", user + "/collections/featured",
		fmt.Sprintf("%s/statuses/%d", user, f.hiddenPost.Id),
	}
	for _, path := range gated {
		t.Run(path, func(t *testing.T) {
			r := f.get(path, "", ap...)
			require.Equal(t, http.StatusUnauthorized, r.Code, "unsigned fetch")
			require.NotContains(t, r.Body.String(), hiddenText)
			r = f.get(path, "", append(ap, "Signature", "forged")...)
			require.Equal(t, http.StatusUnauthorized, r.Code, "bad signature")
			r = f.get(path, "", append(ap, "Signature", "valid")...)
			require.Equal(t, http.StatusOK, r.Code, "signed remote server: %s", r.Body.String())
		})
	}
	t.Run("signed status carries content", func(t *testing.T) {
		r := f.get(fmt.Sprintf("%s/statuses/%d", user, f.hiddenPost.Id), "", append(ap, "Signature", "valid")...)
		require.Contains(t, r.Body.String(), hiddenText)
	})
	t.Run("actor document stays public", func(t *testing.T) {
		r := f.get(user, "", ap...)
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		require.Contains(t, r.Body.String(), "publicKey")
	})
	t.Run("open account unaffected", func(t *testing.T) {
		r := f.get("/users/"+f.open.Username+"/outbox?page=true", "", ap...)
		require.Equal(t, http.StatusOK, r.Code)
		require.Contains(t, r.Body.String(), "open-text")
	})
	restore()
	t.Run("real verifier rejects a forged signature", func(t *testing.T) {
		r := f.get(user+"/outbox", "", append(ap, "Signature", `keyId="https://evil.example/k",algorithm="rsa-sha256",headers="(request-target) host date",signature="AAAA"`)...)
		require.Equal(t, http.StatusUnauthorized, r.Code)
	})
}

func TestRequireSigninUpdateCredentials(t *testing.T) {
	f := newSigninFixture(t)
	row, token := models.NewAuthToken(f.open.UserId, models.AuthTokenTypeOauth, 0, 0, &models.ApplicationScope{ReadScope: models.AppScopeRead, WriteScope: models.AppScopeWrite}, 3600, config.C.EncryptedKey)
	require.NoError(t, f.conn.Create(row).Error)
	patch := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		f.app.ServeHTTP(response, request)
		return response
	}
	r := patch(`{"require_signin_to_view":true}`)
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"require_signin_to_view":true`)
	var stored models.Account
	require.NoError(t, f.conn.First(&stored, f.open.Id).Error)
	require.True(t, stored.RequireSigninToView)
	// A Mastodon client that never sends the field leaves it unchanged.
	r = patch(`{"display_name":"Renamed"}`)
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	require.NoError(t, f.conn.First(&stored, f.open.Id).Error)
	require.True(t, stored.RequireSigninToView)
	r = patch(`{"require_signin_to_view":false}`)
	require.Equal(t, http.StatusOK, r.Code)
	require.NoError(t, f.conn.First(&stored, f.open.Id).Error)
	require.False(t, stored.RequireSigninToView)
	require.NotContains(t, r.Body.String(), `"require_signin_to_view"`)
}
