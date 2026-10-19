package community

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type discoveryRouteFixture struct {
	app          *gin.Engine
	conn         *gorm.DB
	owner, other models.Account
}

func newDiscoveryRouteFixture(t *testing.T) discoveryRouteFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.UserSetting{}, &models.AuthToken{}, &models.SystemSetting{}, &models.RecommendationFeedback{}))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	oldKey, oldOther := config.C.EncryptedKey, config.C.Other
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	config.C.Other = maps.Clone(oldOther)
	if config.C.Other == nil {
		config.C.Other = map[string]string{}
	}
	config.C.Other[systemsettings.KeyRecommendationEnabled] = "false"
	config.C.Other[systemsettings.KeyDiscoveryTrendsEnabled] = "false"
	t.Cleanup(func() { config.C.EncryptedKey, config.C.Other = oldKey, oldOther })
	fixture := discoveryRouteFixture{conn: conn}
	for i, account := range []*models.Account{&fixture.owner, &fixture.other} {
		user := models.User{Id: uint64(8101 + i), UUID: fmt.Sprintf("discovery-router-%d", i), UserName: fmt.Sprintf("discovery_%d", i), EmailHash: fmt.Sprintf("discovery-router-email-%d", i)}
		require.NoError(t, conn.Create(&user).Error)
		*account = models.Account{Id: uint64(8201 + i), UserId: user.Id, Username: user.UserName}
		require.NoError(t, conn.Create(account).Error)
	}
	fixture.app = gin.New()
	SetRouter(fixture.app)
	return fixture
}

func (f discoveryRouteFixture) token(t *testing.T, account models.Account, read, write uint64) string {
	t.Helper()
	row, token := models.NewAuthToken(account.UserId, models.AuthTokenTypeOauth, 0, 0, &models.ApplicationScope{ReadScope: read, WriteScope: write}, 3600, config.C.EncryptedKey)
	require.NotEmpty(t, token)
	require.NoError(t, f.conn.Create(row).Error)
	return token
}

func (f discoveryRouteFixture) request(method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	f.app.ServeHTTP(response, request)
	return response
}

