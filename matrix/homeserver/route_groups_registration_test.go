package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// TestAllRouteGroupsRegisterCleanly mirrors the Matrix registration order in
// router.go with Matrix enabled. gin panics on duplicate method+path pairs; a
// panic here means a whole route group would fail to mount at startup.
func TestAllRouteGroupsRegisterCleanly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)

	r := gin.New()
	groups := []struct {
		name string
		fn   func(gin.IRouter)
	}{
		{"Routes", RegisterRoutes},
		{"FederationRoutes", RegisterFederationRoutes},
		{"BackfillRoutes", RegisterBackfillRoutes},
		{"AdditionalFederationRoutes", RegisterAdditionalFederationRoutes},
		{"FederationE2EERoutes", RegisterFederationE2EERoutes},
		{"RelationsRoutes", RegisterRelationsRoutes},
		{"PushRoutes", RegisterPushRoutes},
		{"SpacesRoutes", RegisterSpacesRoutes},
		{"AppServiceRoutes", RegisterAppServiceRoutes},
		{"RoomUpgradeRoutes", RegisterRoomUpgradeRoutes},
		{"KeySharingRoutes", RegisterKeySharingRoutes},
		{"DeviceListRoutes", RegisterDeviceListRoutes},
		{"BatchSendRoutes", RegisterBatchSendRoutes},
		{"AsyncMediaRoutes", RegisterAsyncMediaRoutes},
		{"AccountRoutes", RegisterAccountRoutes},
	}
	for _, g := range groups {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("Register%s panicked: %v", g.name, p)
				}
			}()
			g.fn(r)
		}()
	}

	seen := map[string]bool{}
	for _, rt := range r.Routes() {
		seen[rt.Method+" "+rt.Path] = true
	}
	for _, want := range []string{
		"GET /_matrix/client/v3/account/whoami",
		"POST /_matrix/client/v3/account/password",
		"POST /_matrix/client/v3/account/password/email/requestToken",
		"POST /_matrix/client/v3/account/deactivate",
		"GET /_matrix/client/v3/user/:userId/account_data/:type",
		"PUT /_matrix/client/v3/user/:userId/account_data/:type",
		"GET /_matrix/client/v3/user/:userId/rooms/:roomId/account_data/:type",
		"PUT /_matrix/client/v3/user/:userId/rooms/:roomId/account_data/:type",
	} {
		if !seen[want] {
			t.Errorf("route not mounted: %s", want)
		}
	}
}

// TestRequestTokenMountedAndOpaque checks the requestToken route is reachable
// on the full Matrix router and does not reveal unknown addresses.
func TestRequestTokenMountedAndOpaque(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&models.Account{}, &PasswordResetToken{}))
	restore := resetGlobalHS(fullHS("test.local"))
	t.Cleanup(restore)

	r := gin.New()
	RegisterRoutes(r)
	RegisterAccountRoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/account/password/email/requestToken",
		strings.NewReader(`{"email":"nobody-route-test@example.invalid","client_secret":"x","send_attempt":1}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body struct{ Sid string }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.NotEmpty(t, body.Sid)

	var n int64
	require.NoError(t, db.GetDB().Model(&PasswordResetToken{}).Count(&n).Error)
	require.Zero(t, n, "unknown address must not create a reset row")
}
