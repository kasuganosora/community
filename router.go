package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/controllers/api/common"
)

func SetRouter(app *gin.Engine) {
	apiGroup := app.Group("/api")
	v2ApiGroup := apiGroup.Group("/v2")

	v2ApiGroup.GET("/instance", common.GetInstanceDetail)

	v2ApiGroup.POST("/account/register", account.Register)
	v2ApiGroup.GET("/account/verify_email", account.VerifyEmail)
	v2ApiGroup.POST("/account/auth", account.Auth)

	// 下面这些路由都是需要登录才能访问的

}
