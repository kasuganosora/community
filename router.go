package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/controllers/api/list"
	"github.com/kasuganosora/community/controllers/api/status"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
)

func SetRouter(app gin.IRouter) {
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
	maybeAuthGroup.GET("/statuses/:id", status.Show)

	// 下面这些路由都是需要登录才能访问的
	authGroup := v2ApiGroup.Use(middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	authInternalGroup := authGroup.Use(middleware.InternalApiMiddleware())
	authInternalGroup.GET("/accounts/logout", account.Logout)
	authInternalGroup.PUT("/accounts/me", account.UpdateAccount)
	authInternalGroup.PUT("/accounts/me/password", account.ChangePassword)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFollows})).
		POST("/accounts/:id/follow", account.Follow).
		DELETE("/accounts/:id/follow", account.UnFollow)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteLists})).
		POST("/lists", list.CreateList).
		PUT("/lists/:id", list.UpdateList).
		DELETE("/lists/:id", list.DeleteList).
		POST("/lists/:id/followers", list.FollowList).
		DELETE("/lists/:id/followers", list.UnfollowList).
		POST("/lists/:id/accounts", list.AddAccountToList).
		DELETE("/lists/:id/accounts", list.RemoveAccountFromList)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadLists})).
		GET("/lists", list.GetMyLists).
		GET("/lists/:id", list.GetList).
		GET("/lists/:id/accounts", list.GetListAccounts).
		GET("/lists/:id/followers", list.GetListFollowers)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses})).
		POST("/statuses", status.Create).
		DELETE("/statuses/:id", status.Delete).
		PUT("/statuses/:id", status.Update).
		POST("/statuses/:id/pin", status.Pin).
		POST("/statuses/:id/unpin", status.UnPin).
		POST("/statuses/:id/reblog", status.Reblog).
		POST("/statuses/:id/unreblog", status.Unreblog).
		POST("/statuses/:id/favourite", status.Favourite).
		POST("/statuses/:id/unfavourite", status.Unfavourite).
		POST("/statuses/:id/mute", status.MuteStatus).
		POST("/statuses/:id/unmute", status.UnmuteStatus).
		POST("/statuses/:id/bookmark", status.Bookmark).
		POST("/statuses/:id/unbookmark", status.Unbookmark).
		POST("/polls/:id/votes", status.VotePoll).
		POST("/scheduled_statuses/:id", status.UpdateStatusScheduledAt)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/statuses/:id/source", status.GetSource).
		GET("/statuses/:id/history", status.GetHistory).
		GET("/statuses/:id/favourited_by", status.FavouritedBy).
		GET("/statuses/:id/reblogged_by", status.RebloggedBy).
		GET("/polls/:id", status.ViewPoll).
		GET("/scheduled_statuses/:id", status.ViewSingleScheduledStatus).
		GET("/scheduled_statuses", status.ListScheduledStatuses)

	//maybeAuthGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
	//	GET("/statuses/:id", status.Show)

}
