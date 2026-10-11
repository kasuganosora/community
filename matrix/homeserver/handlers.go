package homeserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// RegisterRoutes 注册 Matrix Client-Server API 路由.
func RegisterRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 服务器发现 (在 wellknown.go 中定义)
	// r.GET("/.well-known/matrix/server", handleWellKnownServer)
	// r.GET("/.well-known/matrix/client", handleWellKnownClient)

	// 注册 WellKnown 路由
	RegisterWellKnownRoutes(r)

	// 注册 3PID 路由
	RegisterThreePIDRoutes(r)

	// 注册管理员路由
	RegisterAdminRoutes(r)

	// 注册 OpenID 路由
	RegisterOpenIDRoutes(r)

	// 版本信息
	r.GET("/_matrix/client/versions", handleVersions)
	r.GET("/_matrix/client/r0/register/email/requestToken", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })
	// Note: /_matrix/federation/v1/version is registered in RegisterFederationRoutes

	// 认证相关（不需要 token）
	auth := r.Group("/_matrix/client/v3")
	auth.GET("/login", handleGetLoginFlows)
	auth.POST("/login", handleLogin)
	auth.POST("/refresh", handleRefresh)
	auth.POST("/register", handleRegister)
	auth.GET("/register", handleRegisterAvailability)
	auth.GET("/register/available", handleUsernameAvailable)

	// 公开 API（不需要 token）— Matrix spec 要求档案公开
	pub := r.Group("/_matrix/client/v3")
	pub.GET("/profile/:userId", handleGetProfile)
	pub.GET("/profile/:userId/displayname", handleGetDisplayName)
	pub.GET("/profile/:userId/avatar_url", handleGetAvatarURL)

	// 需要 token 的 API
	api := r.Group("/_matrix/client/v3")
	api.Use(matrixAuthMiddleware())
	RegisterCommunityConversationRoutes(api.Group("/community"))
	{
		// 登出（需要有效 token）
		api.POST("/logout", handleLogout)
		api.POST("/logout/all", handleLogoutAll)

		// 用户信息
		api.GET("/account/whoami", handleWhoami)
		api.GET("/account", handleAccount)
		api.POST("/account/deactivate", handleDeactivate)
		api.POST("/account/password", handleChangePassword)
		api.PUT("/profile/:userId/displayname", handleSetDisplayName)
		api.PUT("/profile/:userId/avatar_url", handleSetAvatarURL)

		// 房间管理
		api.POST("/createRoom", handleCreateRoom)
		api.GET("/rooms/:roomId", handleGetRoom)
		api.POST("/join/:roomIdOrAlias", handleJoinRoom)
		api.PUT("/rooms/:roomId/join", handleJoinRoom)
		api.POST("/rooms/:roomId/join", handleJoinRoom)
		api.POST("/rooms/:roomId/leave", handleLeaveRoom)
		api.POST("/rooms/:roomId/forget", handleForgetRoom)
		api.POST("/rooms/:roomId/invite", handleInviteUser)
		api.POST("/rooms/:roomId/kick", handleKickUser)
		api.POST("/rooms/:roomId/ban", handleBanUser)
		api.POST("/rooms/:roomId/unban", handleUnbanUser)
		api.GET("/rooms/:roomId/members", handleGetRoomMembers)
		api.GET("/rooms/:roomId/joined_members", handleGetJoinedMembers)
		api.GET("/rooms/:roomId/state", handleGetRoomState)
		api.GET("/rooms/:roomId/state/:eventType", handleGetRoomStateEvent)
		api.GET("/rooms/:roomId/state/:eventType/:stateKey", handleGetRoomStateEvent)
		api.PUT("/rooms/:roomId/state/:eventType", handleSendStateEvent)
		api.PUT("/rooms/:roomId/state/:eventType/:stateKey", handleSendStateEvent)

		// 事件发送
		api.PUT("/rooms/:roomId/send/:eventType/:txnId", handleSendEvent)
		api.PUT("/rooms/:roomId/send/:eventType", handleSendEvent)
		api.POST("/rooms/:roomId/send/:eventType/:txnId", handleSendEvent)
		api.POST("/rooms/:roomId/send/:eventType", handleSendEvent)

		// 消息
		api.GET("/rooms/:roomId/messages", handleGetMessages)
		api.GET("/rooms/:roomId/context/:eventId", handleGetEventContext)
		api.GET("/rooms/:roomId/event/:eventId", handleGetEvent)

		// 删除
		api.PUT("/rooms/:roomId/redact/:eventId/:txnId", handleRedactEvent)
		api.PUT("/rooms/:roomId/redact/:eventId", handleRedactEvent)

		// 回执
		api.POST("/rooms/:roomId/receipt/:receiptType/:eventId", handleSetReceipt)
		api.POST("/rooms/:roomId/read_markers", handleSetReadMarkers)

		// Typing
		api.PUT("/rooms/:roomId/typing/:userId", handleSetTyping)

		// 同步
		api.GET("/sync", handleSync)

		// 已加入房间
		api.GET("/joined_rooms", handleGetJoinedRooms)

		// 账户数据
		api.GET("/user/:userId/account_data/:type", handleGetAccountData)
		api.PUT("/user/:userId/account_data/:type", handleSetAccountData)
		api.GET("/user/:userId/rooms/:roomId/account_data/:type", handleGetRoomAccountData)
		api.PUT("/user/:userId/rooms/:roomId/account_data/:type", handleSetRoomAccountData)

		// 设备管理
		api.GET("/devices", handleGetDevices)
		api.GET("/devices/:deviceId", handleGetDevice)
		api.PUT("/devices/:deviceId", handleUpdateDevice)
		api.DELETE("/devices/:deviceId", handleDeleteDevice)

		// 过滤器
		api.POST("/user/:userId/filter", handleCreateFilter)
		api.GET("/user/:userId/filter/:filterId", handleGetFilter)

		// To-Device
		api.PUT("/sendToDevice/:eventType/:txnId", handleSendToDevice)
		api.GET("/device/one_time_keys_count", handleGetOneTimeKeysCount)
		api.POST("/keys/upload", handleKeysUpload)
		api.POST("/keys/query", handleKeysQuery)
		api.POST("/keys/claim", handleKeysClaim)
		api.POST("/keys/signatures/upload", handleKeysSignaturesUpload)
		api.POST("/keys/device_signing/upload", handleDeviceSigningUpload)
		api.GET("/keys/changes", handleGetKeyChanges)

		// Presence
		api.PUT("/presence/:userId/status", handleSetPresence)
		api.GET("/presence/:userId/status", handleGetPresence)

		// 公共房间
		api.GET("/publicRooms", handleGetPublicRoomsClient)
		api.POST("/publicRooms", handleGetPublicRoomsClient)

		// 能力
		api.GET("/capabilities", handleGetCapabilities)

		// 第三方网络
		api.GET("/thirdparty/protocols", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })
		api.GET("/thirdparty/protocol/:protocol", handleGetThirdPartyProtocol)
		api.GET("/thirdparty/user/:protocol", handleGetThirdPartyUser)
		api.GET("/thirdparty/location/:protocol", handleGetThirdPartyLocation)

		// VoIP / TURN
		api.GET("/voip/turnServer", handleGetTurnServer)

		// 搜索
		api.POST("/search", handleSearch)

		// 用户目录
		api.POST("/user_directory/search", handleUserDirectory)

		// 推送通知
		api.GET("/pushers", handleGetPushers)
		api.POST("/pushers/set", handleSetPusher)
		api.GET("/pushrules", handleGetPushRules)
		api.GET("/pushrules/", handleGetPushRules)
		api.GET("/pushrules/:scope", handleGetPushRulesGlobal)
		api.GET("/pushrules/:scope/:kind", handleGetPushRulesScope)
		api.GET("/pushrules/:scope/:kind/:ruleId", handleGetPushRule)
		api.PUT("/pushrules/:scope/:kind/:ruleId", handleSetPushRule)
		api.DELETE("/pushrules/:scope/:kind/:ruleId", handleDeletePushRule)
		api.GET("/pushrules/:scope/:kind/:ruleId/actions", handleGetPushRuleActions)
		api.PUT("/pushrules/:scope/:kind/:ruleId/actions", handleSetPushRuleActions)
		api.GET("/pushrules/:scope/:kind/:ruleId/enabled", handleGetPushRuleEnabled)
		api.PUT("/pushrules/:scope/:kind/:ruleId/enabled", handleSetPushRuleEnabled)

		// 房间别名
		api.GET("/directory/room/:roomAlias", handleGetRoomAlias)
		api.PUT("/directory/room/:roomAlias", handleSetRoomAlias)
		api.DELETE("/directory/room/:roomAlias", handleDeleteRoomAlias)
		api.GET("/rooms/:roomId/aliases", handleGetAliases)

		// 公共房间目录可见性
		api.GET("/directory/list/room/:roomId", handleGetRoomVisibility)
		api.PUT("/directory/list/room/:roomId", handleSetRoomVisibility)

		// 房间标签
		api.GET("/user/:userId/rooms/:roomId/tags", handleGetRoomTags)
		api.PUT("/user/:userId/rooms/:roomId/tags/:tag", handleSetRoomTag)
		api.DELETE("/user/:userId/rooms/:roomId/tags/:tag", handleDeleteRoomTag)

		// 举报
		api.POST("/rooms/:roomId/report/:eventId", handleReportRoom)

		// 房间摘要
		api.GET("/rooms/:roomId/summary", handleGetRoomSummary)
		api.GET("/room/:roomIdOrAlias", handleGetRoomSummary)

		// Knock
		api.POST("/knock/:roomIdOrAlias", handleKnockRoom)

		// 空间
		api.GET("/rooms/:roomId/hierarchy", handleGetSpaceHierarchy)
		api.GET("/spaces/:roomId", handleGetSpaceSummary)
		api.POST("/spaces/:roomId", handleGetSpaceSummaryClient)

		// 媒体（本地）
		api.POST("/media/upload", handleUploadMedia)
		api.GET("/media/download/:serverName/:mediaId", handleDownloadMedia)
		api.GET("/media/thumbnail/:serverName/:mediaId", handleThumbnailMedia)
		api.GET("/media/preview_url", handlePreviewURL)
		api.GET("/media/config", handleGetMediaConfig)
		api.GET("/media/config/:userId", handleGetMediaConfigPerUser)
	}

	// 媒体 (unstable endpoint)
	r.POST("/_matrix/media/v1/upload", matrixAuthMiddleware(), handleUploadMedia)
	r.GET("/_matrix/media/v1/download/:serverName/:mediaId", handleLegacyMediaDownload)
	r.GET("/_matrix/media/v1/thumbnail/:serverName/:mediaId", handleLegacyMediaDownload)
	r.GET("/_matrix/media/v1/preview_url", handlePreviewURL)

	// 媒体 (v3 endpoint)
	r.POST("/_matrix/media/v3/upload", matrixAuthMiddleware(), handleUploadMedia)
	r.GET("/_matrix/media/v3/download/:serverName/:mediaId", handleLegacyMediaDownload)
	r.GET("/_matrix/media/v3/thumbnail/:serverName/:mediaId", handleLegacyMediaDownload)
	r.GET("/_matrix/media/v3/preview_url", handlePreviewURL)

	// 媒体 (client/v1 authenticated endpoint - requires auth, returns 401 without token)
	clientV1Media := r.Group("/_matrix/client/v1/media")
	clientV1Media.Use(matrixAuthMiddleware())
	{
		// Use wildcard to handle malformed mxc:// prefix in path (e.g. /download/hs1/mxc://hs1/mediaId)
		clientV1Media.GET("/download/:serverName/*mediaPath", handleDownloadMediaV1)
		clientV1Media.GET("/thumbnail/:serverName/*mediaPath", handleThumbnailMediaV1)
		clientV1Media.POST("/upload", handleUploadMedia)
	}

	// r0 兼容 - unauthenticated endpoints
	r0pub := r.Group("/_matrix/client/r0")
	r0pub.GET("/login", handleGetLoginFlows)
	r0pub.POST("/login", handleLogin)
	r0pub.POST("/register", handleRegister)
	r0pub.GET("/register/available", handleUsernameAvailable)

	// r0 兼容 - authenticated endpoints
	r0 := r.Group("/_matrix/client/r0")
	r0.Use(matrixAuthMiddleware())
	{
		r0.GET("/sync", handleSync)
		r0.POST("/createRoom", handleCreateRoom)
		r0.PUT("/rooms/:roomId/join", handleJoinRoom)
		r0.POST("/rooms/:roomId/join", handleJoinRoom)
		r0.POST("/rooms/:roomId/leave", handleLeaveRoom)
		r0.PUT("/rooms/:roomId/send/:eventType/:txnId", handleSendEvent)
		r0.PUT("/rooms/:roomId/send/:eventType", handleSendEvent)
		r0.POST("/rooms/:roomId/send/:eventType/:txnId", handleSendEvent)
		r0.POST("/rooms/:roomId/send/:eventType", handleSendEvent)
		r0.GET("/rooms/:roomId/messages", handleGetMessages)
		r0.GET("/rooms/:roomId/state", handleGetRoomState)
		r0.GET("/rooms/:roomId/state/:eventType/:stateKey", handleGetRoomStateEvent)
		r0.GET("/rooms/:roomId/state/:eventType", handleGetRoomStateEvent)
		r0.GET("/rooms/:roomId/members", handleGetRoomMembers)
		r0.GET("/rooms/:roomId/joined_members", handleGetJoinedMembers)
		r0.GET("/rooms/:roomId/event/:eventId", handleGetEvent)
		r0.POST("/rooms/:roomId/invite", handleInviteUser)
		r0.POST("/rooms/:roomId/kick", handleKickUser)
		r0.POST("/rooms/:roomId/ban", handleBanUser)
		r0.POST("/rooms/:roomId/unban", handleUnbanUser)
		r0.POST("/rooms/:roomId/forget", handleForgetRoom)
		r0.POST("/rooms/:roomId/receipt/:receiptType/:eventId", handleSetReceipt)
		r0.PUT("/rooms/:roomId/typing/:userId", handleSetTyping)
		r0.GET("/joined_rooms", handleGetJoinedRooms)
		r0.GET("/profile/:userId", handleGetProfile)
		r0.GET("/profile/:userId/displayname", handleGetDisplayName)
		r0.PUT("/profile/:userId/displayname", handleSetDisplayName)
		r0.GET("/profile/:userId/avatar_url", handleGetAvatarURL)
		r0.PUT("/profile/:userId/avatar_url", handleSetAvatarURL)
		r0.GET("/publicRooms", handleGetPublicRoomsClient)
		r0.POST("/publicRooms", handleGetPublicRoomsClient)
		r0.GET("/directory/room/:roomAlias", handleGetRoomAlias)
		r0.PUT("/directory/room/:roomAlias", handleSetRoomAlias)
		r0.DELETE("/directory/room/:roomAlias", handleDeleteRoomAlias)
		r0.GET("/directory/list/room/:roomId", handleGetRoomVisibility)
		r0.PUT("/directory/list/room/:roomId", handleSetRoomVisibility)
		r0.POST("/keys/upload", handleKeysUpload)
		r0.POST("/keys/query", handleKeysQuery)
		r0.POST("/keys/claim", handleKeysClaim)
		r0.POST("/keys/device_signing/upload", handleDeviceSigningUpload)
		r0.POST("/keys/signatures/upload", handleKeysSignaturesUpload)
		r0.GET("/keys/changes", handleGetKeyChanges)
		r0.PUT("/sendToDevice/:eventType/:txnId", handleSendToDevice)
		r0.GET("/capabilities", handleGetCapabilities)
		r0.GET("/account/whoami", handleWhoami)
	}
}

