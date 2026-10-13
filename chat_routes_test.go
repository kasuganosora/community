package community

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestCommunityChatUsesAuthenticatedIdentityAndWriteScope(t *testing.T) {
	old := homeserver.GlobalHS
	homeserver.GlobalHS = &homeserver.HomeServer{Config: &homeserver.HSConfig{Enabled: true, ServerName: "chat.example"}}
	t.Cleanup(func() { homeserver.GlobalHS = old })
	for _, tc := range []struct {
		name, method            string
		banned, mismatch, write bool
		status                  int
	}{
		{"read", "GET", false, false, false, 200},
		{"read-token-cannot-send", "POST", false, false, false, 401},
		{"write", "POST", false, false, true, 200},
		{"banned", "POST", true, false, true, 403},
		{"foreign-account", "GET", false, true, false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				user := &models.User{Id: 8, Status: models.UserStatusNormal, IsBan: tc.banned}
				account := &models.Account{Id: 3, UserId: 8, Username: "alice"}
				if tc.mismatch {
					account.UserId = 9
				}
				scopes := &models.ApplicationScope{ReadScope: models.AppScopeReadStatuses}
				if tc.write {
					scopes.WriteScope = models.AppScopeWriteStatuses
				}
				c.Set("user", user)
				c.Set("account", account)
				c.Set("token_data", &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth, Scopes: scopes})
				c.Set("user_id", "@attacker:elsewhere.example")
				c.Next()
			})
			r.Handle(tc.method, "/chat", communityChatIdentity, communityChatScope, func(c *gin.Context) { c.String(http.StatusOK, c.GetString("user_id")) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, "/chat?user_id=@outsider:example.org", nil))
			require.Equal(t, tc.status, w.Code)
			if tc.status == 200 {
				require.Equal(t, "@alice:chat.example", w.Body.String())
			}
		})
	}
}

func TestCommunityGroupReadScopeOnlyAllowsReadOnlyPosts(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/v1/chat/group-sync", http.StatusOK},
		{"/api/v1/chat/groups/resolve", http.StatusOK},
		{"/api/v1/chat/groups", http.StatusUnauthorized},
		{"/api/v1/chat/groups/join", http.StatusUnauthorized},
		{"/api/v1/chat/groups/resolve/extra", http.StatusUnauthorized},
		{"/api/v1/chat/groups/example/messages", http.StatusUnauthorized},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set("token_data", &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth, Scopes: &models.ApplicationScope{ReadScope: models.AppScopeReadStatuses}})
				c.Next()
			})
			r.POST(tc.path, communityChatScope, func(c *gin.Context) { c.Status(http.StatusOK) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, nil))
			require.Equal(t, tc.status, w.Code)
		})
	}
}
