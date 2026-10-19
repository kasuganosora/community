package community

import (
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPersonalTokenEditScopes(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "sql"
		if cached {
			name = "redis-cache"
		}
		t.Run(name, func(t *testing.T) {
			f, owner, other := newConnectionsFixture(t)
			require.NoError(t, f.conn.AutoMigrate(&models.Role{}, &models.RoleUser{}))
			if cached {
				mr := miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = client.Close() })
				t.Cleanup(middleware.SetTokenCacheRedisForTest(client))
			}
			post := func(token string) int {
				return f.request("POST", "/api/v1/statuses", token, `{"status":"hello"}`).Code
			}
			tok := createPAT(t, f, owner, `{"name":"app","scopes":["read"],"expires_in_days":0}`, 201)
			require.Equal(t, 200, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)
			require.Equal(t, 403, post(tok.Token), "read-only token, now cached")

			path := "/api/v1/settings/tokens/" + tok.Item.ID
			require.Equal(t, 404, f.request("PATCH", path, other, `{"name":"x","scopes":["read"]}`).Code, "other user's token")
			require.Equal(t, 422, f.request("PATCH", path, owner, `{"name":"x","scopes":["bogus"]}`).Code)
			require.Equal(t, 422, f.request("PATCH", path, owner, `{"name":"","scopes":["read"]}`).Code)
			require.Equal(t, 403, f.request("PATCH", path, owner, `{"name":"x","scopes":["admin:read"]}`).Code, "admin scopes only for admins")

			w := f.request("PATCH", path, owner, `{"name":"app2","scopes":["read","write:statuses"]}`)
			require.Equal(t, 200, w.Code, w.Body.String())
			var item models.PersonalAccessToken
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &item))
			require.Equal(t, "app2", item.Name)
			require.Equal(t, []string{"read", "write:statuses"}, item.Scopes)
			code := post(tok.Token)
			require.NotEqual(t, 401, code)
			require.NotEqual(t, 403, code, "new scope applies on the next request")
			require.Equal(t, 403, f.request("POST", "/api/v1/lists", tok.Token, `{"title":"x"}`).Code, "only the granted write scope")

			require.Equal(t, 200, f.request("PATCH", path, owner, `{"name":"app2","scopes":["read"]}`).Code)
			require.Equal(t, 403, post(tok.Token), "removed scope is refused on the next request")
			require.Equal(t, 200, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)

			// Revoked tokens cannot be edited.
			require.Equal(t, 204, f.request("DELETE", path, owner, "").Code)
			require.Equal(t, 404, f.request("PATCH", path, owner, `{"name":"x","scopes":["read"]}`).Code)
		})
	}
}

func TestLegacyPersonalTokenScopesBackfill(t *testing.T) {
	f, owner, _ := newConnectionsFixture(t)
	require.NoError(t, f.conn.AutoMigrate(&models.Role{}, &models.RoleUser{}))
	post := func(token string) int { return f.request("POST", "/api/v1/statuses", token, `{"status":"hello"}`).Code }
	tok := createPAT(t, f, owner, `{"name":"tbtest","scopes":["read:statuses"],"expires_in_days":0}`, 201)
	blob, err := models.GetAuthTokenData(tok.Token, config.C.EncryptedKey)
	require.NoError(t, err)
	// Simulate a pre-edit row: no DB scope bits at all.
	require.NoError(t, f.conn.Exec("UPDATE personal_access_tokens SET scope_bits = NULL, scope_source = '' WHERE id = ?", tok.Item.ID).Error)
	load := func() models.PersonalAccessToken {
		var row models.PersonalAccessToken
		require.NoError(t, f.conn.First(&row, "id = ?", tok.Item.ID).Error)
		return row
	}
	require.Nil(t, load().ScopeBits)
	require.NotEqual(t, 403, f.request("GET", "/api/v1/accounts/verify_credentials", tok.Token, "").Code)
	require.Equal(t, 403, f.request("GET", "/api/v1/notifications", tok.Token, "").Code, "blob scopes exactly: no read:notifications")
	require.Equal(t, 403, post(tok.Token))
	row := load()
	require.NotNil(t, row.ScopeBits, "backfilled on first use")
	require.Equal(t, *blob.Scopes, *row.ScopeBits.Scope())
	require.Equal(t, models.ScopeSourceBlob, row.ScopeSource)

	// Migration-derived bits that disagree with the blob never widen it.
	require.NoError(t, f.conn.Model(&models.PersonalAccessToken{}).Where("id = ?", tok.Item.ID).
		Select("scope_bits", "scope_source").Updates(&models.PersonalAccessToken{ScopeBits: &models.ScopeBits{ReadScope: models.AppScopeRead, WriteScope: models.AppScopeWrite}, ScopeSource: models.ScopeSourceMigration}).Error)
	require.Equal(t, 403, post(tok.Token), "blob wins over migration bits")
	require.Equal(t, models.ScopeSourceMigration, load().ScopeSource, "nothing auto-changed")
}
