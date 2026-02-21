package homeserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

// RegisterRoutes 注册 Matrix Client-Server API 路由
func RegisterRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}
	
	// 服务器发现
	r.GET("/.well-known/matrix/server", handleWellKnownServer)
	r.GET("/.well-known/matrix/client", handleWellKnownClient)
	
	// 版本信息
	r.GET("/_matrix/client/versions", handleVersions)
	r.GET("/_matrix/client/r0/register/email/requestToken", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })
	r.GET("/_matrix/federation/v1/version", handleFederationVersion)
	
	// 认证相关
	auth := r.Group("/_matrix/client/v3")
	auth.GET("/login", handleGetLoginFlows)
	auth.POST("/login", handleLogin)
	auth.POST("/logout", handleLogout)
	auth.POST("/logout/all", handleLogoutAll)
	auth.POST("/refresh", handleRefresh)
	auth.POST("/register", handleRegister)
	auth.GET("/register", handleRegisterAvailability)
	auth.GET("/register/available", handleUsernameAvailable)
	
	// 需要 token 的 API
	api := r.Group("/_matrix/client/v3")
	api.Use(matrixAuthMiddleware())
	{
		// 用户信息
		api.GET("/account/whoami", handleWhoami)
		api.GET("/account", handleAccount)
		api.GET("/profile/:userId", handleGetProfile)
		api.GET("/profile/:userId/displayname", handleGetDisplayName)
		api.PUT("/profile/:userId/displayname", handleSetDisplayName)
		api.GET("/profile/:userId/avatar_url", handleGetAvatarURL)
		api.PUT("/profile/:userId/avatar_url", handleSetAvatarURL)
		
		// 房间管理
		api.POST("/createRoom", handleCreateRoom)
		api.GET("/rooms/:roomId", handleGetRoom)
		api.PUT("/rooms/:roomId/join", handleJoinRoom)
		api.POST("/rooms/:roomId/join", handleJoinRoom)
		api.POST("/rooms/:roomId/leave", handleLeaveRoom)
		api.POST("/rooms/:roomId/forget", handleForgetRoom)
		api.POST("/rooms/:roomId/invite", handleInviteUser)
		api.POST("/rooms/:roomId/kick", handleKickUser)
		api.POST("/rooms/:roomId/ban", handleBanUser)
		api.POST("/rooms/:roomId/unban", handleUnbanUser)
		api.GET("/rooms/:roomId/members", handleGetRoomMembers)
		api.GET("/rooms/:roomId/state", handleGetRoomState)
		api.GET("/rooms/:roomId/state/:eventType", handleGetRoomStateEvent)
		api.GET("/rooms/:roomId/state/:eventType/:stateKey", handleGetRoomStateEvent)
		api.PUT("/rooms/:roomId/state/:eventType", handleSendStateEvent)
		api.PUT("/rooms/:roomId/state/:eventType/:stateKey", handleSendStateEvent)
		
		// 事件发送
		api.PUT("/rooms/:roomId/send/:eventType/:txnId", handleSendEvent)
		api.PUT("/rooms/:roomId/send/:eventType", handleSendEvent)
		
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
		api.GET("/thirdparty/user/:protocol", func(c *gin.Context) { c.JSON(http.StatusOK, []interface{}{}) })
		api.GET("/thirdparty/location/:protocol", func(c *gin.Context) { c.JSON(http.StatusOK, []interface{}{}) })
		api.GET("/thirdparty/protocol/:protocol", handleGetThirdPartyProtocol)
		api.GET("/thirdparty/user/:protocol", handleGetThirdPartyUser)
		api.GET("/thirdparty/location/:protocol", handleGetThirdPartyLocation)
		
		// 搜索
		api.POST("/search", handleSearch)
		
		// 用户目录
		api.POST("/user_directory/search", handleUserDirectory)
		
		// 推送通知
		api.GET("/pushers", handleGetPushers)
		api.POST("/pushers/set", handleSetPusher)
		api.GET("/pushrules", handleGetPushRules)
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
		
		// 房间标签
		api.GET("/user/:userId/rooms/:roomId/tags", handleGetRoomTags)
		api.PUT("/user/:userId/rooms/:roomId/tags/:tag", handleSetRoomTag)
		api.DELETE("/user/:userId/rooms/:roomId/tags/:tag", handleDeleteRoomTag)
		
		// OpenID
		api.POST("/user/:userId/openid/request_token", handleRequestOpenIDToken)
		api.GET("/openid/userinfo", handleGetOpenIDUserInfo)
		
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
	r.GET("/_matrix/media/v1/download/:serverName/:mediaId", handleDownloadMedia)
	r.GET("/_matrix/media/v1/thumbnail/:serverName/:mediaId", handleThumbnailMedia)
	r.GET("/_matrix/media/v1/preview_url", handlePreviewURL)
	
	// 媒体 (v3 endpoint)
	r.POST("/_matrix/media/v3/upload", matrixAuthMiddleware(), handleUploadMedia)
	r.GET("/_matrix/media/v3/download/:serverName/:mediaId", handleDownloadMedia)
	r.GET("/_matrix/media/v3/thumbnail/:serverName/:mediaId", handleThumbnailMedia)
	r.GET("/_matrix/media/v3/preview_url", handlePreviewURL)
	
	// r0 兼容
	r0 := r.Group("/_matrix/client/r0")
	r0.Use(matrixAuthMiddleware())
	{
		r0.GET("/sync", handleSync)
		r0.POST("/createRoom", handleCreateRoom)
		r0.PUT("/rooms/:roomId/join", handleJoinRoom)
		r0.POST("/rooms/:roomId/join", handleJoinRoom)
		r0.POST("/rooms/:roomId/leave", handleLeaveRoom)
		r0.PUT("/rooms/:roomId/send/:eventType/:txnId", handleSendEvent)
		r0.GET("/rooms/:roomId/messages", handleGetMessages)
		r0.GET("/rooms/:roomId/state", handleGetRoomState)
		r0.POST("/rooms/:roomId/invite", handleInviteUser)
	}
}