// ===== 中间件 =====

// matrixErrorStatus maps a MatrixError code to the appropriate HTTP status code.
func matrixErrorStatus(err *MatrixError) int {
	switch err.ErrCode {
	case "M_FORBIDDEN", "M_USER_DEACTIVATED": //nolint:goconst
		return http.StatusForbidden
	case "M_NOT_FOUND": //nolint:goconst
		return http.StatusNotFound
	case "M_UNAUTHORIZED", "M_UNKNOWN_TOKEN", "M_MISSING_TOKEN": //nolint:goconst
		return http.StatusUnauthorized
	case "M_BAD_GATEWAY":
		return http.StatusBadGateway
	case "M_INTERNAL":
		return http.StatusInternalServerError
	case "M_LIMIT_EXCEEDED":
		return http.StatusTooManyRequests
	case "M_TOO_LARGE": //nolint:goconst
		return http.StatusRequestEntityTooLarge
	case "M_INCOMPATIBLE_ROOM_VERSION", "M_UNSUPPORTED_ROOM_VERSION", "M_UNABLE_TO_AUTHORISE_JOIN", "M_UNABLE_TO_GRANT_JOIN":
		return http.StatusBadRequest
	case "M_USER_IN_USE", "M_INVALID_USERNAME", "M_EXCLUSIVE": //nolint:goconst
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

func matrixAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			auth = c.Query("access_token")
		} else {
			// Bearer token
			auth = strings.TrimPrefix(auth, "Bearer ")
		}

		if auth == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"errcode": "M_MISSING_TOKEN",      //nolint:goconst
				"error":   "Missing access token", //nolint:goconst
			})
			c.Abort()

			return
		}

		hs := GetHomeServer()

		session, err := hs.Auth.GetDeviceByToken(auth)
		if err != nil {
			c.JSON(http.StatusUnauthorized, err)
			c.Abort()

			return
		}

		// 存储用户信息
		c.Set("user_id", session.UserID)
		c.Set("device_id", session.DeviceID)
		c.Set("access_token", auth)
		c.Next()
	}
}

func getUserID(c *gin.Context) string {
	if userID, exists := c.Get("user_id"); exists {
		return userID.(string) //nolint:forcetypeassert
	}

	return ""
}

// getUserLeaveTS returns the timestamp when the user left the room, or 0 if not left.
func getUserLeaveTS(roomID, userID string) int64 {
	var member MatrixRoomMember
	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err != nil {
		return 0
	}

	if member.Membership != MembershipLeave && member.Membership != MembershipBan {
		return 0
	}

	if member.EventID == "" {
		return 0
	}

	var event MatrixEvent
	err = db.GetDB().Where("event_id = ?", member.EventID).First(&event).Error
	if err != nil {
		return 0
	}

	return event.OriginServerTS
}

// getStateAtTime returns a state event as it was at or before the given timestamp.
func getStateAtTime(roomID, eventType, stateKey string, ts int64) (*ClientEvent, *MatrixError) {
	// Find the most recent state event of this type that was set at or before ts
	var event MatrixEvent

	err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ? AND origin_server_ts <= ?",
		roomID, eventType, stateKey, ts).
		Order("origin_server_ts DESC").First(&event).Error
	if err != nil {
		return nil, ErrNotFound
	}

	return event.ToClientEvent(), nil
}

// getFullStateAtTime returns all state events for a room as they were at or before the given timestamp.
func getFullStateAtTime(roomID string, ts int64) []*ClientEvent {
	var stateEntries []MatrixRoomState

	db.GetDB().Where("room_id = ?", roomID).Find(&stateEntries)

	var result []*ClientEvent

	for _, entry := range stateEntries {
		// Find the version of this state event that was valid at `ts`
		var event MatrixEvent
		err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ? AND origin_server_ts <= ?",
			roomID, entry.EventType, entry.StateKey, ts).
			Order("origin_server_ts DESC").First(&event).Error
		if err == nil {
			result = append(result, event.ToClientEvent())
		}
	}

	return result
}

// ===== 服务器发现 =====
// 在 wellknown.go 中定义

// handleFederationVersion 在 federation.go 中定义

func handleVersions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"versions": []string{
			"v1.0",
			"v1.1",
			"v1.2",
			"v1.3",
			"v1.4",
			"v1.5",
			"v1.6",
			"v1.7",
			"v1.8",
			"v1.9",
			"v1.10",
			"v1.11",
		},
		"unstable_features": gin.H{
			"org.matrix.e2e_cross_signing": true,
			"org.matrix.msc2815":           true,
			"org.matrix.msc3882":           true,
		},
	})
}

// handleFederationVersion is defined in federation.go

// ===== 认证处理 =====

func handleGetLoginFlows(c *gin.Context) {
	hs := GetHomeServer()
	flowBytes, _ := json.Marshal(hs.Auth.GetLoginFlows()) //nolint:errchkjson // deterministic serialization
	c.Data(http.StatusOK, "application/json", flowBytes)
}

func handleLogin(c *gin.Context) {
	var req LoginRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON", //nolint:goconst
			"error":   "Bad JSON",   //nolint:goconst
		})

		return
	}

	hs := GetHomeServer()

	resp, matErr := hs.Auth.Login(&req)
	if matErr != nil {
		status := http.StatusUnauthorized

		switch matErr.ErrCode {
		case "M_FORBIDDEN", "M_USER_DEACTIVATED":
			status = http.StatusForbidden
		case "M_BAD_JSON", "M_INVALID_PARAM": //nolint:goconst
			status = http.StatusBadRequest
		}

		c.Header("Content-Type", "application/json")
		c.JSON(status, matErr)

		return
	}

	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, resp)
}

func handleLogout(c *gin.Context) {
	token := c.GetString("access_token")
	hs := GetHomeServer()
	hs.Auth.Logout(token)
	c.JSON(http.StatusOK, gin.H{})
}

func handleLogoutAll(c *gin.Context) {
	token := c.GetString("access_token")
	hs := GetHomeServer()
	hs.Auth.LogoutAll(token)
	c.JSON(http.StatusOK, gin.H{})
}

func handleRefresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()

	resp, err := hs.Auth.Refresh(&req)
	if err != nil {
		c.JSON(http.StatusUnauthorized, err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleRegister(c *gin.Context) {
	// Validate UTF-8 before JSON parsing (encoding/json silently replaces invalid UTF-8)
	body, readErr := io.ReadAll(c.Request.Body)
	if readErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Failed to read request body"})

		return
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > 0 && !utf8.Valid(body) {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_NOT_JSON",
			"error":   "Request body contains invalid UTF-8",
		})

		return
	}

	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()

	// Check if UIA is needed
	if uia := hs.Auth.NeedsUIAuth(&req); uia != nil {
		// Must return exactly "application/json" (no charset) per Matrix spec.
		// c.JSON() writes "application/json; charset=utf-8", so write manually.
		uiaBytes, err := json.Marshal(uia)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			return
		}
		c.Data(http.StatusUnauthorized, "application/json", uiaBytes)

		return
	}

	resp, err := hs.Auth.Register(&req, false)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleRegisterAvailability(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"registration_available": true,
	})
}

