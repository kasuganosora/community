package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/controllers/api/admin"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/controllers/api/conversation"
	"github.com/kasuganosora/community/controllers/api/favourite"
	"github.com/kasuganosora/community/controllers/api/filter"
	"github.com/kasuganosora/community/controllers/api/follow_request"
	"github.com/kasuganosora/community/controllers/api/instance"
	"github.com/kasuganosora/community/controllers/api/list"
	"github.com/kasuganosora/community/controllers/api/marker"
	"github.com/kasuganosora/community/controllers/api/media"
	"github.com/kasuganosora/community/controllers/api/oauth"
	report2 "github.com/kasuganosora/community/controllers/api/report"
	"github.com/kasuganosora/community/controllers/api/rule"
	"github.com/kasuganosora/community/controllers/api/search"
	"github.com/kasuganosora/community/controllers/api/status"
	"github.com/kasuganosora/community/controllers/api/streaming"
	"github.com/kasuganosora/community/controllers/api/timelines"
	"github.com/kasuganosora/community/controllers/api/trends"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
)

func SetRouter(app gin.IRouter) {
	apiGroup := app.Group("/api")

	// API v1 - Mastodon 标准 API
	v1ApiGroup := apiGroup.Group("/v1")

	// API v2 - 仅用于确实有 v2 版本的接口
	v2ApiGroup := apiGroup.Group("/v2")

	v2ApiGroup.GET("/instance", common.GetInstanceDetail)
	v2ApiGroup.GET("/instance/rules", rule.List)
	v2ApiGroup.GET("/instance/rules/:id", rule.Get)

	// Search API v2 (Mastodon standard search endpoint)
	maybeAuthGroupV2Search := v2ApiGroup.Use(middleware.AuthMiddleware())
	maybeAuthGroupV2Search.GET("/search", search.Search)

	// Suggestions API v2
	maybeAuthGroupV2Suggestions := v2ApiGroup.Use(middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	maybeAuthGroupV2Suggestions.GET("/suggestions", account.ListSuggestions)

	// ===== API v1 标准 Mastodon 接口 =====
	// 公开接口（无需认证）
	v1ApiGroup.GET("/instance", common.GetInstanceDetail) // v2 也支持

	// OAuth 应用注册（公开接口）
	v1ApiGroup.POST("/apps", oauth.CreateApp)

	// OAuth 端点
	oauthGroup := app.Group("/oauth")
	oauthGroup.GET("/authorize", middleware.AuthMiddleware(), oauth.AuthorizePage)
	oauthGroup.POST("/authorize", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(), oauth.Authorize)
	oauthGroup.POST("/token", oauth.Token)
	oauthGroup.POST("/revoke", oauth.Revoke)

	// ===== ActivityPub 联邦端点 =====
	// WebFinger
	app.GET("/.well-known/webfinger", activitypub.WebFinger)
	app.GET("/.well-known/host-meta", activitypub.HostMeta)
	app.GET("/.well-known/nodeinfo", activitypub.NodeInfo)

	// ActivityPub Actor endpoints
	app.GET("/users/:username", activitypub.ActorProfile)
	app.POST("/users/:username/inbox", activitypub.Inbox)
	app.GET("/users/:username/outbox", activitypub.Outbox)
	app.GET("/users/:username/followers", activitypub.Followers)
	app.GET("/users/:username/following", activitypub.Following)
	app.GET("/users/:username/collections/featured", activitypub.Featured)

	// Shared inbox for efficient delivery
	app.POST("/inbox", activitypub.SharedInbox)

	// 账户注册和登录（自定义扩展）
	v1ApiGroup.POST("/accounts/register", account.Register)
	v1ApiGroup.GET("/accounts/verify_email", account.VerifyEmail)
	v1ApiGroup.POST("/accounts/login", account.Login)
	v1ApiGroup.POST("/accounts/reset_password", account.RecoverPasswordVerifyEmail)
	v1ApiGroup.POST("/accounts/reset_password/verify_token", account.RecoverPassword)

	// Streaming 健康检查
	v1ApiGroup.GET("/streaming/health", streaming.Health)

	// 可登录也可不登陆的路由（v1）
	maybeAuthGroupV1 := v1ApiGroup.Use(middleware.AuthMiddleware())
	maybeAuthGroupV1.GET("/accounts/search", account.Search)
	maybeAuthGroupV1.GET("/accounts/:id", account.GetAccount)
	maybeAuthGroupV1.GET("/accounts/:id/statuses", account.AccountStatuses)
	maybeAuthGroupV1.GET("/accounts/:id/followers", account.Followers)
	maybeAuthGroupV1.GET("/accounts/:id/following", account.Following)
	maybeAuthGroupV1.GET("/statuses/:id", status.Show)
	maybeAuthGroupV1.GET("/statuses/:id/context", status.Context)
	maybeAuthGroupV1.GET("/timelines/public", timelines.Public)
	maybeAuthGroupV1.GET("/timelines/list/:id", timelines.List)
	maybeAuthGroupV1.GET("/timelines/tag/:tag", timelines.Tag)
	maybeAuthGroupV1.GET("/streaming/list", streaming.ListStreaming)
	maybeAuthGroupV1.GET("/streaming/hashtag/local", streaming.HashtagStreamingLocal)
	maybeAuthGroupV1.GET("/streaming/hashtag/remote", streaming.HashtagStreamingRemote)
	maybeAuthGroupV1.GET("/streaming/hashtag", streaming.HashtagStreamingAll)
	maybeAuthGroupV1.GET("/streaming/public/local", streaming.PublicStreamingLocal)
	maybeAuthGroupV1.GET("/streaming/public/remote", streaming.PublicStreamingRemote)
	maybeAuthGroupV1.GET("/streaming/public", streaming.PublicStreamingAll)
	maybeAuthGroupV1.GET("/tags/:id", account.GetTag)
	maybeAuthGroupV1.GET("/directory", account.GetDirectory)
	maybeAuthGroupV1.GET("/custom_emojis", account.GetCustomEmojis)
	maybeAuthGroupV1.GET("/accounts/:id/featured_tags", account.AccountFeaturedTags)
	maybeAuthGroupV1.GET("/instance/peers", instance.Peers)

	// Status translation (public/optional auth)
	maybeAuthGroupV1.POST("/statuses/:id/translate/:lang", status.Translate)

	// Invites (public for lookup, auth for list/create)
	v1ApiGroup.GET("/invites/:code", account.GetInvite)

	// Tags follow/unfollow (requires auth)
	authTagGroup := v1ApiGroup.Use(middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	authTagGroup.POST("/tags/:id/follow", account.FollowTag)
	authTagGroup.POST("/tags/:id/unfollow", account.UnfollowTag)

	// Trends (public)
	v1ApiGroup.GET("/trends/tags", trends.ListTags)
	v1ApiGroup.GET("/trends/statuses", trends.ListStatuses)
	v1ApiGroup.GET("/trends/links", trends.ListLinks)

	// Announcements (public)
	v1ApiGroup.GET("/announcements", account.ListAnnouncements)

	// ===== 向后兼容：v2 接口（deprecated） =====
	v2ApiGroup.POST("/accounts/register", account.Register)
	v2ApiGroup.GET("/accounts/verify_email", account.VerifyEmail)
	v2ApiGroup.POST("/accounts/login", account.Login)
	v2ApiGroup.POST("/accounts/reset_password", account.RecoverPasswordVerifyEmail)
	v2ApiGroup.POST("/accounts/reset_password/verify_token", account.RecoverPassword)
	v2ApiGroup.GET("/streaming/health", streaming.Health)

	// 可登陆也可不登陆的路由（v2 兼容）
	maybeAuthGroup := v2ApiGroup.Use(middleware.AuthMiddleware())
	maybeAuthGroup.GET("/accounts/search", account.Search)
	maybeAuthGroup.GET("/accounts/:id", account.GetAccount)
	maybeAuthGroup.GET("/accounts/:id/statuses", account.AccountStatuses)
	maybeAuthGroup.GET("/accounts/:id/followers", account.Followers)
	maybeAuthGroup.GET("/accounts/:id/following", account.Following)
	maybeAuthGroup.GET("/statuses/:id", status.Show)
	maybeAuthGroup.GET("/statuses/:id/context", status.Context)
	maybeAuthGroup.GET("/timelines/public", timelines.Public)
	maybeAuthGroup.GET("/timelines/list/:id", timelines.List)
	maybeAuthGroup.GET("/timelines/tag/:tag", timelines.Tag)
	maybeAuthGroup.GET("/streaming/list", streaming.ListStreaming)
	maybeAuthGroup.GET("/streaming/hashtag/local", streaming.HashtagStreamingLocal)
	maybeAuthGroup.GET("/streaming/hashtag/remote", streaming.HashtagStreamingRemote)
	maybeAuthGroup.GET("/streaming/hashtag", streaming.HashtagStreamingAll)
	maybeAuthGroup.GET("/streaming/public/local", streaming.PublicStreamingLocal)
	maybeAuthGroup.GET("/streaming/public/remote", streaming.PublicStreamingRemote)
	maybeAuthGroup.GET("/streaming/public", streaming.PublicStreamingAll)

	// ===== 需要登录的路由（v1） =====
	authGroupV1 := v1ApiGroup.Use(middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())

	// OAuth 应用凭证验证
	authGroupV1.GET("/apps/verify_credentials", oauth.VerifyCredentials)

	// 账户凭证
	authGroupV1.GET("/accounts/verify_credentials", account.VerifyCredentials)
	authGroupV1.GET("/accounts/relationships", account.Relationships)
	authGroupV1.PATCH("/accounts/update_credentials", account.UpdateCredentials)
	authGroupV1.GET("/accounts/familiar_followers", account.FamiliarFollowers)

	// Invites (authenticated)
	authGroupV1.GET("/invites", account.ListInvites)
	authGroupV1.POST("/invites", account.CreateInvite)

	// Email confirmations
	authGroupV1.POST("/emails/confirmations", account.SendEmailConfirmation)
	authGroupV1.POST("/emails/confirmations/verify", account.VerifyEmailConfirmation)

	// 账户操作 (block/unblock/mute/unmute/pin/unpin)
	authGroupV1.POST("/accounts/:id/block", account.Block)
	authGroupV1.POST("/accounts/:id/unblock", account.Unblock)
	authGroupV1.POST("/accounts/:id/mute", account.Mute)
	authGroupV1.POST("/accounts/:id/unmute", account.Unmute)
	authGroupV1.POST("/accounts/:id/pin", account.PinAccount)
	authGroupV1.POST("/accounts/:id/unpin", account.UnpinAccount)

	authInternalGroupV1 := authGroupV1.Use(middleware.InternalApiMiddleware())
	authInternalGroupV1.GET("/accounts/logout", account.Logout)
	authInternalGroupV1.PUT("/accounts/me", account.UpdateAccount)
	authInternalGroupV1.PUT("/accounts/me/password", account.ChangePassword)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/notifications", timelines.NotificationTimeline).
		GET("/notifications/:id", timelines.GetNotification)

	// Notifications write operations
	authGroupV1.POST("/notifications/:id/dismiss", timelines.DismissNotification)
	authGroupV1.POST("/notifications/clear", timelines.ClearNotifications)
	authGroupV1.GET("/notifications/policy", timelines.GetNotificationPolicy)
	authGroupV1.POST("/notifications/policy", timelines.UpdateNotificationPolicy)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFollows})).
		POST("/accounts/:id/follow", account.Follow).
		DELETE("/accounts/:id/follow", account.UnFollow).
		POST("/follow_requests/:id/authorize", follow_request.Authorize).
		POST("/follow_requests/:id/reject", follow_request.Reject)

	// Follow Requests (read scope)
	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFollows})).
		GET("/follow_requests", follow_request.List).
		GET("/blocks", account.BlocksList).
		GET("/mutes", account.MutesList).
		GET("/endorsements", account.ListEndorsements).
		GET("/followed_tags", account.ListFollowedTags)

	// Domain blocks
	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFollows})).
		POST("/domain_blocks", account.BlockDomain).
		DELETE("/domain_blocks", account.UnblockDomain)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFollows})).
		GET("/domain_blocks", account.DomainBlocksList)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteLists})).
		POST("/lists", list.CreateList).
		PUT("/lists/:id", list.UpdateList).
		DELETE("/lists/:id", list.DeleteList).
		POST("/lists/:id/followers", list.FollowList).
		DELETE("/lists/:id/followers", list.UnfollowList).
		POST("/lists/:id/accounts", list.AddAccountToList).
		DELETE("/lists/:id/accounts", list.RemoveAccountFromList)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadLists})).
		GET("/lists", list.GetMyLists).
		GET("/lists/:id", list.GetList).
		GET("/lists/:id/accounts", list.GetListAccounts).
		GET("/lists/:id/followers", list.GetListFollowers)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses})).
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
		POST("/scheduled_statuses/:id", status.UpdateStatusScheduledAt).
		POST("/markers", marker.Save)

	// Media API - 媒体上传
	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteMedia})).
		POST("/media", media.Upload).
		POST("/media/v2", media.UploadAsync).
		PUT("/media/:id", media.Update).
		DELETE("/media/:id", media.Delete)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/statuses/:id/source", status.GetSource).
		GET("/statuses/:id/history", status.GetHistory).
		GET("/statuses/:id/favourited_by", status.FavouritedBy).
		GET("/statuses/:id/reblogged_by", status.RebloggedBy).
		GET("/polls/:id", status.ViewPoll).
		GET("/scheduled_statuses/:id", status.ViewSingleScheduledStatus).
		GET("/scheduled_statuses", status.ListScheduledStatuses).
		GET("/timelines/home", timelines.Home).
		GET("/timelines/mention", timelines.Mention).
		GET("/favourites", favourite.Favourites).
		GET("/bookmarks", favourite.Bookmarks)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses}, middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/streaming/user", streaming.UserStreaming).
		GET("/streaming/direct", streaming.DirectStreaming).
		GET("/streaming/notifications", streaming.NotificationStreaming)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/conversations", conversation.List).
		GET("/markers", marker.List).
		GET("/preferences", account.GetPreferences)

	// Suggestions and Announcements (authenticated)
	authGroupV1.GET("/suggestions", account.ListSuggestions)
	authGroupV1.DELETE("/suggestions/:account_id", account.RemoveSuggestion)
	authGroupV1.POST("/announcements/:id/dismiss", account.DismissAnnouncement)
	authGroupV1.PUT("/announcements/:id/reactions/:name", account.AddAnnouncementReaction)
	authGroupV1.DELETE("/announcements/:id/reactions/:name", account.RemoveAnnouncementReaction)

	// Featured Tags
	authGroupV1.GET("/featured_tags", account.ListFeaturedTags)
	authGroupV1.POST("/featured_tags", account.CreateFeaturedTag)
	authGroupV1.DELETE("/featured_tags/:id", account.DeleteFeaturedTag)
	authGroupV1.GET("/featured_tags/suggestions", account.SuggestFeaturedTags)

	// Push Notifications
	authGroupV1.POST("/push/subscription", account.CreatePushSubscription)
	authGroupV1.GET("/push/subscription", account.GetPushSubscription)
	authGroupV1.PUT("/push/subscription", account.UpdatePushSubscription)
	authGroupV1.DELETE("/push/subscription", account.DeletePushSubscription)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteConversations})).
		DELETE("/conversations/:id", conversation.Remove).
		POST("/conversations/:id/read", conversation.MarkRead)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFilters})).
		GET("/filters", filter.List).
		GET("/filters/:id", filter.Show).
		GET("/filters/:id/keywords", filter.KeywordsList).
		GET("/filters/keywords/:id", filter.KeywordView)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFilters})).
		POST("/filters", filter.Create).
		PUT("/filters/:id", filter.Update).
		DELETE("/filters/:id", filter.Delete).
		POST("/filters/:filter_id/keywords", filter.KeywordCreate).
		PUT("/filters/keywords/:id", filter.KeywordUpdate).
		DELETE("/filters/keywords/:id", filter.KeywordDelete)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteReports})).
		POST("/reports", report2.Create)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadReports})).
		GET("/admin/reports/", report2.List).
		GET("/admin/reports/:id", report2.GetReport)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteReports})).
		PUT("/admin/reports/:id", report2.Update).
		DELETE("/admin/reports/:id", report2.Delete).
		POST("/admin/reports/:id/assign_to_self", report2.AssignToSelf).
		POST("/admin/reports/:id/unassign", report2.Unassign).
		POST("/admin/reports/:id/resolve", report2.Resolve).
		POST("/admin/reports/:id/reopen", report2.Reopen)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteRules})).
		POST("/admin/rule", rule.Create).
		PUT("/admin/rule/:id", rule.Update).
		DELETE("/admin/rule/:id", rule.Delete)

	// Admin Accounts API
	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadAccounts})).
		GET("/admin/accounts", admin.ListAccounts).
		GET("/admin/accounts/:id", admin.GetAccount)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteAccounts})).
		POST("/admin/accounts/:id/action", admin.PerformAccountAction).
		POST("/admin/accounts/:id/enable", admin.EnableAccount).
		POST("/admin/accounts/:id/approve", admin.ApproveAccount).
		POST("/admin/accounts/:id/reject", admin.RejectAccount).
		POST("/admin/accounts/:id/unsilence", admin.UnsilenceAccount).
		POST("/admin/accounts/:id/unsuspend", admin.UnsuspendAccount)

	// Admin Statuses API
	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/statuses/:id", admin.GetStatus)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite})).
		POST("/admin/statuses/:id/action", admin.PerformStatusAction).
		DELETE("/admin/statuses/:id", admin.DeleteStatus)

	// Admin Stats API (requires admin:read)
	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/dimensions", admin.GetDimensions).
		GET("/admin/measures", admin.GetMeasures).
		GET("/admin/retention", admin.GetRetention)

	// Admin Custom Emojis API
	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/custom_emojis", admin.ListCustomEmojis).
		GET("/admin/custom_emojis/:id", admin.GetCustomEmoji)

	authGroupV1.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite})).
		POST("/admin/custom_emojis", admin.CreateCustomEmoji).
		PUT("/admin/custom_emojis/:id", admin.UpdateCustomEmoji).
		DELETE("/admin/custom_emojis/:id", admin.DeleteCustomEmoji).
		POST("/admin/custom_emojis/:id/copy", admin.CopyCustomEmoji).
		POST("/admin/custom_emojis/:id/enable", admin.EnableCustomEmoji).
		POST("/admin/custom_emojis/:id/disable", admin.DisableCustomEmoji)

	// ===== 向后兼容：v2 接口（deprecated） =====
	// 下面这些路由都是需要登录才能访问的
	authGroup := v2ApiGroup.Use(middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	authInternalGroup := authGroup.Use(middleware.InternalApiMiddleware())
	authInternalGroup.GET("/accounts/logout", account.Logout)
	authInternalGroup.PUT("/accounts/me", account.UpdateAccount)
	authInternalGroup.PUT("/accounts/me/password", account.ChangePassword)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/notifications", timelines.NotificationTimeline)

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
		POST("/scheduled_statuses/:id", status.UpdateStatusScheduledAt).
		POST("/markers", marker.Save)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/statuses/:id/source", status.GetSource).
		GET("/statuses/:id/history", status.GetHistory).
		GET("/statuses/:id/favourited_by", status.FavouritedBy).
		GET("/statuses/:id/reblogged_by", status.RebloggedBy).
		GET("/polls/:id", status.ViewPoll).
		GET("/scheduled_statuses/:id", status.ViewSingleScheduledStatus).
		GET("/scheduled_statuses", status.ListScheduledStatuses).
		GET("/timelines/home", timelines.Home).
		GET("/timelines/mention", timelines.Mention)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses}, middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/streaming/user", streaming.UserStreaming).
		GET("/streaming/direct", streaming.DirectStreaming).
		GET("/streaming/notifications", streaming.NotificationStreaming)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/conversations", conversation.List).
		GET("/markers", marker.List)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteConversations})).
		DELETE("/conversations/:id", conversation.Remove).
		POST("/conversations/:id/read", conversation.MarkRead)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFilters})).
		GET("/filters", filter.List).
		GET("/filters/:id", filter.Show).
		GET("/filters/:id/keywords", filter.KeywordsList).
		GET("/filters/keywords/:id", filter.KeywordView)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFilters})).
		POST("/filters", filter.Create).
		PUT("/filters/:id", filter.Update).
		DELETE("/filters/:id", filter.Delete).
		POST("/filters/:filter_id/keywords", filter.KeywordCreate).
		PUT("/filters/keywords/:id ", filter.KeywordUpdate).
		DELETE("/filters/keywords/:id ", filter.KeywordDelete)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteReports})).
		POST("/reports", report2.Create)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadReports})).
		GET("/admin/reports/", report2.List).
		GET("/admin/reports/:id", report2.GetReport)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteReports})).
		PUT("/admin/reports/:id", report2.Update).
		DELETE("/admin/reports/:id", report2.Delete).
		POST("/admin/reports/:id/assign_to_self", report2.AssignToSelf).
		POST("/admin/reports/:id/unassign", report2.Unassign).
		POST("/admin/reports/:id/resolvee", report2.Resolve).
		POST("/admin/reports/:id/reopen", report2.Reopen)

	authGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteRules})).
		POST("/admin/rule", rule.Create).
		PUT("/admin/rule/:id", rule.Update).
		DELETE("/admin/rule/:id", rule.Delete)

	//maybeAuthGroup.Use(middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
	//	GET("/statuses/:id", status.Show)

}