func TestDiscoveryRoutesOptionalAuthenticationRejectsInvalidTokens(t *testing.T) {
	f := newDiscoveryRouteFixture(t)
	for _, path := range []string{"/api/v1/trends/tags", "/api/v1/trends/statuses", "/api/v1/trends/links", "/api/v1/discovery/tags"} {
		t.Run(path, func(t *testing.T) {
			response := f.request(http.MethodGet, path, "", "")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			response = f.request(http.MethodGet, path, "invalid-token", "")
			require.Equal(t, http.StatusUnauthorized, response.Code, "invalid authentication must not fall back to anonymous")
		})
	}
	response := f.request(http.MethodGet, "/api/v1/discovery/statuses", "", "")
	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestDiscoveryRoutesOAuthScopesCoverCompatibilityAndNewEndpoints(t *testing.T) {
	f := newDiscoveryRouteFixture(t)
	readAccounts := f.token(t, f.owner, models.AppScopeReadAccounts, 0)
	noScope := f.token(t, f.owner, 0, 0)
	cases := []struct{ method, path, token, body string }{
		{http.MethodGet, "/api/v1/discovery/statuses", readAccounts, ""},
		{http.MethodGet, "/api/v1/discovery/preferences", noScope, ""},
		{http.MethodGet, "/api/v1/discovery/feedback", noScope, ""},
		{http.MethodPut, "/api/v1/discovery/preferences", readAccounts, `{"preference":"off"}`},
		{http.MethodPost, "/api/v1/discovery/feedback", readAccounts, `{"action":"hide_tag","target_kind":"tag","target_key":"test"}`},
		{http.MethodDelete, "/api/v1/discovery/feedback/1", readAccounts, ""},
		{http.MethodDelete, fmt.Sprintf("/api/v1/suggestions/%d", f.other.Id), readAccounts, ""},
		{http.MethodGet, "/api/v1/suggestions", noScope, ""},
		{http.MethodGet, "/api/v2/suggestions", noScope, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := f.request(tc.method, tc.path, tc.token, tc.body)
			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		})
	}
	for _, path := range []string{"/api/v1/suggestions", "/api/v2/suggestions"} {
		response := f.request(http.MethodGet, path, readAccounts, "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.JSONEq(t, `[]`, response.Body.String(), "disabled recommendation remains a compatible empty array")
	}
	var count int64
	require.NoError(t, f.conn.Model(&models.RecommendationFeedback{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDiscoveryRoutesFeedbackOwnerCannotBeSpoofedOrDeletedByAnotherUser(t *testing.T) {
	f := newDiscoveryRouteFixture(t)
	owner := f.token(t, f.owner, models.AppScopeReadAccounts, models.AppScopeWriteAccounts)
	other := f.token(t, f.other, models.AppScopeReadAccounts, models.AppScopeWriteAccounts)
	body := fmt.Sprintf(`{"action":"hide_tag","target_kind":"tag","target_key":"PrivateChoice","scope":"site","owner_id":%d}`, f.other.Id)
	response := f.request(http.MethodPost, "/api/v1/discovery/feedback", owner, body)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var rows []models.RecommendationFeedback
	require.NoError(t, f.conn.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "user", rows[0].Scope)
	require.Equal(t, f.owner.Id, rows[0].OwnerID)
	response = f.request(http.MethodGet, "/api/v1/discovery/feedback", other, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `[]`, response.Body.String())
	path := fmt.Sprintf("/api/v1/discovery/feedback/%d", rows[0].ID)
	response = f.request(http.MethodDelete, path, other, "")
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	response = f.request(http.MethodDelete, path, owner, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	require.EqualValues(t, 2, decoded["revision"], "both mutations invalidate this owner's ranking snapshots")
	var remaining int64
	require.NoError(t, f.conn.Model(&models.RecommendationFeedback{}).Count(&remaining).Error)
	require.Zero(t, remaining)
}

func TestDiscoveryFeedbackDisplaysLocalAndRemoteHandles(t *testing.T) {
	f := newDiscoveryRouteFixture(t)
	token := f.token(t, f.owner, models.AppScopeReadAccounts, models.AppScopeWriteAccounts)
	domain := "remote.example"
	remote := models.Account{Id: 8300, Username: "alice", DisplayName: "Not the username", Domain: &domain}
	require.NoError(t, f.conn.Create(&remote).Error)
	for _, row := range []models.RecommendationFeedback{
		{Scope: "user", OwnerID: f.owner.Id, Action: "hide_author", TargetKind: "account", TargetKey: fmt.Sprint(f.other.Id)},
		{Scope: "user", OwnerID: f.owner.Id, Action: "dismiss_suggestion", TargetKind: "account", TargetKey: fmt.Sprint(remote.Id)},
		{Scope: "user", OwnerID: f.owner.Id, Action: "hide_author", TargetKind: "account", TargetKey: "99999"},
		{Scope: "user", OwnerID: f.owner.Id, Action: "hide_status", TargetKind: "status", TargetKey: fmt.Sprint(remote.Id)},
	} {
		require.NoError(t, f.conn.Create(&row).Error)
	}
	response := f.request(http.MethodGet, "/api/v1/discovery/feedback", token, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	var rows []struct {
		ID    string `json:"id"`
		Kind  string `json:"target_kind"`
		Key   string `json:"target_key"`
		Label string `json:"target_label"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rows))
	require.Len(t, rows, 4)
	require.Empty(t, rows[0].Label, "a status ID must not accidentally resolve an account")
	require.Empty(t, rows[1].Label)
	require.Equal(t, "@alice@remote.example", rows[2].Label)
	require.Equal(t, "@"+f.other.Username, rows[3].Label)
	require.NoError(t, f.conn.Model(&remote).Update("username", "alice_new").Error)
	response = f.request(http.MethodGet, "/api/v1/discovery/feedback", token, "")
	require.Contains(t, response.Body.String(), "@alice_new@remote.example")
	response = f.request(http.MethodDelete, "/api/v1/discovery/feedback/"+rows[2].ID, token, "")
	require.Equal(t, 200, response.Code, response.Body.String())
}
