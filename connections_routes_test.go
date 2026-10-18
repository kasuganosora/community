package community

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/controllers/api/connections"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newConnectionsFixture(t *testing.T) (discoveryRouteFixture, string, string) {
	f := newDiscoveryRouteFixture(t)
	require.NoError(t, f.conn.AutoMigrate(&models.PersonalAccessToken{}, &models.UserWebhook{}, &models.UserWebhookDelivery{}))
	web := func(account models.Account) string {
		row, token := models.NewAuthToken(account.UserId, models.AuthTokenTypeWeb, 0, 0, nil, 3600, config.C.EncryptedKey)
		require.NoError(t, f.conn.Create(row).Error)
		return token
	}
	return f, web(f.owner), web(f.other)
}

func TestPersonalTokensScopeIsolationAndRevocation(t *testing.T) {
	f, owner, other := newConnectionsFixture(t)
	path := "/api/v1/settings/tokens"
	require.Equal(t, 401, f.request("GET", path, "", "").Code)
	oauth := f.token(t, f.owner, models.AppScopeRead, models.AppScopeWrite)
	require.Equal(t, 403, f.request("POST", path, oauth, `{"name":"escalation","scopes":["write"]}`).Code)
	require.Equal(t, 422, f.request("POST", path, owner, `{"name":"admin","scopes":["admin"],"expires_in_days":30}`).Code)
	created := f.request("POST", path, owner, `{"name":"test reader","scopes":["read"],"expires_in_days":30}`)
	require.Equal(t, 201, created.Code, created.Body.String())
	require.Equal(t, "no-store", created.Header().Get("Cache-Control"))
	var output struct {
		Token string                     `json:"token"`
		Item  models.PersonalAccessToken `json:"item"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &output))
	require.NotEmpty(t, output.Token)
	data, err := models.GetAuthTokenData(output.Token, config.C.EncryptedKey)
	require.NoError(t, err)
	require.Zero(t, data.Permissions)
	require.Zero(t, data.Scopes.AdminScope)
	require.Zero(t, data.Scopes.WriteScope)
	require.Equal(t, uint64(models.AppScopeRead), data.Scopes.ReadScope)
	require.WithinDuration(t, time.Now().Add(30*24*time.Hour), *output.Item.ExpiresAt, 5*time.Second)
	list := f.request("GET", path, owner, "")
	require.Equal(t, 200, list.Code)
	require.NotContains(t, list.Body.String(), output.Token)
	require.NotContains(t, list.Body.String(), "token_hash")
	require.JSONEq(t, `[]`, f.request("GET", path, other, "").Body.String())
	require.Equal(t, 404, f.request("DELETE", path+"/"+output.Item.ID, other, "").Code)
	require.Equal(t, 403, f.request("GET", path, output.Token, "").Code)
	require.Equal(t, 403, f.request("PATCH", "/api/v1/accounts/update_credentials", output.Token, `{"display_name":"unauthorized mutation"}`).Code)
	require.Equal(t, 403, f.request("POST", "/api/v1/accounts/1/block", output.Token, "{}").Code)
	require.Equal(t, 204, f.request("DELETE", path+"/"+output.Item.ID, owner, "").Code)
	require.Equal(t, 401, f.request("GET", "/api/v1/accounts/verify_credentials", output.Token, "").Code)
}

func TestPersonalWebhooksOwnerValidationAndSecretLifecycle(t *testing.T) {
	f, owner, other := newConnectionsFixture(t)
	path := "/api/v1/settings/webhooks"
	for _, url := range []string{"http://example.com", "https://127.0.0.1/hook", "https://localhost/hook", "https://192.168.1.1", "https://user:secret@example.com"} {
		body, _ := json.Marshal(map[string]any{"name": "test", "url": url, "events": []string{"note"}, "enabled": true})
		require.Equal(t, 422, f.request("POST", path, owner, string(body)).Code)
	}
	created := f.request("POST", path, owner, `{"name":"My integration","url":"https://receiver.example/hook","events":["note","reply","reaction"],"enabled":true}`)
	require.Equal(t, 201, created.Code, created.Body.String())
	var output struct {
		Item   models.UserWebhook `json:"item"`
		Secret string             `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &output))
	require.Len(t, output.Secret, 64)
	require.NotContains(t, f.request("GET", path, owner, "").Body.String(), output.Secret)
	require.JSONEq(t, `[]`, f.request("GET", path, other, "").Body.String())
	idpath := path + "/" + output.Item.ID
	for _, suffix := range []string{"", "/test", "/deliveries"} {
		method := "DELETE"
		if suffix == "/test" {
			method = "POST"
		}
		if suffix == "/deliveries" {
			method = "GET"
		}
		require.Equal(t, 404, f.request(method, idpath+suffix, other, "").Code)
	}
	require.Equal(t, 202, f.request("POST", idpath+"/test", owner, "").Code)
	require.Equal(t, 422, f.request("POST", idpath+"/test", owner, "").Code)
	changed := f.request("PUT", idpath, owner, `{"name":"paused","url":"https://receiver.example/hook","secret":"","events":["note"],"enabled":false}`)
	require.Equal(t, 200, changed.Code, changed.Body.String())
	require.NotContains(t, changed.Body.String(), output.Secret)
	var stored models.UserWebhook
	require.NoError(t, f.conn.First(&stored, "id = ?", output.Item.ID).Error)
	require.Equal(t, output.Secret, stored.Secret)
	require.False(t, stored.Enabled)
	var delivery models.UserWebhookDelivery
	require.NoError(t, f.conn.First(&delivery, "webhook_id = ?", stored.ID).Error)
	require.Equal(t, "cancelled", delivery.State)
	require.Empty(t, delivery.Payload)
	require.Equal(t, 204, f.request("DELETE", idpath, owner, "").Code)
	var count int64
	require.NoError(t, f.conn.Model(&models.UserWebhookDelivery{}).Count(&count).Error)
	require.Zero(t, count)
}

// Authentication can finish before deletion is requested. Integration writes
// must re-check the locked user row instead of trusting that stale context.
func TestIntegrationWritesRejectAccountDisabledAfterAuthentication(t *testing.T) {
	f, _, _ := newConnectionsFixture(t)
	require.NoError(t, f.conn.Model(&models.User{}).Where("id = ?", f.owner.UserId).Updates(map[string]any{"is_ban": true, "status": models.UserStatusRejected}).Error)
	for _, test := range []struct {
		handler gin.HandlerFunc
		body    string
	}{
		{connections.CreateToken, `{"name":"late token","scopes":["read"],"expires_in_days":30}`},
		{connections.SaveWebhook, `{"name":"late hook","url":"https://receiver.example/hook","events":["note"],"enabled":true}`},
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest("POST", "/", strings.NewReader(test.body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Set("account", &f.owner)
		test.handler(ctx)
		require.Equal(t, 404, response.Code, response.Body.String())
	}
	for _, model := range []any{&models.PersonalAccessToken{}, &models.UserWebhook{}} {
		var count int64
		require.NoError(t, f.conn.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
}