func handleUsernameAvailable(c *gin.Context) {
	username := strings.ToLower(strings.TrimSpace(c.Query("username")))

	if username == "" || len(username) > 20 || !validLocalpartRe.MatchString(username) || models.IsReservedBridgeUsername(username) {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_USERNAME",
			"error":   "Invalid username",
		})

		return
	}

	hs := GetHomeServer()

	if hs.Auth.UserExists(username) {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_USER_IN_USE",
			"error":   "Username already in use",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"available": true,
	})
}

// handleWhoami 在 account.go 中已实现

func handleAccount(c *gin.Context) {
	userID := getUserID(c)
	c.JSON(http.StatusOK, gin.H{
		"mxid": userID,
	})
}

// ===== 房间处理 =====

func handleCreateRoom(c *gin.Context) {
	var req CreateRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	userID := getUserID(c)
	hs := GetHomeServer()

	resp, err := hs.Room.CreateRoom(userID, &req)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleGetRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	c.JSON(http.StatusOK, gin.H{
		"room_id": roomID, //nolint:goconst
	})
}

func handleJoinRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	if roomID == "" {
		roomID = c.Param("roomIdOrAlias")
	}

	userID := getUserID(c)

	// Parse optional request body — any extra keys are merged into the join event
	var body map[string]any

	_ = c.ShouldBindJSON(&body)

	// Accept the current via parameter and legacy server_name hints.
	servers := append(c.QueryArray("via"), c.QueryArray("server_name")...)

	hs := GetHomeServer()

	roomID, err := hs.Room.JoinRoom(userID, roomID, &JoinRoomRequest{AdditionalContent: body, Servers: servers})
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"room_id": roomID,
	})
}

func handleLeaveRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	hs := GetHomeServer()

	// Check if this is a server notice room (created by @_server: bot).
	// Users cannot reject invites to server notice rooms.
	var member MatrixRoomMember
	if db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error == nil {
		if member.Membership == MembershipInvite {
			botUserID := "@_server:" + hs.Config.ServerName

			var createEvent MatrixEvent

			if db.GetDB().Where("room_id = ? AND event_type = ?", roomID, "m.room.create").First(&createEvent).Error == nil {
				if creator, ok := createEvent.Content["creator"].(string); ok && creator == botUserID {
					c.JSON(http.StatusForbidden, gin.H{
						"errcode": "M_CANNOT_LEAVE_SERVER_NOTICE_ROOM",
						"error":   "You cannot reject this invite",
					})

					return
				}
			}
		}
	}

	if err := hs.Room.LeaveRoom(userID, roomID, &LeaveRoomRequest{}); err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func handleForgetRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	// 检查用户必须已经离开房间
	var member MatrixRoomMember
	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Error("forget room %s: %v", roomID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_UNKNOWN", "error": "Internal server error"})

			return
		}
		// 如果找不到成员记录，已经遗忘了
		c.JSON(http.StatusOK, gin.H{})

		return
	}

	if member.Membership != MembershipLeave && member.Membership != MembershipBan {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_UNKNOWN", //nolint:goconst
			"error":   "You must leave the room before you can forget it",
		})

		return
	}

	// Mark as forgotten (don't delete, so we can check forgotten status in access control)
	db.GetDB().Model(&member).Update("forgotten", true)

	c.JSON(http.StatusOK, gin.H{})
}

