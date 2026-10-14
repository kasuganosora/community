package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/discovery"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
)

func registerDiscoveryRoutes(public, auth *gin.RouterGroup) {
	public.GET("/discovery/tags", discovery.ListTags)
	read := auth.Group("/discovery", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses}))
	read.GET("/statuses", discovery.ListStatuses)
	preferences := auth.Group("/discovery", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadAccounts}))
	preferences.GET("/preferences", discovery.GetPreferences)
	preferences.GET("/feedback", discovery.ListFeedback)
	write := auth.Group("/discovery", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteAccounts}))
	write.PUT("/preferences", discovery.UpdatePreferences)
	write.POST("/feedback", discovery.CreateFeedback)
	write.DELETE("/feedback/:id", discovery.DeleteFeedback)
}