// ===== 中间件 =====

func matrixAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			auth = c.Query("access_token")
		} else {
			// Bearer token
			if strings.HasPrefix(auth, "Bearer ") {
				auth = auth[7:]
			}
		}
		
		if auth == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"errcode": "M_MISSING_TOKEN",
				"error":   "Missing access token",
			})
			c.Abort()
			return
		}
		
		hs := GetHomeServer()
		userID, err := hs.Auth.GetUserIDByToken(auth)
		if err != nil {
			c.JSON(http.StatusUnauthorized, err)
			c.Abort()
			return
		}
		
		// 存储用户信息
		c.Set("user_id", userID)
		c.Set("access_token", auth)
		c.Next()
	}
}

func getUserID(c *gin.Context) string {
	if userID, exists := c.Get("user_id"); exists {
		return userID.(string)
	}
	return ""
}

// ===== 服务器发现 =====

func handleWellKnownServer(c *gin.Context) {
	hs := GetHomeServer()
	c.JSON(http.StatusOK, gin.H{
		"m.server": hs.Config.ServerName + ":8448",
	})
}

func handleWellKnownClient(c *gin.Context) {
	hs := GetHomeServer()
	c.JSON(http.StatusOK, gin.H{
		"m.homeserver": gin.H{
			"base_url": hs.Config.BaseURL,
		},
	})
}

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
	c.JSON(http.StatusOK, hs.Auth.GetLoginFlows())
}

func handleLogin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	resp, err := hs.Auth.Login(&req)
	if err != nil {
		c.JSON(http.StatusUnauthorized, err)
		return
	}
	
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
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	resp, err := hs.Auth.Register(&req, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
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
	username := c.Query("username")
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
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, resp)
}

func handleGetRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	c.JSON(http.StatusOK, gin.H{
		"room_id": roomID,
	})
}

func handleJoinRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	
	hs := GetHomeServer()
	roomID, err := hs.Room.JoinRoom(userID, roomID, &JoinRoomRequest{})
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
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
	if err := hs.Room.LeaveRoom(userID, roomID, &LeaveRoomRequest{}); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

func handleForgetRoom(c *gin.Context) {
	_ = c.Param("roomId") // roomID
	c.JSON(http.StatusOK, gin.H{})
}