func handleInviteUser(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	var req InviteUserRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()
	if err := hs.Room.InviteUser(roomID, userID, &req); err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func handleKickUser(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	var req KickUserRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()
	if err := hs.Room.KickUser(roomID, userID, &req); err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func handleBanUser(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	var req BanUserRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()
	if err := hs.Room.BanUser(roomID, userID, &req); err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleUnbanUser 解封用户.
func handleUnbanUser(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	var req struct {
		UserID string `json:"user_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	// 检查权限
	var member MatrixRoomMember

	err := db.GetDB().Where("room_id = ? AND user_id = ? AND membership = ?",
		roomID, userID, "join").First(&member).Error
	if err != nil || member.PowerLevel < 50 {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Not authorized",
		})

		return
	}

	// 移除封禁状态
	result := db.GetDB().Model(&MatrixRoomMember{}).
		Where("room_id = ? AND user_id = ? AND membership = ?", roomID, req.UserID, "ban").
		Update("membership", "leave")

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User is not banned",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func handleGetRoomMembers(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	hs := GetHomeServer()

	// Check caller is a member (or has historical access)
	callerMembership, _ := hs.Room.GetUserMembership(roomID, userID)
	if callerMembership != MembershipJoin {
		// Allow if they were previously a member (leave/ban)
		var count int64

		db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", roomID, userID).Count(&count)

		if count == 0 {
			c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": "You are not in this room"})

			return
		}
	}

	membership := c.QueryArray("membership")
	notMembership := c.QueryArray("not_membership")

	var membershipStates []MembershipState
	for _, m := range membership {
		membershipStates = append(membershipStates, MembershipState(m))
	}

	members, matErr := hs.Room.GetRoomMembers(roomID, membershipStates)
	if matErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": matErr.ErrCode, "error": matErr.Error})

		return
	}

	// Apply not_membership filter
	if len(notMembership) > 0 {
		notSet := make(map[string]bool, len(notMembership))
		for _, nm := range notMembership {
			notSet[nm] = true
		}

		filtered := members[:0]

		for _, m := range members {
			if !notSet[string(m.Membership)] {
				filtered = append(filtered, m)
			}
		}

		members = filtered
	}

	// Determine the point-in-time cutoff timestamp
	var cutoffTS int64

	if atToken := c.Query("at"); atToken != "" {
		// Support both s<ts> (sync token) and t<ts>_<idx> (pagination token) formats
		if len(atToken) > 1 && (atToken[0] == 's' || atToken[0] == 't') {
			tsStr := atToken[1:]
			// Strip _<index> suffix from pagination tokens (e.g. t1234_0 -> 1234)
			if idx := strings.Index(tsStr, "_"); idx > 0 {
				tsStr = tsStr[:idx]
			}

			if ts, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
				cutoffTS = ts
			}
		}
	}
	// For departed users without explicit at param, use leave timestamp
	if cutoffTS == 0 && callerMembership != MembershipJoin {
		cutoffTS = getUserLeaveTS(roomID, userID)
	}
	// Apply point-in-time filter
	if cutoffTS > 0 {
		filtered := members[:0]

		for _, m := range members {
			if m.EventID != "" {
				var evt MatrixEvent
				if db.GetDB().Where("event_id = ?", m.EventID).First(&evt).Error == nil {
					if evt.OriginServerTS <= cutoffTS {
						filtered = append(filtered, m)

						continue
					}
					// Skip members whose event is after the cutoff
					continue
				}
			}
			// If no event or timestamp, include by default (created before sync)
			filtered = append(filtered, m)
		}

		members = filtered
	}

	chunk := make([]gin.H, 0, len(members))

	for _, m := range members {
		content := gin.H{"membership": string(m.Membership)} //nolint:goconst
		if m.DisplayName != nil {
			content["displayname"] = *m.DisplayName
		}

		if m.AvatarURL != nil {
			content["avatar_url"] = *m.AvatarURL
		}

		stateKey := m.UserID
		evt := gin.H{
			"type":             "m.room.member", //nolint:goconst
			"event_id":         m.EventID,       //nolint:goconst
			"room_id":          m.RoomID,
			"sender":           m.UserID, //nolint:goconst
			"state_key":        stateKey, //nolint:goconst
			"content":          content,  //nolint:goconst
			"origin_server_ts": int64(0), //nolint:goconst
		}
		// Enrich origin_server_ts from the event record if available
		if m.EventID != "" {
			var evt2 MatrixEvent
			if db.GetDB().Where("event_id = ?", m.EventID).First(&evt2).Error == nil {
				evt["origin_server_ts"] = evt2.OriginServerTS
				evt["sender"] = evt2.Sender
			}
		}

		chunk = append(chunk, evt)
	}

	c.JSON(http.StatusOK, gin.H{
		"chunk": chunk,
	})
}

func handleGetJoinedMembers(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	hs := GetHomeServer()

	// 检查请求者是否是房间成员
	var requesterMember MatrixRoomMember
	err := db.GetDB().Where("room_id = ? AND user_id = ? AND membership = ?", roomID, userID, MembershipJoin).First(&requesterMember).Error
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": "Not a member of this room"})

		return
	}

	var members []MatrixRoomMember

	db.GetDB().Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Find(&members)

	joined := make(map[string]gin.H, len(members))

	for _, m := range members {
		info := gin.H{
			"display_name": nil,
			"avatar_url":   nil,
		}
		if m.DisplayName != nil {
			info["display_name"] = *m.DisplayName
		}

		if m.AvatarURL != nil {
			info["avatar_url"] = *m.AvatarURL
		}
		// Enrich from account
		localpart, _, ok := ParseMatrixUserID(m.UserID)
		if ok {
			var account models.Account
			if db.GetDB().Where("username = ?", localpart).First(&account).Error == nil {
				if account.DisplayName != "" {
					info["display_name"] = account.DisplayName
				}

				if account.Avatar != "" {
					info["avatar_url"] = fmt.Sprintf("mxc://%s/%s", hs.Config.ServerName, account.Avatar)
				}
			}
		}

		joined[m.UserID] = info
	}

	c.JSON(http.StatusOK, gin.H{"joined": joined})
}

func handleGetRoomState(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	hs := GetHomeServer()

	// Only members (or former members) can view room state
	membership, _ := hs.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		var count int64

		db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", roomID, userID).Count(&count)

		if count == 0 {
			c.JSON(http.StatusForbidden, MatrixError{ErrCode: "M_FORBIDDEN", Error: "You are not in this room"})

			return
		}
	}

	// For departed users, return state at the time of their leave
	if membership != MembershipJoin {
		leaveTS := getUserLeaveTS(roomID, userID)
		if leaveTS > 0 {
			events := getFullStateAtTime(roomID, leaveTS)
			c.JSON(http.StatusOK, events)

			return
		}
	}

	events, err := hs.Room.GetRoomStateAll(roomID)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, events)
}

func handleGetRoomStateEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventType := c.Param("eventType")
	stateKey := c.Param("stateKey")
	userID := getUserID(c)

	hs := GetHomeServer()

	// Check caller is a member of the room
	membership, _ := hs.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		var count int64

		db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", roomID, userID).Count(&count)

		if count == 0 {
			c.JSON(http.StatusForbidden, MatrixError{ErrCode: "M_FORBIDDEN", Error: "You are not in this room"})

			return
		}
	}

	// For departed users, return state at the time of their leave
	if membership != MembershipJoin {
		leaveTS := getUserLeaveTS(roomID, userID)
		if leaveTS > 0 {
			event, err := getStateAtTime(roomID, eventType, stateKey, leaveTS)
			if err != nil {
				c.JSON(http.StatusNotFound, err)

				return
			}

			if c.Query("format") == "event" {
				c.JSON(http.StatusOK, event)

				return
			}

			c.JSON(http.StatusOK, event.Content)

			return
		}
	}

	event, err := hs.Event.GetRoomStateEvent(roomID, eventType, stateKey)
	if err != nil {
		c.JSON(http.StatusNotFound, err)

		return
	}

	// ?format=event returns the full event envelope
	if c.Query("format") == "event" {
		if event.EventID != "" {
			var fullEvent MatrixEvent
			if db.GetDB().Where("event_id = ?", event.EventID).First(&fullEvent).Error == nil {
				c.JSON(http.StatusOK, fullEvent.ToClientEvent())

				return
			}
		}

		c.JSON(http.StatusOK, event)

		return
	}

	// Matrix spec: GET /state/:eventType returns only the event content by default
	c.JSON(http.StatusOK, event.Content)
}

func handleSendStateEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventType := c.Param("eventType")
	stateKey := c.Param("stateKey")
	userID := getUserID(c)

	// Matrix spec: events must be at most 65536 bytes
	// Read body to check actual size (ContentLength may be -1 for chunked encoding)
	body, _ := io.ReadAll(c.Request.Body)

	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	if len(body) > 65536 {
		c.JSON(http.StatusRequestEntityTooLarge, MatrixError{ErrCode: "M_TOO_LARGE", Error: "Event too large"})

		return
	}

	var content map[string]any
	if err := c.ShouldBindJSON(&content); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()

	// Validate m.room.canonical_alias: aliases must exist and point to this room
	if eventType == string(EventTypeRoomCanonicalAlias) {
		aliasService := NewAliasService(hs)
		// Helper: validate alias format (#localpart:server)
		isValidAliasFormat := func(a string) bool {
			return strings.HasPrefix(a, "#") && strings.Contains(a, ":")
		}
		if alias, ok := content["alias"].(string); ok && alias != "" {
			if !isValidAliasFormat(alias) {
				c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_INVALID_PARAM", "error": "Invalid alias format"})

				return
			}

			resolvedRoomID, err := aliasService.GetRoomIDByAlias(alias)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_ALIAS", "error": "Unknown alias"}) //nolint:goconst

				return
			}

			if resolvedRoomID != roomID {
				c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_ALIAS", "error": "Alias points to a different room"})

				return
			}
		}

		if altAliases, ok := content["alt_aliases"].([]any); ok {
			for _, a := range altAliases {
				altAlias, ok := a.(string)
				if !ok || !isValidAliasFormat(altAlias) {
					c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_INVALID_PARAM", "error": "Invalid alt_alias"})

					return
				}

				resolvedRoomID, err := aliasService.GetRoomIDByAlias(altAlias)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_ALIAS", "error": "Unknown alt_alias: " + altAlias})

					return
				}

				if resolvedRoomID != roomID {
					c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_ALIAS", "error": "Alt alias points to a different room"})

					return
				}
			}
		}
	}

	resp, err := hs.Event.SendRoomEvent(roomID, userID, eventType, nil, content, &stateKey)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

// ===== 事件处理 =====

func handleSendEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventType := c.Param("eventType")
	txnID := c.Param("txnId")
	userID := getUserID(c)
	deviceID := c.GetString("device_id")

	// Matrix spec: events must be at most 65536 bytes
	// Read body to check actual size (ContentLength may be -1 for chunked encoding)
	body, _ := io.ReadAll(c.Request.Body)

	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	if len(body) > 65536 {
		c.JSON(http.StatusRequestEntityTooLarge, MatrixError{ErrCode: "M_TOO_LARGE", Error: "Event too large"})

		return
	}

	var content map[string]any
	if err := c.ShouldBindJSON(&content); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()

	var txnIDPtr *string
	if txnID != "" {
		txnIDPtr = &txnID
	}

	resp, err := hs.Event.SendRoomEventFromDevice(roomID, userID, deviceID, eventType, txnIDPtr, content, nil)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleGetMessages(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	req := &GetRoomMessagesRequest{
		Context: c.Request.Context(),
		Dir:     c.Query("dir"),
		From:    c.Query("from"),
		To:      c.Query("to"),
		Filter:  c.Query("filter"),
	}

	if limit := c.Query("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil {
			req.Limit = l
		}
	}

	hs := GetHomeServer()

	resp, err := hs.Event.GetRoomMessages(roomID, userID, req)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleGetEventContext(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	userID := getUserID(c)

	limit := 10

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	hs := GetHomeServer()

	resp, err := hs.Event.GetRoomContextContext(c.Request.Context(), roomID, eventID, userID, limit)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleGetEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	userID := getUserID(c)
	deviceID := c.GetString("device_id")

	hs := GetHomeServer()

	event, err := hs.Event.GetRoomEventForDevice(roomID, eventID, deviceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "Event not found"}) //nolint:goconst

		return
	}

	// Check history visibility access control
	if !canAccessEvent(hs, roomID, userID, event) {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "Event not found"})

		return
	}

	c.JSON(http.StatusOK, event)
}

// canAccessEvent checks if a user can access a specific event based on history visibility.
func canAccessEvent(hs *HomeServer, roomID, userID string, event *ClientEvent) bool {
	if event == nil || event.RoomID != roomID {
		return false
	}
	return canReadClientEvent(roomID, userID, event.EventID)
}

func handleRedactEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	txnID := c.Param("txnId")
	userID := getUserID(c)

	var req struct {
		Reason string `json:"reason"`
	}
	// Reason is optional for redaction; ignore bind errors
	_ = c.ShouldBindJSON(&req)

	hs := GetHomeServer()

	var txnIDPtr *string
	if txnID != "" {
		txnIDPtr = &txnID
	}

	resp, err := hs.Event.RedactEvent(roomID, eventID, userID, req.Reason, txnIDPtr)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, resp)
}

// ===== 同步 =====

func handleSync(c *gin.Context) {
	userID := getUserID(c)
	deviceID, _ := c.Get("device_id")
	deviceIDStr, _ := deviceID.(string)

	req := &SyncRequest{
		Filter:      c.Query("filter"),
		Since:       c.Query("since"),
		FullState:   c.Query("full_state") == "true",
		SetPresence: c.Query("set_presence"),
	}

	if timeout := c.Query("timeout"); timeout != "" {
		if t, err := strconv.Atoi(timeout); err == nil {
			req.Timeout = min(max(t, 0), 30000)
		}
	}

	hs := GetHomeServer()

	// Register for notifications BEFORE running the sync query to avoid a race
	// condition where a notification fires between Sync() returning empty and
	// Wait() being called. Without this, the notification channel would be
	// created too late and the notification would be lost.
	var notify <-chan struct{}
	if req.Timeout > 0 {
		var release func()
		notify, release = hs.SyncNotifier.Subscribe(userID, deviceIDStr)
		defer release()
	}

	resp, err := hs.Sync.SyncContext(c.Request.Context(), userID, deviceIDStr, req)
	if err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	// Long-polling: if timeout > 0 and no meaningful data, wait for new events
	if req.Timeout > 0 && !syncResponseHasData(resp) {
		timer := time.NewTimer(time.Duration(req.Timeout) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-notify:
			// Re-run sync with same since token to pick up new data
			resp, err = hs.Sync.SyncContext(c.Request.Context(), userID, deviceIDStr, req)
			if err != nil {
				c.JSON(matrixErrorStatus(err), err)

				return
			}
		case <-timer.C:
			// Return current (empty) response after timeout
		case <-c.Request.Context().Done():
			return
		}
	}

	c.JSON(http.StatusOK, resp)
}

// syncResponseHasData returns true if the sync response contains meaningful new data
// that should be delivered immediately (no need to long-poll).
// Presence is excluded because setPresence() in Sync() always updates the user's own
// last_active_ts, so the user's own presence event is always included in incremental syncs.
// Including it here would prevent long-polling from ever waiting.
func syncResponseHasData(resp *SyncResponse) bool {
	if resp.Rooms != nil {
		if len(resp.Rooms.Join) > 0 || len(resp.Rooms.Invite) > 0 || len(resp.Rooms.Leave) > 0 {
			return true
		}
	}

	if resp.ToDevice != nil && len(resp.ToDevice.Events) > 0 {
		return true
	}

	if resp.AccountData != nil && len(resp.AccountData.Events) > 0 {
		return true
	}

	if len(resp.DeviceLists.Changed) > 0 || len(resp.DeviceLists.Left) > 0 {
		return true
	}

	return false
}

// ===== 回执 =====

func handleSetReceipt(c *gin.Context) {
	roomID := c.Param("roomId")
	receiptType := c.Param("receiptType")
	eventID := c.Param("eventId")
	userID := getUserID(c)

	// Parse optional thread_id from request body
	var body struct {
		ThreadID string `json:"thread_id"`
	}

	c.ShouldBindJSON(&body) //nolint:errcheck,gosec // G104: body may be empty

	hs := GetHomeServer()
	if err := hs.Sync.SetReceiptWithThread(roomID, eventID, userID, receiptType, body.ThreadID); err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func handleSetReadMarkers(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	var req struct {
		Read        string `json:"m.read"`
		ReadPrivate string `json:"m.read.private"`
		FullyRead   string `json:"m.fully_read"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()
	if req.Read != "" {
		hs.Sync.SetReceipt(roomID, req.Read, userID, "m.read")
	}

	if req.ReadPrivate != "" {
		hs.Sync.SetReceipt(roomID, req.ReadPrivate, userID, "m.read.private")
	}

	if req.FullyRead != "" {
		// Store m.fully_read as room account data
		roomIDCopy := roomID
		_ = hs.Account.SetAccountData(userID, "m.fully_read", &roomIDCopy, map[string]any{
			"event_id": req.FullyRead,
		})
	}

	c.JSON(http.StatusOK, gin.H{})
}

// ===== Typing =====

func handleSetTyping(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)

	var req struct {
		Typing  bool  `json:"typing"`
		Timeout int64 `json:"timeout"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()
	if err := hs.Sync.SetTyping(roomID, userID, req.Typing, req.Timeout); err != nil {
		c.JSON(matrixErrorStatus(err), err)

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// ===== 用户档案 =====

func handleGetProfile(c *gin.Context) {
	userID := c.Param("userId")

	hs := GetHomeServer()
	if profile, ok := hs.bridgeGhostProfile(c.Request.Context(), userID); ok {
		c.JSON(http.StatusOK, profile)
		return
	}
	// 获取用户档案
	accountID, err := hs.Auth.GetLocalAccountID(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found", //nolint:goconst
		})

		return
	}

	// 获取社区账户信息
	var account models.Account
	if err := db.GetDB().First(&account, accountID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
		})

		return
	}

	avatarURL := hs.bridgeCompatibleProfileAvatar(c.Request.Context(), userID, account)

	c.JSON(http.StatusOK, gin.H{
		"displayname": account.DisplayName,
		"avatar_url":  avatarURL,
	})
}

func handleGetDisplayName(c *gin.Context) {
	targetUserID := c.Param("userId")
	if profile, ok := GetHomeServer().bridgeGhostProfile(c.Request.Context(), targetUserID); ok {
		c.JSON(http.StatusOK, gin.H{"displayname": profile["displayname"]})
		return
	}

	localpart, _, ok := ParseMatrixUserID(targetUserID)

	if !ok {
		localpart = targetUserID
	}

	var account models.Account
	err := db.GetDB().Where("username = ?", localpart).First(&account).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "User not found"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"displayname": account.DisplayName})
}

func handleSetDisplayName(c *gin.Context) {
	userID := c.Param("userId")

	callerID := getUserID(c)

	if callerID == "" || userID != callerID {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": "Cannot set another user's displayname"})

		return
	}

	var req struct {
		DisplayName string `json:"displayname"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Bad JSON"})

		return
	}

	localpart, _, ok := ParseMatrixUserID(userID)
	if !ok {
		localpart = userID
	}

	db.GetDB().Model(&models.Account{}).Where("username = ?", localpart).Update("display_name", req.DisplayName)

	// Propagate displayname change to m.room.member events in all joined rooms
	hs := GetHomeServer()
	propagateProfileChange(hs, userID, "displayname", req.DisplayName)

	c.JSON(http.StatusOK, gin.H{})
}

func handleGetAvatarURL(c *gin.Context) {
	targetUserID := c.Param("userId")
	if profile, ok := GetHomeServer().bridgeGhostProfile(c.Request.Context(), targetUserID); ok {
		c.JSON(http.StatusOK, gin.H{"avatar_url": profile["avatar_url"]})
		return
	}

	localpart, _, ok := ParseMatrixUserID(targetUserID)

	if !ok {
		localpart = targetUserID
	}

	var account models.Account
	err := db.GetDB().Where("username = ?", localpart).First(&account).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "User not found"})

		return
	}

	hs := GetHomeServer()
	avatarURL := hs.bridgeCompatibleProfileAvatar(c.Request.Context(), targetUserID, account)

	c.JSON(http.StatusOK, gin.H{"avatar_url": avatarURL})
}

