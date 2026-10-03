package community

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/controllers/api/admin"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/controllers/api/contact"
	"github.com/kasuganosora/community/controllers/api/conversation"
	"github.com/kasuganosora/community/controllers/api/favourite"
	"github.com/kasuganosora/community/controllers/api/filter"
	"github.com/kasuganosora/community/controllers/api/follow_request"
	group2 "github.com/kasuganosora/community/controllers/api/group"
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
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/matrix"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
)

// SetRouter registers all HTTP routes on the given Gin engine/router group.
//
// 中间件架构说明（重要）:
// Gin 的 RouterGroup.Use() 会就地追加中间件到同一个组对象上，因此连续在
// 同一个变量上调用 .Use(...) 会导致中间件累积泄露到后续所有路由。
// 为了避免该问题，本函数中每一个需要独立中间件链的子组都通过
// RouterGroup.Group("", middlewares...) 创建新的、独立的组对象，
// 而不是复用同一个变量再追加。这保证了每条路由只会执行它应当执行的中间件。
//
// 控制器使用 AbortPanic()（panic(abortSentinel)）作为控制流，用来自动
// 终止 handler 执行并返回已写入的 HTTP 响应。AbortRecoveryMiddleware
// 捕获该哨兵 panic 并静默吞掉，防止 gin.Recovery() 记录虚假的堆栈日志。
// 必须在 gin.Recovery() 之后注册（即成为更内层的中间件），使其 defer
// 先于 gin.Recovery 的 recover 执行。
func SetRouter(app gin.IRouter) {
	app.Use(controllers.AbortRecoveryMiddleware())

	// 全局：自毁模式信息头
	app.Use(middleware.SelfDestructInfoMiddleware())

	// 全局：自毁模式下的写操作限制（必须在业务路由之前应用）
	app.Use(middleware.SelfDestructMiddleware())

	// Uploaded media files (URLs built by serializer.ToMediaAttachment).
	app.GET("/media/:id/*file", media.Serve)
	app.GET("/emoji-assets/:file", emojiasset.Serve)

	apiGroup := app.Group("/api")

	// API v1 - Mastodon 标准 API
	v1ApiGroup := apiGroup.Group("/v1")

	// API v2 - 仅用于确实有 v2 版本的接口
	v2ApiGroup := apiGroup.Group("/v2")

	v2ApiGroup.GET("/instance", common.GetInstanceDetail)
	v2ApiGroup.GET("/instance/rules", rule.List)
	v2ApiGroup.GET("/instance/rules/:id", rule.Get)

	// ===== 公开接口（无需认证）=====

	// Instance 信息（v1 与 v2 共用同一处理函数）
	v1ApiGroup.GET("/instance", common.GetInstanceDetail) // v2 也支持
	// Mastodon serves the rules under v1; they were only registered on v2.
	v1ApiGroup.GET("/instance/rules", rule.List)

	// OAuth 应用注册
	v1ApiGroup.POST("/apps", oauth.CreateApp)

	// Captcha 配置
	v1ApiGroup.GET("/captcha/config", common.GetCaptchaConfig)
	v1ApiGroup.GET("/captcha/providers", common.GetCaptchaProviders)

	// OAuth 端点
	oauthGroup := app.Group("/oauth")
	oauthGroup.GET("/authorize", middleware.AuthMiddleware(), oauth.AuthorizePage)
	oauthGroup.POST("/authorize", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(), oauth.Authorize)
	oauthGroup.POST("/token", oauth.Token)
	oauthGroup.POST("/revoke", oauth.Revoke)

	// ===== ActivityPub 联邦端点 =====
	// WebFinger / NodeInfo
	app.GET("/.well-known/webfinger", activitypub.WebFinger)
	app.GET("/.well-known/host-meta", activitypub.HostMeta)
	app.GET("/.well-known/nodeinfo", activitypub.NodeInfo)

	// ActivityPub Actor 端点
	app.GET("/users/:username", activitypub.ActorProfile)
	app.GET("/users/:username/statuses/:id", activitypub.StatusObject)

	// 用户 Inbox（限流：每 IP 每分钟 50 次请求）
	userInboxGroup := app.Group("", middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/users/inbox", 50))
	userInboxGroup.POST("/users/:username/inbox", activitypub.Inbox)

	app.GET("/users/:username/outbox", activitypub.Outbox)
	app.GET("/users/:username/followers", activitypub.Followers)
	app.GET("/users/:username/following", activitypub.Following)
	app.GET("/users/:username/collections/featured", activitypub.Featured)

	// Shared Inbox（限流：每 IP 每分钟 100 次请求）
	inboxGroup := app.Group("", middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/inbox", 100))
	inboxGroup.POST("/inbox", activitypub.SharedInbox)

	// ===== 账户注册与登录（自定义扩展，公开接口）=====
	v1ApiGroup.POST("/accounts/register", account.Register)
	v1ApiGroup.GET("/accounts/verify_email", account.VerifyEmail)
	v1ApiGroup.POST("/accounts/login", account.Login)
	v1ApiGroup.POST("/accounts/reset_password", account.RecoverPasswordVerifyEmail)
	v1ApiGroup.POST("/accounts/reset_password/verify_token", account.RecoverPassword)

	// Streaming 健康检查（公开）
	v1ApiGroup.GET("/streaming/health", streaming.Health)

	// ===== 可登录也可不登录的路由（v1）=====
	// AuthMiddleware 在无 token 时放行，由各 handler 决定是否需要登录。
	maybeAuthGroupV1 := v1ApiGroup.Group("", middleware.AuthMiddleware())
	maybeAuthGroupV1.GET("/accounts/search", account.Search)
	maybeAuthGroupV1.GET("/accounts/:id", account.GetAccount)
	maybeAuthGroupV1.GET("/accounts/:id/statuses", account.AccountStatuses)
	maybeAuthGroupV1.GET("/accounts/:id/followers", account.Followers)
	maybeAuthGroupV1.GET("/accounts/:id/following", account.Following)
	maybeAuthGroupV1.GET("/statuses/:id", status.Show)
	maybeAuthGroupV1.GET("/statuses/:id/context", status.Context)
	maybeAuthGroupV1.GET("/statuses/:id/reactions", status.Reactions)
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

	// 状态翻译（公开/可选认证）
	maybeAuthGroupV1.POST("/statuses/:id/translate/:lang", status.Translate)

	// 邀请码查询（公开）
	v1ApiGroup.GET("/invites/:code", account.GetInvite)

	// 标签关注/取关（需要登录）
	authTagGroup := v1ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	authTagGroup.POST("/tags/:id/follow", account.FollowTag)
	authTagGroup.POST("/tags/:id/unfollow", account.UnfollowTag)

	// 热门话题（公开）
	v1ApiGroup.GET("/trends/tags", trends.ListTags)
	v1ApiGroup.GET("/trends/statuses", trends.ListStatuses)
	v1ApiGroup.GET("/trends/links", trends.ListLinks)

	// 公告（公开）
	v1ApiGroup.GET("/announcements", account.ListAnnouncements)

	// ===== 需要登录的路由（v1）=====
	// 认证基础组：AuthMiddleware 解析 token，CheckAuthMiddleware 强制要求已登录。
	authGroupV1 := v1ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())

	// OAuth 应用凭证验证
	authGroupV1.GET("/apps/verify_credentials", oauth.VerifyCredentials)

	// 账户凭证
	authGroupV1.GET("/accounts/verify_credentials", account.VerifyCredentials)
	authGroupV1.GET("/accounts/relationships", account.Relationships)
	authGroupV1.PATCH("/accounts/update_credentials", account.UpdateCredentials)
	authGroupV1.GET("/accounts/familiar_followers", account.FamiliarFollowers)

	// 邀请码管理（已登录）
	authGroupV1.GET("/invites", account.ListInvites)
	authGroupV1.POST("/invites", account.CreateInvite)

	// 邮箱确认
	authGroupV1.POST("/emails/confirmations", account.SendEmailConfirmation)
	authGroupV1.POST("/emails/confirmations/verify", account.VerifyEmailConfirmation)

	// 账户操作 (block/unblock/mute/unmute/pin/unpin)
	authGroupV1.POST("/accounts/:id/block", account.Block)
	authGroupV1.POST("/accounts/:id/unblock", account.Unblock)
	authGroupV1.POST("/accounts/:id/mute", account.Mute)
	authGroupV1.POST("/accounts/:id/unmute", account.Unmute)
	authGroupV1.POST("/accounts/:id/pin", account.PinAccount)
	authGroupV1.POST("/accounts/:id/unpin", account.UnpinAccount)

	// 内部接口（仅允许 Web 端登录的 token，拒绝 OAuth token）
	authInternalGroupV1 := authGroupV1.Group("", middleware.InternalApiMiddleware())
	authInternalGroupV1.GET("/accounts/logout", account.Logout)
	authInternalGroupV1.PUT("/accounts/me", account.UpdateAccount)
	authInternalGroupV1.PUT("/accounts/me/password", account.ChangePassword)

	// 通知（读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/notifications", timelines.NotificationTimeline).
		GET("/notifications/:id", timelines.GetNotification)

	// 通知策略（读取）——与通知读取相同的 scope
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/notifications/policy", timelines.GetNotificationPolicy)

	// 通知（写入）——需要通知写入 scope
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteNotifications})).
		POST("/notifications/:id/dismiss", timelines.DismissNotification).
		POST("/notifications/clear", timelines.ClearNotifications).
		POST("/notifications/policy", timelines.UpdateNotificationPolicy)

	// 关注操作（写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFollows})).
		POST("/accounts/:id/follow", account.Follow).
		DELETE("/accounts/:id/follow", account.UnFollow).
		POST("/accounts/:id/unfollow", account.UnFollow). // Mastodon API
		POST("/follow_requests/:id/authorize", follow_request.Authorize).
		POST("/follow_requests/:id/reject", follow_request.Reject)

	// 关注请求 / 屏蔽 / 静音 / 认可 / 已关注标签（读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFollows})).
		GET("/follow_requests", follow_request.List).
		GET("/blocks", account.BlocksList).
		GET("/mutes", account.MutesList).
		GET("/endorsements", account.ListEndorsements).
		GET("/followed_tags", account.ListFollowedTags)

	// 域名屏蔽（写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFollows})).
		POST("/domain_blocks", account.BlockDomain).
		DELETE("/domain_blocks", account.UnblockDomain)

	// 域名屏蔽（读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFollows})).
		GET("/domain_blocks", account.DomainBlocksList)

	// 列表（写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteLists})).
		POST("/lists", list.CreateList).
		PUT("/lists/:id", list.UpdateList).
		DELETE("/lists/:id", list.DeleteList).
		POST("/lists/:id/followers", list.FollowList).
		DELETE("/lists/:id/followers", list.UnfollowList).
		POST("/lists/:id/accounts", list.AddAccountToList).
		DELETE("/lists/:id/accounts", list.RemoveAccountFromList)

	// 列表（读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadLists})).
		GET("/lists", list.GetMyLists).
		GET("/lists/:id", list.GetList).
		GET("/lists/:id/accounts", list.GetListAccounts).
		GET("/lists/:id/followers", list.GetListFollowers)

	// 状态（写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses})).
		POST("/statuses", status.Create).
		DELETE("/statuses/:id", status.Delete).
		PUT("/statuses/:id", status.Update).
		POST("/statuses/:id/replace", status.Replace).
		POST("/statuses/:id/pin", status.Pin).
		POST("/statuses/:id/unpin", status.UnPin).
		POST("/statuses/:id/reblog", status.Reblog).
		POST("/statuses/:id/unreblog", status.Unreblog).
		POST("/statuses/:id/favourite", status.Favourite).
		POST("/statuses/:id/unfavourite", status.Unfavourite).
		PUT("/statuses/:id/reactions/:name", status.AddReaction).
		DELETE("/statuses/:id/reactions/:name", status.RemoveReaction).
		POST("/statuses/:id/mute", status.MuteStatus).
		POST("/statuses/:id/unmute", status.UnmuteStatus).
		POST("/statuses/:id/bookmark", status.Bookmark).
		POST("/statuses/:id/unbookmark", status.Unbookmark).
		POST("/polls/:id/votes", status.VotePoll).
		POST("/scheduled_statuses/:id", status.UpdateStatusScheduledAt).
		POST("/markers", marker.Save)

	// 媒体上传（写入，需登录 + 媒体写入 scope + 限流：每用户每分钟 30 次）
	mediaGroup := v1ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(),
		middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/media", 30))
	mediaGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteMedia})).
		POST("/media", media.Upload).
		POST("/media/v2", media.UploadAsync).
		GET("/media/:id", media.Get).
		PUT("/media/:id", media.Update).
		DELETE("/media/:id", media.Delete)

	// Mastodon clients upload via POST /api/v2/media (async upload).
	v2ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(),
		middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/media", 30),
		middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteMedia})).
		POST("/media", media.UploadAsync)

	// 状态（读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
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

	// 流式接口（需要读取状态 + 读取通知 scope）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses},
		middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/streaming/user", streaming.UserStreaming).
		GET("/streaming/direct", streaming.DirectStreaming).
		GET("/streaming/notifications", streaming.NotificationStreaming)

	// 会话 / 标记 / 偏好设置（读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/conversations", conversation.List).
		GET("/markers", marker.List).
		GET("/preferences", account.GetPreferences)

	// 建议与公告（已登录）
	authGroupV1.GET("/suggestions", account.ListSuggestions)
	authGroupV1.DELETE("/suggestions/:account_id", account.RemoveSuggestion)
	authGroupV1.POST("/announcements/:id/dismiss", account.DismissAnnouncement)
	authGroupV1.PUT("/announcements/:id/reactions/:name", account.AddAnnouncementReaction)
	authGroupV1.DELETE("/announcements/:id/reactions/:name", account.RemoveAnnouncementReaction)

	// 精选标签
	authGroupV1.GET("/featured_tags", account.ListFeaturedTags)
	authGroupV1.POST("/featured_tags", account.CreateFeaturedTag)
	authGroupV1.DELETE("/featured_tags/:id", account.DeleteFeaturedTag)
	authGroupV1.GET("/featured_tags/suggestions", account.SuggestFeaturedTags)

	// 推送通知订阅
	authGroupV1.POST("/push/subscription", account.CreatePushSubscription)
	authGroupV1.GET("/push/subscription", account.GetPushSubscription)
	authGroupV1.PUT("/push/subscription", account.UpdatePushSubscription)
	authGroupV1.DELETE("/push/subscription", account.DeletePushSubscription)

	// 会话（写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteConversations})).
		DELETE("/conversations/:id", conversation.Remove).
		POST("/conversations/:id/read", conversation.MarkRead)

	// 过滤器（读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFilters})).
		GET("/filters", filter.List).
		GET("/filters/:id", filter.Show).
		GET("/filters/:id/keywords", filter.KeywordsList).
		GET("/filters/keywords/:id", filter.KeywordView)

	// 过滤器（写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFilters})).
		POST("/filters", filter.Create).
		PUT("/filters/:id", filter.Update).
		DELETE("/filters/:id", filter.Delete).
		POST("/filters/:filter_id/keywords", filter.KeywordCreate).
		PUT("/filters/keywords/:id", filter.KeywordUpdate).
		DELETE("/filters/keywords/:id", filter.KeywordDelete)

	// 举报（写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteReports})).
		POST("/reports", report2.Create)

	// ===== 管理员接口（v1）=====

	// 举报管理（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadReports})).
		GET("/admin/reports/", report2.List).
		GET("/admin/reports/:id", report2.GetReport)

	// 举报管理（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteReports})).
		PUT("/admin/reports/:id", report2.Update).
		DELETE("/admin/reports/:id", report2.Delete).
		POST("/admin/reports/:id/assign_to_self", report2.AssignToSelf).
		POST("/admin/reports/:id/unassign", report2.Unassign).
		POST("/admin/reports/:id/resolve", report2.Resolve).
		POST("/admin/reports/:id/reopen", report2.Reopen)

	// 规则管理（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteRules})).
		POST("/admin/rule", rule.Create).
		PUT("/admin/rule/:id", rule.Update).
		DELETE("/admin/rule/:id", rule.Delete)

	// 账户管理（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadAccounts})).
		GET("/admin/accounts", admin.ListAccounts).
		GET("/admin/accounts/:id", admin.GetAccount)

	// 账户管理（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteAccounts})).
		POST("/admin/accounts/:id/action", admin.PerformAccountAction).
		POST("/admin/accounts/:id/enable", admin.EnableAccount).
		POST("/admin/accounts/:id/approve", admin.ApproveAccount).
		POST("/admin/accounts/:id/reject", admin.RejectAccount).
		POST("/admin/accounts/:id/unsilence", admin.UnsilenceAccount).
		POST("/admin/accounts/:id/unsuspend", admin.UnsuspendAccount)

	// 状态管理（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/statuses/:id", admin.GetStatus)

	// 状态管理（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite})).
		POST("/admin/statuses/:id/action", admin.PerformStatusAction).
		DELETE("/admin/statuses/:id", admin.DeleteStatus)

	// 统计（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/dimensions", admin.GetDimensions).
		GET("/admin/measures", admin.GetMeasures).
		GET("/admin/retention", admin.GetRetention)

	// 自定义表情管理（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/custom_emojis", admin.ListCustomEmojis).
		GET("/admin/emoji_roles", admin.EmojiRoles).
		GET("/admin/custom_emojis/:id", admin.GetCustomEmoji)

	// 自定义表情管理（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite})).
		POST("/admin/custom_emojis", admin.CreateCustomEmoji).
		POST("/admin/custom_emojis/upload", admin.UploadCustomEmoji).
		POST("/admin/custom_emojis/batch", admin.BatchCustomEmojis).
		PUT("/admin/custom_emojis/:id", admin.UpdateCustomEmoji).
		DELETE("/admin/custom_emojis/:id", admin.DeleteCustomEmoji).
		POST("/admin/custom_emojis/:id/copy", admin.CopyCustomEmoji).
		POST("/admin/custom_emojis/:id/enable", admin.EnableCustomEmoji).
		POST("/admin/custom_emojis/:id/disable", admin.DisableCustomEmoji)

	// 中继管理（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/relays", admin.ListRelays).
		GET("/admin/relays/inboxes", admin.GetRelayInboxes).
		GET("/admin/relays/:id", admin.GetRelay)

	// 中继管理（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite})).
		POST("/admin/relays", admin.CreateRelay).
		DELETE("/admin/relays/:id", admin.DeleteRelay).
		POST("/admin/relays/:id/enable", admin.EnableRelay).
		POST("/admin/relays/:id/disable", admin.DisableRelay)

	// ===== 群组 API（需要登录）=====
	groupsGroup := v1ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	groupsGroup.POST("/groups", group2.CreateGroup)
	groupsGroup.GET("/groups", group2.GetMyGroups)
	groupsGroup.GET("/groups/:id", group2.GetGroup)
	groupsGroup.DELETE("/groups/:id", group2.DeleteGroup)
	groupsGroup.POST("/groups/:id/invite", group2.InviteMember)
	groupsGroup.DELETE("/groups/:id/members/:member_id", group2.RemoveMember)
	groupsGroup.GET("/groups/:id/messages", group2.GetGroupMessages)
	groupsGroup.POST("/groups/:id/messages", group2.SendGroupMessage)
	groupsGroup.POST("/groups/:id/bridge", group2.BridgeGroup)

	// ===== 联系人 API（需要登录）=====
	contactsGroup := v1ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	contactsGroup.POST("/contacts", contact.AddContact)
	contactsGroup.GET("/contacts", contact.GetContacts)
	contactsGroup.DELETE("/contacts/:id", contact.RemoveContact)
	contactsGroup.POST("/contacts/:id/block", contact.BlockContact)
	// 注意：静态路径 /contacts/requests 与 /contacts/lookup 须避免与参数路由冲突；
	// 此处仅有 DELETE /contacts/:id（HTTP 方法不同），故无冲突。
	contactsGroup.GET("/contacts/requests", contact.GetPendingRequests)
	contactsGroup.POST("/contacts/requests/:id/accept", contact.AcceptRequest)
	contactsGroup.POST("/contacts/requests/:id/reject", contact.RejectRequest)
	contactsGroup.GET("/contacts/lookup/matrix/:account_id", contact.LookupMatrixUser)
	contactsGroup.GET("/contacts/lookup/local", contact.SearchLocalUser)

	// ===== 向后兼容：v2 接口（deprecated）=====
	v2ApiGroup.POST("/accounts/register", account.Register)
	v2ApiGroup.GET("/accounts/verify_email", account.VerifyEmail)
	v2ApiGroup.POST("/accounts/login", account.Login)
	v2ApiGroup.POST("/accounts/reset_password", account.RecoverPasswordVerifyEmail)
	v2ApiGroup.POST("/accounts/reset_password/verify_token", account.RecoverPassword)
	v2ApiGroup.GET("/streaming/health", streaming.Health)

	// Search API v2（Mastodon 标准搜索端点，可选认证）
	v2ApiGroup.Group("", middleware.AuthMiddleware()).GET("/search", search.Search)

	// Suggestions API v2（需要登录）
	v2ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware()).GET("/suggestions", account.ListSuggestions)

	// 可登录也可不登录的路由（v2 兼容）
	maybeAuthGroup := v2ApiGroup.Group("", middleware.AuthMiddleware())
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

	// 需要登录的路由（v2 兼容）
	authGroup := v2ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())

	// 内部接口（仅 Web 端 token）
	authInternalGroup := authGroup.Group("", middleware.InternalApiMiddleware())
	authInternalGroup.GET("/accounts/logout", account.Logout)
	authInternalGroup.PUT("/accounts/me", account.UpdateAccount)
	authInternalGroup.PUT("/accounts/me/password", account.ChangePassword)

	// 通知（读取）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/notifications", timelines.NotificationTimeline)

	// 关注操作（写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFollows})).
		POST("/accounts/:id/follow", account.Follow).
		DELETE("/accounts/:id/follow", account.UnFollow).
		POST("/accounts/:id/unfollow", account.UnFollow) // Mastodon API

	// 列表（写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteLists})).
		POST("/lists", list.CreateList).
		PUT("/lists/:id", list.UpdateList).
		DELETE("/lists/:id", list.DeleteList).
		POST("/lists/:id/followers", list.FollowList).
		DELETE("/lists/:id/followers", list.UnfollowList).
		POST("/lists/:id/accounts", list.AddAccountToList).
		DELETE("/lists/:id/accounts", list.RemoveAccountFromList)

	// 列表（读取）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadLists})).
		GET("/lists", list.GetMyLists).
		GET("/lists/:id", list.GetList).
		GET("/lists/:id/accounts", list.GetListAccounts).
		GET("/lists/:id/followers", list.GetListFollowers)

	// 状态（写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses})).
		POST("/statuses", status.Create).
		DELETE("/statuses/:id", status.Delete).
		PUT("/statuses/:id", status.Update).
		POST("/statuses/:id/replace", status.Replace).
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

	// 状态（读取）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/statuses/:id/source", status.GetSource).
		GET("/statuses/:id/history", status.GetHistory).
		GET("/statuses/:id/favourited_by", status.FavouritedBy).
		GET("/statuses/:id/reblogged_by", status.RebloggedBy).
		GET("/polls/:id", status.ViewPoll).
		GET("/scheduled_statuses/:id", status.ViewSingleScheduledStatus).
		GET("/scheduled_statuses", status.ListScheduledStatuses).
		GET("/timelines/home", timelines.Home).
		GET("/timelines/mention", timelines.Mention)

	// 流式接口（读取状态 + 读取通知）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses},
		middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadNotifications})).
		GET("/streaming/user", streaming.UserStreaming).
		GET("/streaming/direct", streaming.DirectStreaming).
		GET("/streaming/notifications", streaming.NotificationStreaming)

	// 会话 / 标记（读取）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})).
		GET("/conversations", conversation.List).
		GET("/markers", marker.List)

	// 会话（写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteConversations})).
		DELETE("/conversations/:id", conversation.Remove).
		POST("/conversations/:id/read", conversation.MarkRead)

	// 过滤器（读取）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadFilters})).
		GET("/filters", filter.List).
		GET("/filters/:id", filter.Show).
		GET("/filters/:id/keywords", filter.KeywordsList).
		GET("/filters/keywords/:id", filter.KeywordView)

	// 过滤器（写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFilters})).
		POST("/filters", filter.Create).
		PUT("/filters/:id", filter.Update).
		DELETE("/filters/:id", filter.Delete).
		POST("/filters/:filter_id/keywords", filter.KeywordCreate).
		PUT("/filters/keywords/:id", filter.KeywordUpdate).
		DELETE("/filters/keywords/:id", filter.KeywordDelete)

	// 举报（写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteReports})).
		POST("/reports", report2.Create)

	// 举报管理（管理员读取）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadReports})).
		GET("/admin/reports/", report2.List).
		GET("/admin/reports/:id", report2.GetReport)

	// 举报管理（管理员写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteReports})).
		PUT("/admin/reports/:id", report2.Update).
		DELETE("/admin/reports/:id", report2.Delete).
		POST("/admin/reports/:id/assign_to_self", report2.AssignToSelf).
		POST("/admin/reports/:id/unassign", report2.Unassign).
		POST("/admin/reports/:id/resolve", report2.Resolve).
		POST("/admin/reports/:id/reopen", report2.Reopen)

	// 规则管理（管理员写入）
	authGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteRules})).
		POST("/admin/rule", rule.Create).
		PUT("/admin/rule/:id", rule.Update).
		DELETE("/admin/rule/:id", rule.Delete)

	// ===== Matrix Bridge 端点 =====
	// Matrix AS Webhook 端点（供 Homeserver 调用，内部通过 HS Token 鉴权）
	matrixWebhookHandler := matrix.NewWebhookHandler(nil)
	app.PUT("/_matrix/app/v1/transactions/:txnId", matrixWebhookHandler.HandleTransaction)
	// Note: GET /users/:userId 和 /rooms/:roomAlias 由 RegisterAppServiceRoutes 注册
	app.GET("/_matrix/health", matrixWebhookHandler.HandleHealthCheck)

	// ===== Matrix Homeserver 端点（Client-Server & Server-Server API）=====
	// 注册完整的 Matrix Client-Server API v1.17
	homeserver.RegisterRoutes(app)
	// 注册 Matrix Federation API v1.17
	homeserver.RegisterFederationRoutes(app)
	// 注册额外的联邦路由（Backfill 等）
	homeserver.RegisterBackfillRoutes(app)
	homeserver.RegisterAdditionalFederationRoutes(app)
	// 注册联邦 E2EE 路由（密钥查询、认领、To-Device 消息）
	homeserver.RegisterFederationE2EERoutes(app)
	// 注册关系 API（回复、反应、编辑、线程）
	safeRegisterRoutes(func() { homeserver.RegisterRelationsRoutes(app) })
	// 注册推送 API（Push Gateway）
	safeRegisterRoutes(func() { homeserver.RegisterPushRoutes(app) })
	// 注册 Spaces API
	safeRegisterRoutes(func() { homeserver.RegisterSpacesRoutes(app) })
	// 注册应用服务 API
	safeRegisterRoutes(func() { homeserver.RegisterAppServiceRoutes(app) })
	// 注册房间升级 API
	safeRegisterRoutes(func() { homeserver.RegisterRoomUpgradeRoutes(app) })
	// 注册密钥共享 API
	safeRegisterRoutes(func() { homeserver.RegisterKeySharingRoutes(app) })
	// 注册设备列表联邦 API
	safeRegisterRoutes(func() { homeserver.RegisterDeviceListRoutes(app) })
	// 注册批量发送 API (MSC2716)
	safeRegisterRoutes(func() { homeserver.RegisterBatchSendRoutes(app) })
	// 注册异步媒体 API (MSC2246)
	safeRegisterRoutes(func() { homeserver.RegisterAsyncMediaRoutes(app) })
	// 注册账户管理 API
	safeRegisterRoutes(func() { homeserver.RegisterAccountRoutes(app) })

	// ===== 矩阵管理 API（需要管理员权限）=====

	// 矩阵状态（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/matrix/status", admin.GetMatrixStatus).
		GET("/admin/matrix/config", admin.GetMatrixConfig)

	// 矩阵管理（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite})).
		POST("/admin/matrix/start", admin.StartMatrixBridge).
		POST("/admin/matrix/stop", admin.StopMatrixBridge).
		POST("/admin/matrix/migrate", admin.RunMatrixMigration).
		POST("/admin/matrix/test", admin.TestMatrixConnection)

	// ===== 缩略图管理 API（需要管理员权限）=====

	// 缩略图（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadThumbnails})).
		GET("/admin/thumbnails", admin.ListThumbnails).
		GET("/admin/thumbnails/:id", admin.GetThumbnail).
		GET("/admin/thumbnails/stats", admin.GetThumbnailStats).
		GET("/admin/thumbnails/config", admin.GetThumbnailConfig)

	// 缩略图（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWriteThumbnails})).
		PUT("/admin/thumbnails/config", admin.UpdateThumbnailConfig).
		DELETE("/admin/thumbnails/:id", admin.DeleteThumbnail).
		POST("/admin/thumbnails/:id/regenerate", admin.RegenerateThumbnail).
		POST("/admin/thumbnails/purge", admin.PurgeThumbnails)

	// ===== Captcha 管理 API（需要管理员权限）=====

	// Captcha 配置（管理员读取）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead})).
		GET("/admin/captcha/config", common.GetCaptchaConfig)

	// Captcha 配置（管理员写入）
	authGroupV1.Group("", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite})).
		PUT("/admin/captcha/config", common.AdminUpdateCaptchaConfig)
}

// safeRegisterRoutes 捕获重复路由注册时的 panic。
func safeRegisterRoutes(fn func()) {
	defer func() {
		_ = recover()
	}()

	fn()
}
