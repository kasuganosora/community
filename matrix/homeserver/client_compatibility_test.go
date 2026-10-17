package homeserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func TestLoadHSConfigExplicitBaseURLWithServerName(t *testing.T) {
	t.Setenv("SERVER_NAME", "matrix.example.org")
	oldURL, oldConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	t.Cleanup(func() { config.SetAppBaseURL(oldURL, oldConfigured) })
	config.SetAppBaseURL("http://localhost:8080", true)
	cfg := loadHSConfig()
	require.Equal(t, "matrix.example.org", cfg.ServerName)
	require.Equal(t, "http://localhost:8080", cfg.BaseURL)
	require.Equal(t, cfg.BaseURL, NewAuthService(&HomeServer{Config: cfg}).GetWellKnown().Homeserver.BaseURL)
}

func TestRegisterRejectsUsernameExceedingUserColumn(t *testing.T) {
	auth := NewAuthService(nil)
	_, err := auth.Register(&RegisterRequest{Username: strings.Repeat("a", 21), Password: "test-password"}, false)
	require.Equal(t, ErrInvalidUsername, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/register/available", handleUsernameAvailable)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/register/available?username="+strings.Repeat("a", 21), nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "M_INVALID_USERNAME")
}

func TestPushRulesTrailingSlashDoesNotRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Header("Access-Control-Allow-Origin", "*"); c.Next() })
	RegisterRoutes(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/pushrules/", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code, "must reach authentication instead of Gin's CORS-less redirect")
	require.Empty(t, w.Header().Get("Location"))
	require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestSyncReportsAvailableOneTimeKeysForCurrentDevice(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	hs.KeyStore = &KeyStore{hs: hs}
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixOneTimeKey{}, &MatrixToDeviceMessage{}))
	user := "@otk-count:test.local"
	records := []MatrixOneTimeKey{
		{UserID: user, DeviceID: "CURRENT", KeyID: "1", Algorithm: "signed_curve25519"},
		{UserID: user, DeviceID: "CURRENT", KeyID: "2", Algorithm: "signed_curve25519"},
		{UserID: user, DeviceID: "CURRENT", KeyID: "3", Algorithm: "signed_curve25519", Used: true},
		{UserID: user, DeviceID: "OTHER", KeyID: "1", Algorithm: "signed_curve25519"},
	}
	require.NoError(t, db.GetDB().Create(&records).Error)
	resp, err := hs.Sync.Sync(user, "CURRENT", &SyncRequest{})
	require.Nil(t, err)
	require.Equal(t, int64(2), resp.DeviceOneTimeKeysCount["signed_curve25519"])
	resp, err = hs.Sync.Sync(user, "EMPTY", &SyncRequest{Since: resp.NextBatch})
	require.Nil(t, err)
	require.Contains(t, resp.DeviceOneTimeKeysCount, "signed_curve25519")
	require.Zero(t, resp.DeviceOneTimeKeysCount["signed_curve25519"])
}

func TestSyncStopsLongPollWhenRequestCancelled(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	hs.SyncNotifier = NewSyncNotifier()
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixToDeviceMessage{}))
	restore := resetGlobalHS(hs)
	t.Cleanup(restore)
	r := gin.New()
	r.GET("/sync", func(c *gin.Context) {
		c.Set("user_id", "@cancel:test.local")
		c.Set("device_id", "CANCEL")
		handleSync(c)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/sync?since=s9999999999999&timeout=30000&set_presence=offline", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { r.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sync ignored request cancellation")
	}
}

func TestRegisterRejectsEmptyPassword(t *testing.T) {
	auth := NewAuthService(nil)
	for _, pw := range []string{""} {
		resp, err := auth.Register(&RegisterRequest{Username: "nopass", Password: pw}, false)
		require.Nil(t, resp)
		require.Equal(t, ErrMissingPassword, err)
	}
	resp, err := auth.createLocalUser("nopass", "", nil, nil, true, false, false)
	require.Nil(t, resp)
	require.Equal(t, ErrMissingPassword, err)

	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	r := gin.New()
	r.POST("/register", handleRegister)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/register",
		strings.NewReader(`{"username":"nopass","auth":{"type":"m.login.dummy"}}`)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "M_MISSING_PARAM")
}
