package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/connections"
	"github.com/kasuganosora/community/middleware"
)

func registerConnectionsRoutes(auth *gin.RouterGroup) {
	group := auth.Group("/settings", connections.WebSession)
	group.GET("/tokens", connections.Tokens)
	// Rate limited: creating admin:write tokens re-checks the password.
	group.POST("/tokens", middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/api/v1/settings/tokens", 30), connections.CreateToken)
	group.DELETE("/tokens/:id", connections.RevokeToken)
	group.GET("/webhooks", connections.Webhooks)
	group.POST("/webhooks", connections.SaveWebhook)
	group.PUT("/webhooks/:id", connections.SaveWebhook)
	group.DELETE("/webhooks/:id", connections.DeleteWebhook)
	group.GET("/webhooks/:id/deliveries", connections.Deliveries)
	group.POST("/webhooks/:id/test", connections.TestWebhook)
}
