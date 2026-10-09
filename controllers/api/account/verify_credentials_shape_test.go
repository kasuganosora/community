package account

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyCredentialsShape(t *testing.T) {
	conn := setupDirectoryDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Emoji{}, &models.Role{}, &models.RoleUser{}))

	user := &models.User{UUID: "u1", UserName: "alice", EmailHash: "h"}
	require.NoError(t, conn.Create(user).Error)

	created := time.Date(2026, 1, 2, 3, 4, 5, 678901000, time.UTC)
	acc := &models.Account{Username: "alice", UserId: user.Id, CreatedAt: &created, LastStatusAt: &created}
	require.NoError(t, conn.Create(acc).Error)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/accounts/verify_credentials", nil) //nolint:noctx
	c.Set("account", acc)
	c.Set("user", user)
	VerifyCredentials(c)
	require.Equal(t, 200, w.Code, w.Body.String())

	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))

	for _, k := range []string{"id", "username", "acct", "url", "display_name", "note", "avatar", "avatar_static",
		"header", "header_static", "locked", "fields", "emojis", "bot", "group", "created_at", "last_status_at",
		"statuses_count", "followers_count", "following_count", "source", "role", "roles"} {
		assert.Contains(t, raw, k)
	}

	assert.Equal(t, "2026-01-02T00:00:00.000Z", raw["created_at"])
	assert.Equal(t, "2026-01-02", raw["last_status_at"])
	assert.Regexp(t, `\.png$`, raw["avatar"])
	assert.IsType(t, "", raw["id"])

	src, _ := raw["source"].(map[string]any)
	for _, k := range []string{"privacy", "sensitive", "language", "note", "fields", "follow_requests_count"} {
		assert.Contains(t, src, k)
	}

	role, _ := raw["role"].(map[string]any)
	assert.IsType(t, "", role["id"])
}