func handleInviteUser(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	
	var req InviteUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	if err := hs.Room.InviteUser(roomID, userID, &req); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

func handleKickUser(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	
	var req KickUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	if err := hs.Room.KickUser(roomID, userID, &req); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

func handleBanUser(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	
	var req BanUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	if err := hs.Room.BanUser(roomID, userID, &req); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

// handleUnbanUser 解封用户
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
	_ = roomID
	hs := GetHomeServer()
	
	membership := c.QueryArray("membership")
	var membershipStates []MembershipState
	for _, m := range membership {
		membershipStates = append(membershipStates, MembershipState(m))
	}
	
	members, err := hs.Room.GetRoomMembers(roomID, membershipStates)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	chunk := make([]gin.H, len(members))
	for i, m := range members {
		chunk[i] = gin.H{
			"user_id":   m.UserID,
			"membership": string(m.Membership),
		}
		if m.DisplayName != nil {
			chunk[i]["displayname"] = m.DisplayName
		}
	}
	
	c.JSON(http.StatusOK, gin.H{
		"chunk": chunk,
	})
}

func handleGetRoomState(c *gin.Context) {
	roomID := c.Param("roomId")
	hs := GetHomeServer()
	
	events, err := hs.Room.GetRoomStateAll(roomID)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, events)
}

func handleGetRoomStateEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventType := c.Param("eventType")
	stateKey := c.Param("stateKey")
	
	hs := GetHomeServer()
	
	if stateKey == "" {
		stateKey = ""
	}
	
	event, err := hs.Event.GetRoomStateEvent(roomID, eventType, stateKey)
	if err != nil {
		c.JSON(http.StatusNotFound, err)
		return
	}
	
	c.JSON(http.StatusOK, event)
}

func handleSendStateEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventType := c.Param("eventType")
	stateKey := c.Param("stateKey")
	userID := getUserID(c)
	
	var content map[string]interface{}
	if err := c.ShouldBindJSON(&content); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	resp, err := hs.Event.SendRoomEvent(roomID, userID, eventType, nil, content, &stateKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
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
	
	var content map[string]interface{}
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
	
	resp, err := hs.Event.SendRoomEvent(roomID, userID, eventType, txnIDPtr, content, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, resp)
}

func handleGetMessages(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	
	req := &GetRoomMessagesRequest{
		Dir:    c.Query("dir"),
		From:   c.Query("from"),
		To:     c.Query("to"),
		Filter: c.Query("filter"),
	}
	if limit := c.Query("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil {
			req.Limit = l
		}
	}
	
	hs := GetHomeServer()
	resp, err := hs.Event.GetRoomMessages(roomID, userID, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
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
	resp, err := hs.Event.GetRoomContext(roomID, eventID, userID, limit)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, resp)
}

func handleGetEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	
	hs := GetHomeServer()
	event, err := hs.Event.GetRoomEvent(roomID, eventID)
	if err != nil {
		c.JSON(http.StatusNotFound, err)
		return
	}
	
	c.JSON(http.StatusOK, event)
}

func handleRedactEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	txnID := c.Param("txnId")
	userID := getUserID(c)
	
	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	
	hs := GetHomeServer()
	var txnIDPtr *string
	if txnID != "" {
		txnIDPtr = &txnID
	}
	
	resp, err := hs.Event.RedactEvent(roomID, eventID, userID, req.Reason, txnIDPtr)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, resp)
}

// ===== 同步 =====

func handleSync(c *gin.Context) {
	userID := getUserID(c)
	
	req := &SyncRequest{
		Filter:      c.Query("filter"),
		Since:       c.Query("since"),
		FullState:   c.Query("full_state") == "true",
		SetPresence: c.Query("set_presence"),
	}
	if timeout := c.Query("timeout"); timeout != "" {
		if t, err := strconv.Atoi(timeout); err == nil {
			req.Timeout = t
		}
	}
	
	hs := GetHomeServer()
	resp, err := hs.Sync.Sync(userID, "", req)
	if err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, resp)
}

// ===== 回执 =====