func handleSetAvatarURL(c *gin.Context) {
	userID := c.Param("userId")

	callerID := getUserID(c)

	if callerID == "" || userID != callerID {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": "Cannot set another user's avatar_url"})

		return
	}

	var req struct {
		AvatarURL string `json:"avatar_url"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Bad JSON"})

		return
	}

	localpart, _, ok := ParseMatrixUserID(userID)
	if !ok {
		localpart = userID
	}

	db.GetDB().Model(&models.Account{}).Where("username = ?", localpart).Update("avatar", req.AvatarURL)

	// Propagate avatar change to m.room.member events in all joined rooms
	hs := GetHomeServer()
	propagateProfileChange(hs, userID, "avatar_url", req.AvatarURL)

	c.JSON(http.StatusOK, gin.H{})
}

// propagateProfileChange updates m.room.member events in all joined rooms when a user's profile changes.
func propagateProfileChange(hs *HomeServer, userID, field, value string) {
	joinedRooms, err := hs.Room.GetJoinedRooms(userID)
	if err != nil {
		return
	}

	for _, roomID := range joinedRooms {
		// Get existing member state content
		var existing MatrixRoomState
		err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?",
			roomID, string(EventTypeRoomMember), userID).First(&existing).Error
		if err != nil {
			continue
		}
		// Build updated content from existing
		content := make(map[string]any)

		if existing.Content != nil {
			maps.Copy(content, existing.Content)
		}

		content[field] = value
		if field == "avatar_url" {
			content[bridgeAvatarManualKey] = true
		}
		// Ensure membership stays join
		if _, ok := content["membership"]; !ok {
			content["membership"] = "join" //nolint:goconst
		}
		// Create new member event
		event := &MatrixEvent{
			EventID:        "$" + uuid.New().String(),
			RoomID:         roomID,
			EventType:      EventTypeRoomMember,
			Sender:         userID,
			StateKey:       &userID,
			Content:        content,
			Depth:          hs.Room.getNextDepth(db.GetDB(), roomID),
			OriginServerTS: time.Now().UnixMilli(),
			SenderDomain:   hs.Config.ServerName,
		}
		err = db.GetDB().Create(event).Error
		if err != nil {
			continue
		}

		hs.Room.updateRoomState(db.GetDB(), roomID, event)
	}
}

// ===== 已加入房间 =====

func handleGetJoinedRooms(c *gin.Context) {
	userID := getUserID(c)
	hs := GetHomeServer()

	rooms, err := hs.Room.GetJoinedRooms(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",            //nolint:goconst
			"error":   "Internal server error", //nolint:goconst
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"joined_rooms": rooms,
	})
}

// ===== 账户数据 =====

// handleGetAccountData, handleSetAccountData, handleGetRoomAccountData, handleSetRoomAccountData
// 这些函数已在 account.go 中实现

// ===== 设备管理 =====

func handleGetDevices(c *gin.Context) {
	userID := getUserID(c)

	hs := GetHomeServer()

	// 获取用户的所有会话（设备）
	var sessions []MatrixUserSession

	db.GetDB().Where("user_id = ?", userID).Find(&sessions)

	devices := []gin.H{}

	for _, session := range sessions {
		displayName := ""
		if session.DisplayName != nil {
			displayName = *session.DisplayName
		}

		devices = append(devices, gin.H{
			"device_id":    session.DeviceID,
			"display_name": displayName,
			"last_seen_ip": nil,
			"last_seen_ts": session.LastActiveTS,
		})
	}

	// 如果没有设备，添加当前设备
	if len(devices) == 0 {
		session, _ := hs.Auth.GetDeviceByToken(c.GetString("access_token"))
		if session != nil {
			devices = append(devices, gin.H{
				"device_id":    session.DeviceID,
				"display_name": nil,
				"last_seen_ip": nil,
				"last_seen_ts": nil,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"devices": devices,
	})
}

func handleGetDevice(c *gin.Context) {
	userID := getUserID(c)
	deviceID := c.Param("deviceId")

	var session MatrixUserSession

	result := db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).First(&session)

	if result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Device not found", //nolint:goconst
		})

		return
	}

	displayName := ""
	if session.DisplayName != nil {
		displayName = *session.DisplayName
	}

	c.JSON(http.StatusOK, gin.H{
		"device_id":    session.DeviceID,
		"display_name": displayName,
		"last_seen_ip": nil,
		"last_seen_ts": session.LastActiveTS,
	})
}

func handleUpdateDevice(c *gin.Context) {
	userID := getUserID(c)
	deviceID := c.Param("deviceId")

	var req struct {
		DisplayName string `json:"display_name"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON", //nolint:goconst
		})

		return
	}

	// Check device exists first
	var existing MatrixUserSession
	if db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).First(&existing).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Device not found",
		})

		return
	}

	result := db.GetDB().Model(&MatrixUserSession{}).
		Where("user_id = ? AND device_id = ?", userID, deviceID).
		Update("display_name", req.DisplayName)

	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN",
			"error":   result.Error.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func handleDeleteDevice(c *gin.Context) {
	userID := getUserID(c)
	deviceID := c.Param("deviceId")

	var req struct {
		Auth *struct {
			Type       string `json:"type"`
			Password   string `json:"password"`
			Session    string `json:"session"`
			Identifier *struct {
				Type string `json:"type"`
				User string `json:"user"`
			} `json:"identifier,omitempty"`
		} `json:"auth,omitempty"`
	}
	// Body is optional (some clients may not send a body on first request)
	c.ShouldBindJSON(&req) //nolint:errcheck,gosec // G104: body may be empty

	// Check if device exists
	var existing MatrixUserSession
	if db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).First(&existing).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "Device not found"})

		return
	}

	// Require UIA
	if req.Auth == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"session": generateSessionID(),
			"flows": []gin.H{
				{"stages": []string{"m.login.password"}}, //nolint:goconst
			},
			"params": gin.H{},
		})

		return
	}

	// Verify password via UIA
	if req.Auth.Type == "m.login.password" {
		// If the auth identifier specifies a different user, reject outright
		if req.Auth.Identifier != nil && req.Auth.Identifier.User != "" {
			authUser := req.Auth.Identifier.User
			// Normalize: if it's a full @user:server ID, compare directly; if localpart, build full ID
			if !strings.HasPrefix(authUser, "@") {
				hs := GetHomeServer()
				authUser = fmt.Sprintf("@%s:%s", authUser, hs.Config.ServerName)
			}

			if authUser != userID {
				c.JSON(http.StatusForbidden, gin.H{
					"errcode": "M_FORBIDDEN",
					"error":   "Cannot delete another user's device",
				})

				return
			}
		}

		hs := GetHomeServer()
		if !hs.Auth.VerifyUserPassword(userID, req.Auth.Password) {
			// Matrix spec: UIA failure should return 401 with a new session, not 403
			c.JSON(http.StatusUnauthorized, gin.H{
				"session": generateSessionID(),
				"flows": []gin.H{
					{"stages": []string{"m.login.password"}},
				},
				"params":  gin.H{},
				"errcode": "M_FORBIDDEN",
				"error":   "Incorrect password",
			})

			return
		}
	}

	db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).Delete(&MatrixUserSession{})
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 过滤器 =====

func handleCreateFilter(c *gin.Context) {
	userID := c.Param("userId")

	callerID := getUserID(c)

	if userID != callerID {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": "Cannot create filter for another user"})

		return
	}

	var filterJSON json.RawMessage
	err := c.ShouldBindJSON(&filterJSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Bad JSON"})

		return
	}
	// Filter must be a JSON object
	if len(filterJSON) == 0 || filterJSON[0] != '{' {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Filter must be a JSON object"})

		return
	}

	// Validate filter schema: certain fields must be objects or arrays
	var filterMap map[string]any
	err = json.Unmarshal(filterJSON, &filterMap)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Invalid filter JSON"})

		return
	}

	err = validateFilterSchema(filterMap)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": err.Error()})

		return
	}

	filterID := fmt.Sprintf("f%d", time.Now().UnixNano()%1000000)
	filter := &MatrixFilter{
		UserID:   userID,
		FilterID: filterID,
		Filter:   string(filterJSON),
	}

	err = db.GetDB().Create(filter).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_INTERNAL", "error": "Failed to create filter"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"filter_id": filterID})
}

func handleGetFilter(c *gin.Context) {
	userID := c.Param("userId")
	filterID := c.Param("filterId")

	var filter MatrixFilter
	err := db.GetDB().Where("user_id = ? AND filter_id = ?", userID, filterID).First(&filter).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "Filter not found"})

		return
	}

	var filterContent any
	err = json.Unmarshal([]byte(filter.Filter), &filterContent)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_UNKNOWN", "error": "Invalid stored filter"})

		return
	}

	c.JSON(http.StatusOK, filterContent)
}

