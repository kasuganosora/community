package community

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
)

// registerChatRoutes exposes the same conversation service to Community
// sessions. It never mints a Matrix access token or trusts a client-sent MXID.
func registerChatRoutes(authenticated *gin.RouterGroup) {
	group := authenticated.Group("/chat", communityChatIdentity, communityChatScope)
	homeserver.RegisterCommunityConversationRoutes(group)
	homeserver.RegisterCommunityGroupRoutes(group)
}

func communityChatIdentity(c *gin.Context) {
	hs := homeserver.GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "conversation service unavailable"})
		return
	}
	raw, _ := c.Get("account")
	account, ok := raw.(*models.Account)
	u, _ := c.Get("user")
	user, valid := u.(*models.User)
	if !ok || account == nil || account.Domain != nil || account.IsBan || account.UserId == 0 || !valid || user == nil || user.Id != account.UserId || user.IsBan || user.Status != models.UserStatusNormal {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "local active account required"})
		return
	}
	c.Set("user_id", homeserver.FormatMatrixUserID(account.Username, hs.Config.ServerName))
	c.Set("device_id", "community-web")
	c.Next()
}

func communityChatScope(c *gin.Context) {
	scope := middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses}
	readPost := c.Request.Method == http.MethodPost && (c.Request.URL.Path == "/api/v1/chat/group-sync" || c.Request.URL.Path == "/api/v1/chat/groups/resolve")
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && !readPost {
		scope = middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses}
	}
	middleware.CheckScopeMiddleware(scope)(c)
}