func handleSetReceipt(c *gin.Context) {
	roomID := c.Param("roomId")
	receiptType := c.Param("receiptType")
	eventID := c.Param("eventId")
	userID := getUserID(c)
	
	hs := GetHomeServer()
	if err := hs.Sync.SetReceipt(roomID, eventID, userID, receiptType); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

func handleSetReadMarkers(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	
	var req struct {
		Read      string `json:"m.read"`
		ReadPrivate string `json:"m.read.private"`
		FullyRead  string `json:"m.fully_read"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	if err := hs.Sync.SetTyping(roomID, userID, req.Typing, req.Timeout); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 用户档案 =====

func handleGetProfile(c *gin.Context) {
	userID := c.Param("userId")
	
	hs := GetHomeServer()
	// 获取用户档案
	accountID, err := hs.Auth.GetLocalAccountID(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
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
	
	// 构建头像 MXC URL
	avatarURL := ""
	if account.Avatar != "" {
		// 将社区头像 URL 转换为 MXC 格式
		// 如果是本地文件，生成 MXC URI
		if strings.HasPrefix(account.Avatar, "/uploads/") {
			// 提取媒体 ID
			parts := strings.Split(account.Avatar, "/")
			if len(parts) >= 4 {
				mediaID := parts[len(parts)-1]
				avatarURL = fmt.Sprintf("mxc://%s/%s", hs.Config.ServerName, strings.Split(mediaID, ".")[0])
			}
		} else {
			// 外部 URL，保持原样（Matrix 客户端可能不支持）
			avatarURL = account.Avatar
		}
	}
	
	c.JSON(http.StatusOK, gin.H{
		"displayname": account.DisplayName,
		"avatar_url":  avatarURL,
	})
}

func handleGetDisplayName(c *gin.Context) {
	userID := c.Param("userId")
	c.JSON(http.StatusOK, gin.H{
		"displayname": userID,
	})
}

func handleSetDisplayName(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

func handleGetAvatarURL(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"avatar_url": "",
	})
}

func handleSetAvatarURL(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 已加入房间 =====

func handleGetJoinedRooms(c *gin.Context) {
	userID := getUserID(c)
	hs := GetHomeServer()
	
	rooms, err := hs.Room.GetJoinedRooms(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Internal server error",
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
			"device_id":     session.DeviceID,
			"display_name":  displayName,
			"last_seen_ip":  nil,
			"last_seen_ts":  session.LastActiveTS,
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
			"error":   "Device not found",
		})
		return
	}
	
	displayName := ""
	if session.DisplayName != nil {
		displayName = *session.DisplayName
	}
	
	c.JSON(http.StatusOK, gin.H{
		"device_id":     session.DeviceID,
		"display_name":  displayName,
		"last_seen_ip":  nil,
		"last_seen_ts":  session.LastActiveTS,
	})
}

func handleUpdateDevice(c *gin.Context) {
	userID := getUserID(c)
	deviceID := c.Param("deviceId")
	
	var req struct {
		DisplayName string `json:"display_name"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
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
	
	result := db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).Delete(&MatrixUserSession{})
	
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN",
			"error":   result.Error.Error(),
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 过滤器 =====

func handleCreateFilter(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"filter_id": "default",
	})
}

func handleGetFilter(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== To-Device =====

func handleSendToDevice(c *gin.Context) {
	senderID := getUserID(c)
	eventType := c.Param("eventType")
	txnID := c.Param("txnId")
	_ = txnID
	
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
	}
	
	// 解析目标用户和设备
	for userID, userData := range req {
		if userMap, ok := userData.(map[string]interface{}); ok {
			for deviceID, content := range userMap {
				contentMap, _ := content.(map[string]interface{})
				
				// 存储 To-Device 消息
				msg := &MatrixToDeviceMessage{
					Sender:       senderID,
					TargetUser:   userID,
					TargetDevice: deviceID,
					EventType:    eventType,
					Content:      contentMap,
				}
				
				if err := db.GetDB().Create(msg).Error; err != nil {
					log.Error("Failed to create to-device message: %v", err)
				}
			}
		}
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

func handleKeysUpload(c *gin.Context) {
	userID := getUserID(c)
	deviceID := c.GetString("device_id")
	if deviceID == "" {
		deviceID = "DEVICE"
	}
	
	var req UploadKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
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
	
	resp, err := keyStore.QueryDeviceKeys(&req)
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
		resp.Failures = make(map[string]interface{})
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
	
	resp, err := keyStore.ClaimOneTimeKeys(&req)
	if err != nil {
		log.Error("Failed to claim keys: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN",
			"error":   err.Error(),
		})
		return
	}
	
	if resp.OneTimeKeys == nil {
		resp.OneTimeKeys = make(map[string]map[string]map[string]string)
	}
	if resp.Failures == nil {
		resp.Failures = make(map[string]interface{})
	}
	
	c.JSON(http.StatusOK, resp)
}

func handleKeysSignaturesUpload(c *gin.Context) {
	// 简化实现：接收签名并存储
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
	}
	
	// TODO: 验证并存储签名
	// 这是交叉签名验证的一部分
	
	c.JSON(http.StatusOK, gin.H{
		"failures": gin.H{},
	})
}

// ===== Presence =====

func handleSetPresence(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

func handleGetPresence(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"presence": "online",
		"last_active_ago": 0,
	})
}

// ===== 公共房间 =====

