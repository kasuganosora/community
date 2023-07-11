package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/auth"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/controllers/api/common"
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

	// 下面这些路由都是需要登录才能访问的
	authGroup := v2ApiGroup.Use(auth.AuthMiddleware())
	authGroup.GET("/accounts/logout", account.Logout)
	authGroup.GET("/accounts/me", account.GetMe)
	authGroup.PUT("/accounts/me", account.UpdateAccount)
	authGroup.PUT("/accounts/me/password", account.ChangePassword)
}