// validateFilterSchema validates that a filter map conforms to the Matrix filter schema.
// Fields that expect objects must be objects; fields that expect arrays must be arrays.
func validateFilterSchema(filter map[string]any) error {
	// Top-level object fields that must be objects if present
	objectFields := []string{"presence", "account_data", "room"}
	for _, field := range objectFields {
		if v, ok := filter[field]; ok {
			if _, isMap := v.(map[string]any); !isMap {
				return fmt.Errorf("'%s' must be an object", field)
			}
		}
	}
	// Top-level array fields
	arrayFields := []string{"event_fields"}
	for _, field := range arrayFields {
		if v, ok := filter[field]; ok {
			if _, isSlice := v.([]any); !isSlice {
				return fmt.Errorf("'%s' must be an array", field)
			}
		}
	}
	// Validate room filter sub-fields
	if roomVal, ok := filter["room"]; ok {
		roomFilter, _ := roomVal.(map[string]any)

		roomObjectFields := []string{"state", "timeline", "ephemeral", "account_data"}

		for _, field := range roomObjectFields {
			if v, ok := roomFilter[field]; ok {
				if _, isMap := v.(map[string]any); !isMap {
					return fmt.Errorf("'room.%s' must be an object", field)
				}
			}
		}
		// Validate room filter array fields (rooms, not_rooms)
		roomArrayFields := []string{"rooms", "not_rooms"}
		for _, field := range roomArrayFields {
			if v, ok := roomFilter[field]; ok {
				if _, isSlice := v.([]any); !isSlice {
					return fmt.Errorf("'room.%s' must be an array", field)
				}
			}
		}
		// Validate sub-filter array fields
		for _, subField := range roomObjectFields {
			if subVal, ok := roomFilter[subField]; ok {
				subFilter, _ := subVal.(map[string]any)
				for _, arrField := range []string{"rooms", "not_rooms", "types", "not_types", "senders", "not_senders"} {
					if v, ok := subFilter[arrField]; ok {
						arr, isSlice := v.([]any)
						if !isSlice {
							return fmt.Errorf("'room.%s.%s' must be an array", subField, arrField)
						}
						// Validate array contents: all elements must be strings
						for _, elem := range arr {
							s, ok := elem.(string)
							if !ok {
								return fmt.Errorf("'room.%s.%s' elements must be strings", subField, arrField)
							}
							// Validate room IDs start with !
							if (arrField == "rooms" || arrField == "not_rooms") && !strings.HasPrefix(s, "!") {
								return fmt.Errorf("'room.%s.%s' contains invalid room ID: %s", subField, arrField, s)
							}
							// Validate sender IDs start with @
							if (arrField == "senders" || arrField == "not_senders") && !strings.HasPrefix(s, "@") {
								return fmt.Errorf("'room.%s.%s' contains invalid user ID: %s", subField, arrField, s)
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// ===== To-Device =====

func handleSendToDevice(c *gin.Context) {
	var body struct {
		Messages map[string]map[string]map[string]any `json:"messages"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{ErrCode: "M_BAD_JSON", Error: "Invalid JSON"})
		return
	}
	for target := range body.Messages {
		if _, _, ok := ParseMatrixUserID(target); !ok {
			c.JSON(http.StatusBadRequest, MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Invalid target user ID"})
			return
		}
	}
	err := sendClientToDevice(GetHomeServer(), getUserID(c), c.GetString("device_id"), c.Param("eventType"), c.Param("txnId"), body.Messages)
	if err != nil {
		log.Error("To-device delivery failed: %v", err)
		c.JSON(http.StatusBadGateway, MatrixError{ErrCode: "M_UNKNOWN", Error: "To-device delivery failed; retry the transaction"})
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

func handleGetOneTimeKeysCount(c *gin.Context) {
	userID := getUserID(c)

	deviceID := c.GetString("device_id")
	if deviceID == "" {
		deviceID = "DEVICE"
	}

	hs := GetHomeServer()
	keyStore := NewKeyStore(hs)

	counts := keyStore.GetOneTimeKeysCount(userID, deviceID)

	c.JSON(http.StatusOK, counts)
}

func handleGetKeyChanges(c *gin.Context) {
	userID := getUserID(c)
	fromToken := c.Query("from")
	toToken := c.Query("to")

	hs := GetHomeServer()

	// Parse sync tokens to timestamps
	var fromTS, toTS int64

	_, _ = fmt.Sscanf(fromToken, "s%d", &fromTS)
	_, _ = fmt.Sscanf(toToken, "s%d", &toTS)

	if fromTS == 0 || toTS == 0 {
		c.JSON(http.StatusOK, gin.H{"changed": []string{}, "left": []string{}})

		return
	}

	// Find users who uploaded new device keys between from and to
	joinedRooms, _ := hs.Room.GetJoinedRooms(userID)
	changed, left := hs.Sync.getDeviceListUpdates(userID, joinedRooms, fromTS)

	// Also check for new device key uploads in the time range
	if len(joinedRooms) > 0 {
		var members []MatrixRoomMember

		db.GetDB().Where("room_id IN ? AND membership = ? AND user_id != ?",
			joinedRooms, MembershipJoin, userID).Find(&members)

		changedSet := make(map[string]bool)
		for _, u := range changed {
			changedSet[u] = true
		}

		for _, m := range members {
			if changedSet[m.UserID] {
				continue
			}

			fromTime := time.Unix(0, fromTS*int64(time.Millisecond))
			toTime := time.Unix(0, toTS*int64(time.Millisecond))

			// Check if user uploaded keys or created new sessions in the time range
			var keyCount int64

			db.GetDB().Model(&MatrixDeviceKey{}).
				Where("user_id = ? AND updated_at > ? AND updated_at <= ?",
					m.UserID, fromTime, toTime).
				Count(&keyCount)

			var sessionCount int64

			db.GetDB().Model(&MatrixUserSession{}).
				Where("user_id = ? AND created_at > ? AND created_at <= ?",
					m.UserID, fromTime, toTime).
				Count(&sessionCount)

			if keyCount > 0 || sessionCount > 0 {
				changed = append(changed, m.UserID)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"changed": changed, "left": left})
}

func handleKeysUpload(c *gin.Context) {
	userID := getUserID(c)

	deviceID := c.GetString("device_id")
	if deviceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_MISSING_PARAM", "error": "Missing device_id"})

		return
	}

	var req UploadKeysRequest
	// Read body manually — handles empty body gracefully (body may be absent or {})
	bodyBytes, _ := io.ReadAll(c.Request.Body)
	if len(bodyBytes) > 0 {
		err := json.Unmarshal(bodyBytes, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"errcode": "M_BAD_JSON",
				"error":   "Invalid JSON: " + err.Error(),
			})

			return
		}
	}

	// Validate user_id / device_id match (must return 400, not 500)
	if req.DeviceKeys != nil {
		if req.DeviceKeys.UserID != "" && req.DeviceKeys.UserID != userID {
			c.JSON(http.StatusBadRequest, gin.H{
				"errcode": "M_INVALID_PARAM",
				"error":   "user_id in device_keys does not match authenticated user",
			})

			return
		}

		if req.DeviceKeys.DeviceID != "" && req.DeviceKeys.DeviceID != deviceID {
			c.JSON(http.StatusBadRequest, gin.H{
				"errcode": "M_INVALID_PARAM",
				"error":   "device_id in device_keys does not match authenticated device",
			})

			return
		}
		// Validate required fields per Matrix spec — algorithms and keys are required,
		// signatures may be empty (e.g. test clients uploading unsigned keys for device tracking)
		if len(req.DeviceKeys.Algorithms) == 0 || len(req.DeviceKeys.Keys) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"errcode": "M_BAD_JSON",
				"error":   "device_keys must contain algorithms and keys",
			})

			return
		}
		// Ensure user_id and device_id are set correctly before storing
		req.DeviceKeys.UserID = userID
		req.DeviceKeys.DeviceID = deviceID
	}

	hs := GetHomeServer()
	keyStore := NewKeyStore(hs)

	resp, err := keyStore.UploadDeviceKeys(userID, deviceID, &req)
	if err != nil {
		log.Error("Failed to upload keys: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN",
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleKeysQuery(c *gin.Context) {
	var req QueryKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})

		return
	}

	// 如果没有指定查询的用户，返回空结果
	if len(req.DeviceKeys) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"device_keys":       gin.H{},
			"master_keys":       gin.H{},
			"self_signing_keys": gin.H{},
			"user_signing_keys": gin.H{},
		})

		return
	}

	hs := GetHomeServer()
	keyStore := NewKeyStore(hs)

	// Split request into local and remote users
	localReq := &QueryKeysRequest{DeviceKeys: make(map[string][]string)}
	remoteByServer := make(map[string]map[string][]string) // server → userID → deviceIDs

	for userID, deviceIDs := range req.DeviceKeys {
		_, domain, ok := ParseMatrixUserID(userID)
		if !ok || domain == hs.Config.ServerName {
			localReq.DeviceKeys[userID] = deviceIDs
		} else {
			if remoteByServer[domain] == nil {
				remoteByServer[domain] = make(map[string][]string)
			}

			remoteByServer[domain][userID] = deviceIDs
		}
	}

	// Query local keys
	resp, err := keyStore.QueryDeviceKeys(localReq)
	if err != nil {
		log.Error("Failed to query keys: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN",
			"error":   err.Error(),
		})

		return
	}

	// 确保返回所有字段
	if resp.DeviceKeys == nil {
		resp.DeviceKeys = make(map[string]map[string]*DeviceKeys)
	}

	if resp.Failures == nil {
		resp.Failures = make(map[string]any)
	}

	if err := appendClientSigningKeys(resp, localReq.DeviceKeys, getUserID(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_UNKNOWN", "error": "Cannot load cross-signing keys"})
		return
	}

	// Forward remote queries via federation
	if len(remoteByServer) > 0 {
		fedClient := NewFederationClient(hs)
		for server, userDevices := range remoteByServer {
			remoteKeys, err := fedClient.QueryRemoteKeyBundle(server, userDevices)
			if err != nil {
				resp.Failures[server] = map[string]any{"error": err.Error()}

				continue
			}

			maps.Copy(resp.DeviceKeys, remoteKeys.DeviceKeys)
			maps.Copy(resp.MasterKeys, remoteKeys.MasterKeys)
			maps.Copy(resp.SelfSigningKeys, remoteKeys.SelfSigningKeys)
		}
	}

	c.JSON(http.StatusOK, resp)
}

func handleKeysClaim(c *gin.Context) {
	var req ClaimKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})

		return
	}

	hs := GetHomeServer()
	keyStore := NewKeyStore(hs)

	local := &ClaimKeysRequest{OneTimeKeys: map[string]map[string]string{}}
	remote := map[string]map[string]map[string]string{}
	for user, devices := range req.OneTimeKeys {
		_, server, ok := ParseMatrixUserID(user)
		if !ok {
			c.JSON(400, MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Invalid user ID"})
			return
		}
		if server == hs.Config.ServerName {
			local.OneTimeKeys[user] = devices
		} else {
			if remote[server] == nil {
				remote[server] = map[string]map[string]string{}
			}
			remote[server][user] = devices
		}
	}
	resp, err := keyStore.ClaimOneTimeKeys(local)
	if err != nil {
		log.Error("Failed to claim keys: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN",
			"error":   err.Error(),
		})

		return
	}

	if resp.OneTimeKeys == nil {
		resp.OneTimeKeys = make(map[string]map[string]map[string]json.RawMessage)
	}

	if resp.Failures == nil {
		resp.Failures = make(map[string]any)
	}

	client := NewFederationClient(hs)
	for server, users := range remote {
		claimed, err := client.ClaimRemoteKeys(server, users)
		if err != nil {
			resp.Failures[server] = map[string]any{"error": err.Error()}
			continue
		}
		maps.Copy(resp.OneTimeKeys, claimed.OneTimeKeys)
	}
	c.JSON(http.StatusOK, resp)
}

func handleKeysSignaturesUpload(c *gin.Context) { handleClientSignaturesUpload(c) }

// ===== Presence =====

func handleSetPresence(c *gin.Context) {
	userID := c.Param("userId")

	callerID := getUserID(c)

	if userID != callerID {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": "Cannot set another user's presence"})

		return
	}

	var req struct {
		Presence  string `json:"presence"`
		StatusMsg string `json:"status_msg"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Bad JSON"})

		return
	}

	hs := GetHomeServer()
	hs.Sync.setPresence(userID, req.Presence)

	if req.StatusMsg != "" {
		db.GetDB().Model(&MatrixPresence{}).Where("user_id = ?", userID).Update("status_msg", req.StatusMsg)
	}

	c.JSON(http.StatusOK, gin.H{})
}

func handleGetPresence(c *gin.Context) {
	userID := c.Param("userId")

	var presence MatrixPresence
	err := db.GetDB().Where("user_id = ?", userID).First(&presence).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Error("get presence %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_UNKNOWN", "error": "Internal server error"})

		return
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"presence":        "offline",
			"last_active_ago": 0,
		})

		return
	}

	lastActiveAgo := time.Now().UnixMilli() - presence.LastActiveTS

	resp := gin.H{
		"presence":        presence.Presence,
		"last_active_ago": lastActiveAgo,
	}
	if presence.StatusMsg != nil {
		resp["status_msg"] = *presence.StatusMsg
	}

	c.JSON(http.StatusOK, resp)
}

// ===== 公共房间 =====

func handleGetPublicRoomsClient(c *gin.Context) {
	// 解析分页参数
	limit := 100
	if l := c.Query("limit"); l != "" {
		if _, err := fmt.Sscanf(l, "%d", &limit); err != nil {
			limit = 100
		}

		if limit < 0 {
			limit = 0
		}
	}

	if limit > 250 {
		limit = 250
	}

	since := c.Query("since")

	// POST 请求可能带 filter
	var filter struct {
		GenericSearchTerm string `json:"generic_search_term"`
	}

	if c.Request.Method == http.MethodPost {
		var body struct {
			Limit  int    `json:"limit"`
			Since  string `json:"since"`
			Filter struct {
				GenericSearchTerm string `json:"generic_search_term"`
			} `json:"filter"`
		}

		if c.ShouldBindJSON(&body) == nil {
			if body.Limit > 0 {
				limit = body.Limit
			}

			if body.Since != "" {
				since = body.Since
			}

			filter = body.Filter
		}
	}

	// 查询公共房间
	query := db.GetDB().Where("visibility = ?", "public")

	if filter.GenericSearchTerm != "" {
		term := "%" + filter.GenericSearchTerm + "%"
		like := dialect.ILike(query)
		query = query.Where("name "+like+" ? OR topic "+like+" ?", term, term)
	}

	var rooms []MatrixRoom

	query.Order("created_at DESC").Limit(limit).Find(&rooms)

	// 构建响应
	chunk := make([]gin.H, len(rooms))

	for i, room := range rooms {
		var joinedCount int64

		db.GetDB().Model(&MatrixRoomMember{}).
			Where("room_id = ? AND membership = ?", room.RoomID, MembershipJoin).
			Count(&joinedCount)

		entry := gin.H{
			"room_id":            room.RoomID,
			"num_joined_members": joinedCount,
			"world_readable":     room.HistoryVisibility == "world_readable",
			"guest_can_join":     room.GuestAccess == "can_join",
		}
		// Only include optional fields when non-empty (per Matrix spec)
		if room.Name != nil && *room.Name != "" {
			entry["name"] = *room.Name
		}

		if room.Topic != nil && *room.Topic != "" {
			entry["topic"] = *room.Topic
		}

		if room.AvatarURL != nil && *room.AvatarURL != "" {
			entry["avatar_url"] = *room.AvatarURL
		}

		if room.JoinRule != "" {
			entry["join_rule"] = room.JoinRule
		}

		if room.RoomType != nil && *room.RoomType != "" {
			entry["room_type"] = *room.RoomType
		}
		// Fetch canonical alias from room state
		var canonicalAliasState MatrixRoomState
		err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?",
			room.RoomID, EventTypeRoomCanonicalAlias, "").First(&canonicalAliasState).Error
		if err == nil {
			var aliasEvent MatrixEvent
			err := db.GetDB().Where("event_id = ?", canonicalAliasState.EventID).First(&aliasEvent).Error
			if err == nil {
				if alias, ok := aliasEvent.Content["alias"].(string); ok && alias != "" {
					entry["canonical_alias"] = alias
				}
			}
		}

		chunk[i] = entry
	}

	resp := gin.H{
		"chunk":                     chunk,
		"total_room_count_estimate": len(rooms),
	}
	if len(rooms) == limit {
		resp["next_batch"] = fmt.Sprintf("s%d", time.Now().UnixMilli())
	}

	if since != "" {
		resp["prev_batch"] = since
	}

	c.JSON(http.StatusOK, resp)
}

// handleGetRoomVisibility returns the visibility of a room in the public room directory.
func handleGetRoomVisibility(c *gin.Context) {
	roomID := c.Param("roomId")

	var room MatrixRoom
	err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error

	if err != nil {
		c.JSON(http.StatusNotFound, MatrixError{ErrCode: "M_NOT_FOUND", Error: "Room not found"}) //nolint:goconst

		return
	}

	visibility := room.Visibility
	if visibility == "" {
		visibility = "private"
	}

	c.JSON(http.StatusOK, gin.H{"visibility": visibility})
}

// handleSetRoomVisibility sets the visibility of a room in the public room directory.
func handleSetRoomVisibility(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")
	userID := c.GetString("user_id")

	var req struct {
		Visibility string `json:"visibility"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil || req.Visibility == "" {
		c.JSON(http.StatusBadRequest, MatrixError{ErrCode: "M_BAD_JSON", Error: "Missing visibility"})

		return
	}

	if req.Visibility != "public" && req.Visibility != "private" {
		c.JSON(http.StatusBadRequest, MatrixError{ErrCode: "M_BAD_JSON", Error: "visibility must be 'public' or 'private'"})

		return
	}

	// Check room exists
	var room MatrixRoom
	err = db.GetDB().Where("room_id = ?", roomID).First(&room).Error
	if err != nil {
		c.JSON(http.StatusNotFound, MatrixError{ErrCode: "M_NOT_FOUND", Error: "Room not found"})

		return
	}

	// Only room members with sufficient power can change visibility
	membership, _ := hs.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		c.JSON(http.StatusForbidden, MatrixError{ErrCode: "M_FORBIDDEN", Error: "You are not joined to this room"})

		return
	}

	db.GetDB().Model(&MatrixRoom{}).Where("room_id = ?", roomID).Update("visibility", req.Visibility)
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 能力 =====

func handleGetCapabilities(c *gin.Context) {
	hs := GetHomeServer()
	c.JSON(http.StatusOK, gin.H{
		"capabilities": gin.H{
			"m.change_password": gin.H{
				"enabled": true,
			},
			"m.room_versions": gin.H{
				"default": hs.Config.RoomVersion,
				"available": gin.H{
					"1":  "stable", //nolint:goconst
					"2":  "stable",
					"3":  "stable",
					"4":  "stable",
					"5":  "stable",
					"6":  "stable",
					"7":  "stable",
					"8":  "stable",
					"9":  "stable",
					"10": "stable",
				},
			},
		},
	})
}

// ===== 媒体仓库 API =====
// 媒体上传、下载、缩略图、URL预览等函数已移至 media.go

// ===== 推送通知 API =====

// GetPushers 获取推送器列表.
func handleGetPushers(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	pushers, err := hs.Push.GetPushers(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR", //nolint:goconst
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{"pushers": pushers})
}

// SetPusher 设置推送器.
func handleSetPusher(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var pusher Pusher
	err := c.ShouldBindJSON(&pusher)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})

		return
	}

	// Associate the pusher with the current device
	pusher.DeviceID = c.GetString("device_id")

	err = hs.Push.SetPusher(userID, &pusher)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// GetPushRules 获取推送规则.
func handleGetPushRules(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	rules, err := hs.Push.GetPushRules(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, rules)
}

// GetPushRulesGlobal 获取全局推送规则（按 scope）.
func handleGetPushRulesGlobal(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	rules, err := hs.Push.GetPushRules(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, MatrixError{
			ErrCode: "M_INTERNAL_ERROR",
			Error:   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, rules.Global)
}

// GetPushRulesScope 获取特定范围推送规则.
func handleGetPushRulesScope(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	kind := c.Param("kind")

	rules, err := hs.Push.GetPushRules(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, MatrixError{
			ErrCode: "M_INTERNAL_ERROR",
			Error:   err.Error(),
		})

		return
	}

	switch kind {
	case "override":
		c.JSON(http.StatusOK, rules.Global.Override)
	case "content":
		c.JSON(http.StatusOK, rules.Global.Content)
	case "room":
		c.JSON(http.StatusOK, rules.Global.Room)
	case "sender":
		c.JSON(http.StatusOK, rules.Global.Sender)
	case "underride":
		c.JSON(http.StatusOK, rules.Global.Underride)
	default:
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_INVALID_PARAM",
			Error:   "Unknown rule kind: " + kind,
		})
	}
}

// GetPushRule 获取单个推送规则.
func handleGetPushRule(c *gin.Context) {
	userID := c.GetString("user_id")
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")

	var rule PushRule
	err := db.GetDB().Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?",
		userID, scope, kind, ruleID).First(&rule).Error
	if err != nil {
		c.JSON(http.StatusNotFound, MatrixError{
			ErrCode: "M_NOT_FOUND",
			Error:   "Push rule not found",
		})

		return
	}

	hs := GetHomeServer()
	resp := hs.Push.ruleToResponse(&rule)
	c.JSON(http.StatusOK, resp)
}

// SetPushRule 设置推送规则.
func handleSetPushRule(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")

	var body struct {
		Actions    []any           `json:"actions"`
		Conditions []PushCondition `json:"conditions"`
		Pattern    string          `json:"pattern"`
	}
	err := c.ShouldBindJSON(&body)

	if err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_BAD_JSON",
			Error:   err.Error(),
		})

		return
	}

	actionsJSON, _ := json.Marshal(body.Actions)       //nolint:errchkjson // body.Actions is []string, always safe
	conditionsJSON, _ := json.Marshal(body.Conditions) //nolint:errchkjson // body.Conditions is []string, always safe

	rule := &PushRule{
		Enabled:    true,
		Actions:    string(actionsJSON),
		Conditions: string(conditionsJSON),
		Pattern:    body.Pattern,
	}

	err = hs.Push.SetPushRule(userID, scope, kind, ruleID, rule)
	if err != nil {
		c.JSON(http.StatusInternalServerError, MatrixError{
			ErrCode: "M_INTERNAL_ERROR",
			Error:   err.Error(),
		})

		return
	}

	go hs.NotifyUser(userID)

	c.JSON(http.StatusOK, gin.H{})
}

// DeletePushRule 删除推送规则.
func handleDeletePushRule(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")

	err := hs.Push.DeletePushRule(userID, scope, kind, ruleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, MatrixError{
			ErrCode: "M_INTERNAL_ERROR",
			Error:   err.Error(),
		})

		return
	}

	go hs.NotifyUser(userID)

	c.JSON(http.StatusOK, gin.H{})
}

// GetPushRuleActions 获取推送规则动作.
func handleGetPushRuleActions(c *gin.Context) {
	userID := c.GetString("user_id")
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")

	var rule PushRule
	err := db.GetDB().Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?",
		userID, scope, kind, ruleID).First(&rule).Error
	if err != nil {
		c.JSON(http.StatusNotFound, MatrixError{
			ErrCode: "M_NOT_FOUND",
			Error:   "Push rule not found",
		})

		return
	}

	var actions []any
	if rule.Actions != "" {
		json.Unmarshal([]byte(rule.Actions), &actions) //nolint:errcheck,gosec // G104: best effort parsing
	}

	c.JSON(http.StatusOK, gin.H{"actions": actions})
}

// SetPushRuleActions 设置推送规则动作.
func handleSetPushRuleActions(c *gin.Context) {
	userID := c.GetString("user_id")
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")

	var body struct {
		Actions []any `json:"actions"`
	}
	err := c.ShouldBindJSON(&body)

	if err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_BAD_JSON",
			Error:   err.Error(),
		})

		return
	}

	actionsJSON, _ := json.Marshal(body.Actions) //nolint:errchkjson // []string, always safe
	err = db.GetDB().Model(&PushRule{}).
		Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?", userID, scope, kind, ruleID).
		Update("actions", string(actionsJSON)).Error
	if err != nil {
		c.JSON(http.StatusNotFound, MatrixError{
			ErrCode: "M_NOT_FOUND",
			Error:   "Push rule not found",
		})

		return
	}

	hs := GetHomeServer()
	go hs.NotifyUser(userID)

	c.JSON(http.StatusOK, gin.H{})
}

// GetPushRuleEnabled 获取推送规则启用状态.
func handleGetPushRuleEnabled(c *gin.Context) {
	userID := c.GetString("user_id")
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")

	var rule PushRule
	err := db.GetDB().Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?",
		userID, scope, kind, ruleID).First(&rule).Error
	if err != nil {
		c.JSON(http.StatusNotFound, MatrixError{
			ErrCode: "M_NOT_FOUND",
			Error:   "Push rule not found",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{"enabled": rule.Enabled})
}

// SetPushRuleEnabled 设置推送规则启用状态.
func handleSetPushRuleEnabled(c *gin.Context) {
	userID := c.GetString("user_id")
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")

	var body struct {
		Enabled bool `json:"enabled"`
	}
	err := c.ShouldBindJSON(&body)

	if err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_BAD_JSON",
			Error:   err.Error(),
		})

		return
	}

	err = db.GetDB().Model(&PushRule{}).
		Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?", userID, scope, kind, ruleID).
		Update("enabled", body.Enabled).Error
	if err != nil {
		c.JSON(http.StatusNotFound, MatrixError{
			ErrCode: "M_NOT_FOUND",
			Error:   "Push rule not found",
		})

		return
	}

	hs := GetHomeServer()
	go hs.NotifyUser(userID)

	c.JSON(http.StatusOK, gin.H{})
}

// ===== VoIP / TURN =====

// GetTurnServer 获取 TURN 服务器配置.
func handleGetTurnServer(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"username": "",
		"password": "",
		"uris":     []string{},
		"ttl":      86400,
	})
}

// ===== 搜索 API =====
// 搜索相关函数已移至 search.go

// SearchRequest 搜索请求.
type SearchRequest struct {
	SearchCategories struct {
		RoomEvents    *RoomEventsSearch    `json:"room_events,omitempty"`
		UserDirectory *UserDirectorySearch `json:"user_directory,omitempty"`
	} `json:"search_categories"`
	NextBatch string `json:"next_batch,omitempty"`
}

// RoomEventsSearch 房间事件搜索.
type RoomEventsSearch struct {
	SearchTerm   string              `json:"search_term"`
	Filter       string              `json:"filter,omitempty"`
	Rooms        []string            `json:"rooms,omitempty"`
	OrderBy      string              `json:"order_by,omitempty"`
	EventContext *EventContextSearch `json:"event_context,omitempty"`
	IncludeState bool                `json:"include_state,omitempty"`
	GroupBy      []string            `json:"group_by,omitempty"`
}

// UserDirectorySearch 用户目录搜索.
type UserDirectorySearch struct {
	SearchTerm string `json:"search_term"`
	Limit      int    `json:"limit,omitempty"`
}

// EventContextSearch 事件上下文搜索.
type EventContextSearch struct {
	BeforeLimit    int  `json:"before_limit,omitempty"`
	AfterLimit     int  `json:"after_limit,omitempty"`
	IncludeProfile bool `json:"include_profile,omitempty"`
}

// ===== 用户目录 API =====
// 用户目录搜索函数已移至 search.go

// ===== 房间别名 API =====
// 别名相关函数已移至 services.go

// ===== 第三方网络 API =====

// GetThirdPartyProtocol 获取第三方协议.
func handleGetThirdPartyProtocol(c *gin.Context) {
	protocol := c.Param("protocol")
	_ = protocol

	c.JSON(http.StatusNotFound, gin.H{
		"errcode": "M_NOT_FOUND",
		"error":   "Protocol not found",
	})
}

// GetThirdPartyUser 获取第三方用户.
func handleGetThirdPartyUser(c *gin.Context) {
	protocol := c.Param("protocol")
	_ = protocol

	c.JSON(http.StatusOK, []any{})
}

// GetThirdPartyLocation 获取第三方位置.
func handleGetThirdPartyLocation(c *gin.Context) {
	protocol := c.Param("protocol")
	_ = protocol

	c.JSON(http.StatusOK, []any{})
}

// ===== OpenID API =====
// 在 openid.go 中定义

// ===== 房间标签 API =====

// GetRoomTags 获取房间标签.
func handleGetRoomTags(c *gin.Context) {
	userID := c.Param("userId")
	roomID := c.Param("roomId")

	var tags []MatrixRoomTag

	db.GetDB().Where("user_id = ? AND room_id = ?", userID, roomID).Find(&tags)

	tagsMap := make(map[string]any)

	for _, tag := range tags {
		var content map[string]any
		if tag.Content != "" {
			json.Unmarshal([]byte(tag.Content), &content) //nolint:errcheck,gosec // G104: best effort parsing
		}

		if content == nil {
			content = map[string]any{}
		}

		tagsMap[tag.Tag] = content
	}

	c.JSON(http.StatusOK, gin.H{
		"tags": tagsMap,
	})
}

// SetRoomTag 设置房间标签.
func handleSetRoomTag(c *gin.Context) {
	userID := c.Param("userId")
	roomID := c.Param("roomId")
	tag := c.Param("tag")

	var body map[string]any
	err := c.ShouldBindJSON(&body)
	if err != nil {
		body = map[string]any{}
	}

	contentBytes, _ := json.Marshal(body) //nolint:errchkjson // body is map[string]any

	// Upsert
	var existing MatrixRoomTag

	result := db.GetDB().Where("user_id = ? AND room_id = ? AND tag = ?", userID, roomID, tag).First(&existing)
	if result.Error == nil {
		existing.Content = string(contentBytes)
		db.GetDB().Save(&existing)
	} else {
		db.GetDB().Create(&MatrixRoomTag{
			UserID:  userID,
			RoomID:  roomID,
			Tag:     tag,
			Content: string(contentBytes),
		})
	}

	c.JSON(http.StatusOK, gin.H{})
}

// DeleteRoomTag 删除房间标签.
func handleDeleteRoomTag(c *gin.Context) {
	userID := c.Param("userId")
	roomID := c.Param("roomId")
	tag := c.Param("tag")

	db.GetDB().Where("user_id = ? AND room_id = ? AND tag = ?", userID, roomID, tag).Delete(&MatrixRoomTag{})

	c.JSON(http.StatusOK, gin.H{})
}

// ===== 举报 API =====
// 举报相关函数已移至 services.go

// ===== 忽略用户 API =====

// ===== 房间摘要 API =====

// GetRoomSummary 获取房间摘要.
func handleGetRoomSummary(c *gin.Context) {
	roomIDOrAlias := c.Param("roomIdOrAlias")

	// 通过别名或ID获取房间摘要
	hs := GetHomeServer()

	// 转换为房间ID
	roomID := roomIDOrAlias

	if strings.HasPrefix(roomIDOrAlias, "#") {
		// 解析别名
		aliasService := NewAliasService(hs)

		resolvedRoomID, err := aliasService.GetRoomIDByAlias(roomIDOrAlias)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"errcode": "M_NOT_FOUND",
				"error":   "Room alias not found",
			})

			return
		}

		roomID = resolvedRoomID
	}

	// 获取房间信息
	room, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})

		return
	}

	// 获取成员
	members, _ := hs.Room.GetRoomMembers(roomID, []MembershipState{MembershipJoin})

	c.JSON(http.StatusOK, gin.H{
		"room_id":            roomID,
		"room_type":          room.RoomType,
		"name":               room.Name, //nolint:goconst
		"topic":              room.Topic,
		"avatar_url":         room.AvatarURL,
		"num_joined_members": len(members),
		"join_rule":          room.JoinRule, //nolint:goconst
		"world_readable":     room.HistoryVisibility == "world_readable",
		"guest_can_join":     room.GuestAccess == "can_join",
	})
}

// ===== Knocks API =====

// KnockRoom 敲门.
func handleKnockRoom(c *gin.Context) {
	roomIDOrAlias := c.Param("roomIdOrAlias")
	userID := getUserID(c)

	var req struct {
		Reason string `json:"reason"`
	}

	c.ShouldBindJSON(&req) //nolint:errcheck,gosec // G104: body is optional

	roomID := roomIDOrAlias

	// 检查房间是否存在
	hs := GetHomeServer()

	room, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})

		return
	}

	// 检查是否允许 knock
	if room.JoinRule != "knock" {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Room does not allow knocking",
		})

		return
	}

	// 创建 knock 成员事件
	event := &MatrixEvent{
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    userID,
		StateKey:  &userID,
		Content: map[string]any{
			"membership": "knock",
			"reason":     req.Reason,
		},
		Depth: hs.Room.getNextDepth(db.GetDB(), roomID),
	}
	event.EventID = "$" + uuid.New().String()
	event.OriginServerTS = time.Now().UnixMilli()

	if err := db.GetDB().Create(event).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Internal server error",
		})

		return
	}

	hs.Room.updateRoomState(db.GetDB(), roomID, event)
	hs.Room.updateRoomMember(db.GetDB(), roomID, userID, MembershipKnock, event.EventID)

	c.JSON(http.StatusOK, gin.H{
		"room_id": roomID,
	})
}

// ===== 空间 API =====

// GetSpaceHierarchy 获取空间层级.
func handleGetSpaceHierarchy(c *gin.Context) {
	hs := GetHomeServer()
	userID := getUserID(c)
	roomID := c.Param("roomId")

	limit := 20

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	maxDepth := 3

	if d := c.Query("max_depth"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil {
			maxDepth = parsed
		}
	}

	from := c.Query("from")

	result, err := hs.Spaces.GetSpaceHierarchy(userID, roomID, limit, maxDepth, from)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, result)
}

// GetSpaceSummary 获取空间摘要.
func handleGetSpaceSummary(c *gin.Context) {
	hs := GetHomeServer()
	userID := getUserID(c)
	roomID := c.Param("roomId")

	result, err := hs.Spaces.GetSpaceSummary(userID, roomID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, result)
}

// GetSpaceSummaryClient 客户端空间摘要.
func handleGetSpaceSummaryClient(c *gin.Context) {
	handleGetSpaceHierarchy(c)
}