func handleGetPublicRoomsClient(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"chunk":         []interface{}{},
		"total_room_count_estimate": 0,
	})
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
					"1":  "stable",
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

// GetPushers 获取推送器列表
func handleGetPushers(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	pushers, err := hs.Push.GetPushers(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"pushers": pushers})
}

// SetPusher 设置推送器
func handleSetPusher(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var pusher Pusher
	if err := c.ShouldBindJSON(&pusher); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.Push.SetPusher(userID, &pusher); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// GetPushRules 获取推送规则
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

// GetPushRulesGlobal 获取全局推送规则
func handleGetPushRulesGlobal(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"content":   []interface{}{},
		"override":  []interface{}{},
		"room":      []interface{}{},
		"sender":    []interface{}{},
		"underride": []interface{}{},
	})
}

// GetPushRulesScope 获取特定范围推送规则
func handleGetPushRulesScope(c *gin.Context) {
	scope := c.Param("scope")
	_ = scope
	
	c.JSON(http.StatusOK, []interface{}{})
}

// GetPushRule 获取单个推送规则
func handleGetPushRule(c *gin.Context) {
	scope := c.Param("scope")
	kind := c.Param("kind")
	ruleID := c.Param("ruleId")
	_ = scope
	_ = kind
	_ = ruleID
	
	c.JSON(http.StatusNotFound, gin.H{
		"errcode": "M_NOT_FOUND",
		"error":   "Push rule not found",
	})
}

// SetPushRule 设置推送规则
func handleSetPushRule(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// DeletePushRule 删除推送规则
func handleDeletePushRule(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// GetPushRuleActions 获取推送规则动作
func handleGetPushRuleActions(c *gin.Context) {
	c.JSON(http.StatusOK, []interface{}{})
}

// SetPushRuleActions 设置推送规则动作
func handleSetPushRuleActions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// GetPushRuleEnabled 获取推送规则启用状态
func handleGetPushRuleEnabled(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"enabled": true,
	})
}

// SetPushRuleEnabled 设置推送规则启用状态
func handleSetPushRuleEnabled(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 搜索 API =====
// 搜索相关函数已移至 search.go

// SearchRequest 搜索请求
type SearchRequest struct {
	SearchCategories struct {
		RoomEvents *RoomEventsSearch `json:"room_events,omitempty"`
		UserDirectory *UserDirectorySearch `json:"user_directory,omitempty"`
	} `json:"search_categories"`
	NextBatch string `json:"next_batch,omitempty"`
}

// RoomEventsSearch 房间事件搜索
type RoomEventsSearch struct {
	SearchTerm   string   `json:"search_term"`
	Filter       string   `json:"filter,omitempty"`
	Rooms        []string `json:"rooms,omitempty"`
	OrderBy      string   `json:"order_by,omitempty"`
	EventContext *EventContextSearch `json:"event_context,omitempty"`
	IncludeState bool     `json:"include_state,omitempty"`
	GroupBy      []string `json:"group_by,omitempty"`
}

// UserDirectorySearch 用户目录搜索
type UserDirectorySearch struct {
	SearchTerm string `json:"search_term"`
	Limit      int    `json:"limit,omitempty"`
}

// EventContextSearch 事件上下文搜索
type EventContextSearch struct {
	BeforeLimit int  `json:"before_limit,omitempty"`
	AfterLimit  int  `json:"after_limit,omitempty"`
	IncludeProfile bool `json:"include_profile,omitempty"`
}

// ===== 用户目录 API =====
// 用户目录搜索函数已移至 search.go

// ===== 房间别名 API =====
// 别名相关函数已移至 services.go

// ===== 第三方网络 API =====

// GetThirdPartyProtocol 获取第三方协议
func handleGetThirdPartyProtocol(c *gin.Context) {
	protocol := c.Param("protocol")
	_ = protocol
	
	c.JSON(http.StatusNotFound, gin.H{
		"errcode": "M_NOT_FOUND",
		"error":   "Protocol not found",
	})
}

// GetThirdPartyUser 获取第三方用户
func handleGetThirdPartyUser(c *gin.Context) {
	protocol := c.Param("protocol")
	_ = protocol
	
	c.JSON(http.StatusOK, []interface{}{})
}

// GetThirdPartyLocation 获取第三方位置
func handleGetThirdPartyLocation(c *gin.Context) {
	protocol := c.Param("protocol")
	_ = protocol
	
	c.JSON(http.StatusOK, []interface{}{})
}

// ===== OpenID API =====

// RequestOpenIDToken 请求 OpenID Token
func handleRequestOpenIDToken(c *gin.Context) {
	userID := getUserID(c)
	_ = userID
	
	// 生成 OpenID Token
	token := "openid_" + generateAccessToken()
	
	c.JSON(http.StatusOK, gin.H{
		"access_token":      token,
		"token_type":        "Bearer",
		"matrix_server_name": GetHomeServer().Config.ServerName,
		"expires_in":        3600,
	})
}

// GetOpenIDUserInfo 获取 OpenID 用户信息
func handleGetOpenIDUserInfo(c *gin.Context) {
	// 验证 access_token
	token := c.GetHeader("Authorization")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"errcode": "M_MISSING_TOKEN",
			"error":   "Missing access token",
		})
		return
	}
	
	// 验证 OpenID Token
	var session MatrixUserSession
	err := db.GetDB().Where("access_token = ?", token).First(&session).Error
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"errcode": "M_UNKNOWN_TOKEN",
			"error":   "Unknown token",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"sub": session.UserID,
	})
}

