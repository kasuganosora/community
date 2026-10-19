package community

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type createdToken struct {
	Token string                     `json:"token"`
	Item  models.PersonalAccessToken `json:"item"`
}

func createPAT(t *testing.T, f discoveryRouteFixture, web, body string, want int) createdToken {
	t.Helper()
	w := f.request("POST", "/api/v1/settings/tokens", web, body)
	require.Equal(t, want, w.Code, w.Body.String())
	var out createdToken
	if want == 201 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	}
	return out
}

func TestPersonalTokenGranularScopes(t *testing.T) {
	f, owner, _ := newConnectionsFixture(t)
	require.NoError(t, f.conn.AutoMigrate(&models.Role{}, &models.RoleUser{}))
	for _, body := range []string{
		`{"name":"x","scopes":[],"expires_in_days":0}`,
		`{"name":"x","scopes":["read:everything"],"expires_in_days":0}`,
		`{"name":"x","scopes":["admin"],"expires_in_days":0}`,
	} {
		createPAT(t, f, owner, body, 422)
	}
	tok := createPAT(t, f, owner, `{"name":"poster","scopes":["write:statuses","read:statuses","write:statuses"],"expires_in_days":0}`, 201)
	require.Equal(t, []string{"read:statuses", "write:statuses"}, tok.Item.Scopes, "deduped and sorted")
	data, err := models.GetAuthTokenData(tok.Token, config.C.EncryptedKey)
	require.NoError(t, err)
	require.Equal(t, uint64(models.AppScopeWriteStatuses), data.Scopes.WriteScope)
	require.Equal(t, uint64(models.AppScopeReadStatuses), data.Scopes.ReadScope)
	// Out-of-scope writes are 403, never 401.
	for _, wr := range [][2]string{{"PATCH", "/api/v1/accounts/update_credentials"}, {"POST", "/api/v1/accounts/1/block"}, {"POST", "/api/v1/lists"}, {"POST", "/api/v1/filters"}} {
		w := f.request(wr[0], wr[1], tok.Token, `{}`)
		require.Equal(t, 403, w.Code, "%s %s %s", wr[0], wr[1], w.Body.String())
	}
	// In-scope write passes the scope guard (handler may still reject the body).
	// Legacy ["read","write"] tokens keep full bits.
	legacy := createPAT(t, f, owner, `{"name":"legacy","scopes":["read","write"],"expires_in_days":0}`, 201)
	data, err = models.GetAuthTokenData(legacy.Token, config.C.EncryptedKey)
	require.NoError(t, err)
	require.Equal(t, uint64(models.AppScopeWrite), data.Scopes.WriteScope)
	require.Equal(t, uint64(models.AppScopeRead), data.Scopes.ReadScope)
}

func TestPersonalTokenAdminScopes(t *testing.T) {
	f, owner, _ := newConnectionsFixture(t)
	require.NoError(t, f.conn.AutoMigrate(&models.Role{}, &models.RoleUser{}))
	require.NoError(t, f.conn.AutoMigrate(&models.UserSecretData{}, &models.UserTwoFactor{}))
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	require.NoError(t, err)
	require.NoError(t, f.conn.Create(&models.UserSecretData{UserId: f.owner.UserId, PasswordScheme: "bcrypt", Password: string(hash)}).Error)
	body := `{"name":"mod","scopes":["admin:read:reports"],"expires_in_days":0}`
	createPAT(t, f, owner, body, 403) // not an admin
	require.NoError(t, f.conn.Model(&models.User{}).Where("id = ?", f.owner.UserId).Update("is_admin", true).Error)
	reader := createPAT(t, f, owner, body, 201)
	createPAT(t, f, owner, `{"name":"mod","scopes":["admin:write:reports"],"expires_in_days":0}`, 422)
	createPAT(t, f, owner, `{"name":"mod","scopes":["admin:write:reports"],"expires_in_days":0,"password":"wrong"}`, 422)
	createPAT(t, f, owner, `{"name":"mod","scopes":["admin:write:reports"],"expires_in_days":0,"password":"correct horse"}`, 201)
	require.NotEqual(t, 403, f.request("GET", "/api/v1/admin/reports/999999", reader.Token, "").Code)
	// Demoted: the same token loses admin access at request time.
	require.NoError(t, f.conn.Model(&models.User{}).Where("id = ?", f.owner.UserId).Update("is_admin", false).Error)
	require.Equal(t, 403, f.request("GET", "/api/v1/admin/reports/999999", reader.Token, "").Code)
}

func TestTokenLastUsedAndInactivityExpiry(t *testing.T) {
	f, owner, _ := newConnectionsFixture(t)
	require.NoError(t, f.conn.AutoMigrate(&models.Role{}, &models.RoleUser{}))
	tok := createPAT(t, f, owner, `{"name":"app","scopes":["read"],"expires_in_days":0}`, 201)
	stored := func() models.AuthToken {
		var row models.AuthToken
		require.NoError(t, f.conn.First(&row, "token = ?", tok.Item.TokenHash).Error)
		return row
	}
	var meta models.PersonalAccessToken
	require.NoError(t, f.conn.First(&meta, "id = ?", tok.Item.ID).Error)
	tok.Item.TokenHash = meta.TokenHash
	require.Nil(t, stored().LastUsedAt, "never used")

	w := f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	first := stored().LastUsedAt
	require.NotNil(t, first)
	// Throttled: a second request within the hour does not rewrite it.
	require.Equal(t, 200, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)
	require.True(t, first.Equal(*stored().LastUsedAt))
	// Older than an hour: refreshed.
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, f.conn.Model(&models.AuthToken{}).Where("token = ?", tok.Item.TokenHash).Update("last_used_at", old).Error)
	require.Equal(t, 200, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)
	require.True(t, stored().LastUsedAt.After(old.Add(time.Minute)))

	list := f.request("GET", "/api/v1/settings/tokens", owner, "")
	require.Contains(t, list.Body.String(), `"last_used_at":"`)

	// Idle for 200 days with the toggle off: still valid.
	idle := time.Now().Add(-200 * 24 * time.Hour)
	require.NoError(t, f.conn.Model(&models.AuthToken{}).Where("token = ?", tok.Item.TokenHash).Updates(map[string]any{"last_used_at": idle, "created_at": idle}).Error)
	require.Equal(t, 200, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)
	require.NoError(t, f.conn.Model(&models.AuthToken{}).Where("token = ?", tok.Item.TokenHash).Update("last_used_at", idle).Error)

	// Toggle on: rejected with 401, soft-revoked, last_used_at untouched.
	config.C.Other[systemsettings.KeyAPITokenInactivityExpiry] = "true"
	for range 3 {
		require.Equal(t, 401, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)
	}
	row := stored()
	require.NotNil(t, row.Revoke)
	require.Equal(t, models.RevokeReasonInactive, row.RevokeReason)
	require.WithinDuration(t, idle, *row.LastUsedAt, time.Second)
	require.Contains(t, f.request("GET", "/api/v1/settings/tokens", owner, "").Body.String(), `"revoked_reason":"inactive"`)
	// Stays invalid after the toggle is turned off again.
	config.C.Other[systemsettings.KeyAPITokenInactivityExpiry] = "false"
	require.Equal(t, 401, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)

	// A never-used token counts from created_at.
	config.C.Other[systemsettings.KeyAPITokenInactivityExpiry] = "true"
	fresh := createPAT(t, f, owner, `{"name":"fresh","scopes":["read"],"expires_in_days":0}`, 201)
	require.Equal(t, 200, f.request("GET", "/api/v1/accounts/verify_credentials", fresh.Token, "").Code)
}
