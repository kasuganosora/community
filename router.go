package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
)

func SetRouter(app *gin.Engine) {
	apiGroup := app.Group("/api")
	v2ApiGroup := apiGroup.Group("/v2")

	v2ApiGroup.GET("/instance", common.GetInstanceDetail)

	v2ApiGroup.POST("/accounts/register", account.Register)
	v2ApiGroup.GET("/accounts/verify_email", account.VerifyEmail)
	v2ApiGroup.POST("/accounts/login", account.Login)
	v2ApiGroup.POST("/accounts/reset_password", account.RecoverPasswordVerifyEmail)
	v2ApiGroup.POST("/accounts/reset_password/verify_token", account.RecoverPassword)

	// 可登陆也可不登陆的路由
	maybeAuthGroup := v2ApiGroup.Use(middleware.AuthMiddleware())
	maybeAuthGroup.GET("/accounts/search", account.Search)
	maybeAuthGroup.GET("/accounts/:id", account.GetAccount)

	// 下面这些路由都是需要登录才能访问的
	authGroup := v2ApiGroup.Use(middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	authInternalGroup := authGroup.Use(middleware.InternalApiMiddleware())
	authInternalGroup.GET("/accounts/logout", account.Logout)
	authInternalGroup.PUT("/accounts/me", account.UpdateAccount)
	authInternalGroup.PUT("/accounts/me/password", account.ChangePassword)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFollows})).
		POST("/accounts/:id/follow", account.Follow).
		DELETE("/accounts/:id/follow", account.UnFollow)
}