// ===== 删除群组 API =====

// DeleteGroupAlias 删除群组别名
func handleDeleteGroupAlias(c *gin.Context) {
	groupID := c.Param("groupId")
	_ = groupID
	
	c.JSON(http.StatusOK, gin.H{})
}

// ===== Appservice API =====

// AppservicePing Appservice Ping
func handleAppservicePing(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 房间标签 API =====

// GetRoomTags 获取房间标签
func handleGetRoomTags(c *gin.Context) {
	userID := c.Param("userId")
	roomID := c.Param("roomId")
	_ = userID
	_ = roomID
	
	c.JSON(http.StatusOK, gin.H{
		"tags": gin.H{},
	})
}

// SetRoomTag 设置房间标签
func handleSetRoomTag(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// DeleteRoomTag 删除房间标签
func handleDeleteRoomTag(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 账户有效期 API =====

// GetAccountValidity 获取账户有效期
func handleGetAccountValidity(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"expires_at_ms": 0,
	})
}

// RenewAccount 续期账户
func handleRenewAccount(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 举报 API =====
// 举报相关函数已移至 services.go

// ===== 忽略用户 API =====

// GetIgnoredUsers 获取忽略的用户列表
func handleGetIgnoredUsers(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"muted_users": []string{},
	})
}

// ===== 房间摘要 API =====

// GetRoomSummary 获取房间摘要
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
		"room_id":       roomID,
		"room_type":     room.RoomType,
		"name":          room.Name,
		"topic":         room.Topic,
		"avatar_url":    room.AvatarURL,
		"num_joined_members": len(members),
		"join_rule":     room.JoinRule,
		"world_readable": room.HistoryVisibility == "world_readable",
		"guest_can_join": room.GuestAccess == "can_join",
	})
}

// ===== Knocks API =====

// KnockRoom 敲门
func handleKnockRoom(c *gin.Context) {
	roomIDOrAlias := c.Param("roomIdOrAlias")
	userID := getUserID(c)
	
	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	
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
		RoomID:     roomID,
		EventType:  EventTypeRoomMember,
		Sender:     userID,
		StateKey:   &userID,
		Content: map[string]interface{}{
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

// GetSpaceHierarchy 获取空间层级
func handleGetSpaceHierarchy(c *gin.Context) {
	roomID := c.Param("roomId")
	_ = roomID
	
	c.JSON(http.StatusOK, gin.H{
		"rooms": []interface{}{},
		"next_batch": "",
	})
}

// GetSpaceSummary 获取空间摘要
func handleGetSpaceSummary(c *gin.Context) {
	roomID := c.Param("roomId")
	_ = roomID
	
	c.JSON(http.StatusOK, gin.H{
		"rooms":       []interface{}{},
		"events":      []interface{}{},
		"next_batch":  "",
	})
}

// GetSpaceSummaryClient 客户端空间摘要
func handleGetSpaceSummaryClient(c *gin.Context) {
	var req struct {
		MaxRoomsPerSpace int      `json:"max_rooms_per_space"`
		RoomID           string   `json:"room_id"`
		SuggestedOnly    bool     `json:"suggested_only"`
		AutoJoinOnly     bool     `json:"auto_join_only"`
	}
	c.ShouldBindJSON(&req)
	_ = req.RoomID
	
	c.JSON(http.StatusOK, gin.H{
		"rooms":  []interface{}{},
		"events": []interface{}{},
	})
}
