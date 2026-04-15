package homeserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

// BackfillResponse Backfill 响应
type BackfillResponse struct {
	Origin         string        `json:"origin"`
	OriginServerTS int64         `json:"origin_server_ts"`
	PDUs           []ClientEvent `json:"pdus"`
}

// RegisterBackfillRoutes 注册 Backfill 路由
func RegisterBackfillRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	fed := r.Group("/_matrix/federation/v1")
	{
		// Backfill - 获取房间历史事件
		fed.GET("/backfill/:roomId", handleBackfill)

		// Timestamp to Event - 时间戳映射到事件
		fed.GET("/timestamp_to_event/:roomId", handleTimestampToEvent)

		// Event Authorization
		fed.GET("/event_auth/:roomId/:eventId", handleEventAuth)

		// Query Auth Chain
		fed.POST("/query_auth/:roomId/:eventId", handleQueryAuth)

		// Get Missing Events
		fed.POST("/get_missing_events/:roomId", handleGetMissingEvents)
	}
}

// handleBackfill 处理 Backfill 请求
// GET /_matrix/federation/v1/backfill/:roomId
// 参数: v (事件ID列表), limit (限制数量)
func handleBackfill(c *gin.Context) {
	roomID := c.Param("roomId")
	hs := GetHomeServer()

	// 解析查询参数
	v := c.QueryArray("v") // 最早已知的事件ID列表
	limitStr := c.DefaultQuery("limit", "100")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	// 验证房间是否存在
	room, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}

	// Find the starting point timestamp from the v events
	var events []MatrixEvent
		startTS := time.Now().UnixMilli()
	if len(v) > 0 {
		var vEvent MatrixEvent
		// Use the EARLIEST v event's timestamp to walk backward from
		result := db.GetDB().Where("event_id IN ?", v).Order("origin_server_ts ASC").First(&vEvent)
		if result.Error == nil {
			startTS = vEvent.OriginServerTS
		}
	}

	// Query events at or before the starting point (include v events per spec)
	query := db.GetDB().Where("room_id = ? AND origin_server_ts <= ?", roomID, startTS).
		Order("origin_server_ts DESC").
		Limit(limit)
	query.Find(&events)

	// Reverse to chronological order
	pdus := make([]ClientEvent, len(events))
	for i, event := range events {
		pdus[len(events)-1-i] = *event.ToClientEvent()
	}

	// Build auth chain from room state events
	authChain := buildRoomAuthChain(roomID)
	_ = room

	c.JSON(http.StatusOK, gin.H{
		"origin":           hs.Config.ServerName,
		"origin_server_ts": time.Now().UnixMilli(),
		"pdus":             pdus,
		"auth_chain":       authChain,
	})
}

// buildRoomAuthChain returns auth chain events (create, power_levels, join_rules) for a room.
func buildRoomAuthChain(roomID string) []ClientEvent {
	authTypes := []string{
		string(EventTypeRoomCreate),
		string(EventTypeRoomPowerLevels),
		string(EventTypeRoomJoinRules),
		string(EventTypeRoomMember),
	}
	var stateEntries []MatrixRoomState
	db.GetDB().Where("room_id = ? AND event_type IN ?", roomID, authTypes).Find(&stateEntries)

	chain := make([]ClientEvent, 0, len(stateEntries))
	for _, entry := range stateEntries {
		var evt MatrixEvent
		if db.GetDB().Where("event_id = ?", entry.EventID).First(&evt).Error == nil {
			chain = append(chain, *evt.ToClientEvent())
		}
	}
	return chain
}

// handleTimestampToEvent 时间戳映射到事件
// GET /_matrix/federation/v1/timestamp_to_event/:roomId
func handleTimestampToEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	tsStr := c.Query("ts")
	dir := c.DefaultQuery("dir", "f") // f=forward, b=backward

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   "Invalid timestamp",
		})
		return
	}

	hs := GetHomeServer()

	// 验证房间
	_, err = hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}

	// 查找最接近的事件
	var event MatrixEvent
	var queryResult error

	if dir == "f" {
		// 向前查找：>= ts 的最早事件
		queryResult = db.GetDB().Where("room_id = ? AND origin_server_ts >= ?", roomID, ts).
			Order("origin_server_ts ASC").
			First(&event).Error
	} else {
		// 向后查找：<= ts 的最晚事件
		queryResult = db.GetDB().Where("room_id = ? AND origin_server_ts <= ?", roomID, ts).
			Order("origin_server_ts DESC").
			First(&event).Error
	}

	if queryResult != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "No event found near timestamp",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"event_id":         event.EventID,
		"origin_server_ts": event.OriginServerTS,
	})
}

// handleEventAuth 获取事件授权信息
// GET /_matrix/federation/v1/event_auth/:roomId/:eventId
func handleEventAuth(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	hs := GetHomeServer()

	// 验证房间
	_, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}

	// 获取目标事件
	var targetEvent MatrixEvent
	if err := db.GetDB().Where("event_id = ?", eventID).First(&targetEvent).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Event not found",
		})
		return
	}

	// 获取授权链
	authEvents := []ClientEvent{}
	if targetEvent.AuthEvents != nil {
		for _, authEventID := range targetEvent.AuthEvents {
			var authEvent MatrixEvent
			if err := db.GetDB().Where("event_id = ?", authEventID).First(&authEvent).Error; err == nil {
				authEvents = append(authEvents, *authEvent.ToClientEvent())
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"auth_events": authEvents,
	})
}

// QueryAuthRequest Query Auth 请求
type QueryAuthRequest struct {
	Event ClientEvent `json:"event"`
}

// handleQueryAuth 查询授权
// POST /_matrix/federation/v1/query_auth/:roomId/:eventId
func handleQueryAuth(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")

	var req QueryAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
	}

	hs := GetHomeServer()

	// 验证房间
	_, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}

	// 获取授权链
	authChain := []ClientEvent{}
	authEvents := []ClientEvent{}

	// 获取请求事件的授权事件
	if req.Event.EventID != "" {
		var event MatrixEvent
		if err := db.GetDB().Where("event_id = ?", req.Event.EventID).First(&event).Error; err == nil {
			// 解析授权事件
			if event.AuthEvents != nil {
				for _, authEventID := range event.AuthEvents {
					var authEvent MatrixEvent
					if err := db.GetDB().Where("event_id = ?", authEventID).First(&authEvent).Error; err == nil {
						authEvents = append(authEvents, *authEvent.ToClientEvent())
						authChain = append(authChain, *authEvent.ToClientEvent())
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"auth_chain":  authChain,
		"auth_events": authEvents,
	})

	_ = eventID
}

// GetMissingEventsRequest 获取缺失事件请求
type GetMissingEventsRequest struct {
	EarliestEvents []string `json:"earliest_events"`
	LatestEvents   []string `json:"latest_events"`
	Limit          int      `json:"limit"`
	MinDepth       int      `json:"min_depth"`
}

// GetMissingEventsResponse 获取缺失事件响应
type GetMissingEventsResponse struct {
	Events []ClientEvent `json:"events"`
}

// handleGetMissingEvents 获取缺失事件
// POST /_matrix/federation/v1/get_missing_events/:roomId
func handleGetMissingEvents(c *gin.Context) {
	roomID := c.Param("roomId")

	var req GetMissingEventsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
	}

	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 100
	}

	hs := GetHomeServer()

	// 验证房间
	_, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}

	// Build set of earliest event IDs (already known by requester, should be excluded)
	earliestSet := make(map[string]bool)
	for _, eid := range req.EarliestEvents {
		earliestSet[eid] = true
	}

	// Get timestamp bounds from earliest and latest events
	var earliestTS int64 = 0
	if len(req.EarliestEvents) > 0 {
		var earliestEvent MatrixEvent
		if err := db.GetDB().Where("event_id IN ?", req.EarliestEvents).
			Order("origin_server_ts ASC").
			First(&earliestEvent).Error; err == nil {
			earliestTS = earliestEvent.OriginServerTS
		}
	}

		latestTS := time.Now().UnixMilli()
	if len(req.LatestEvents) > 0 {
		var latestEvent MatrixEvent
		if err := db.GetDB().Where("event_id IN ?", req.LatestEvents).
			Order("origin_server_ts DESC").
			First(&latestEvent).Error; err == nil {
			latestTS = latestEvent.OriginServerTS
		}
	}

	// Query events between earliest and latest (inclusive on both sides to handle
	// same-timestamp events, then filter out earliest_events by ID)
	var events []MatrixEvent
	query := db.GetDB().Where("room_id = ? AND origin_server_ts >= ? AND origin_server_ts <= ?",
		roomID, earliestTS, latestTS).
		Order("origin_server_ts ASC")

	if req.MinDepth > 0 {
		query = query.Where("depth >= ?", req.MinDepth)
	}

	query.Limit(req.Limit + len(req.EarliestEvents)).Find(&events)

	// Filter out earliest_events (already known) and latest_events (already known)
	latestSet := make(map[string]bool)
	for _, eid := range req.LatestEvents {
		latestSet[eid] = true
	}
	result := make([]ClientEvent, 0, len(events))
	for _, event := range events {
		if earliestSet[event.EventID] || latestSet[event.EventID] {
			continue
		}
		result = append(result, *event.ToClientEvent())
		if len(result) >= req.Limit {
			break
		}
	}

	c.JSON(http.StatusOK, GetMissingEventsResponse{
		Events: result,
	})
}

// RegisterAdditionalFederationRoutes 注册额外的联邦路由
func RegisterAdditionalFederationRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	fed := r.Group("/_matrix/federation/v1")
	{
		// 注册用户查询
		fed.GET("/query/user", handleQueryUser)

		// 获取用户签名
		fed.GET("/user/signing_key/:userId", handleGetUserSigningKey)
	}
}

// handleQueryUser 查询用户
func handleQueryUser(c *gin.Context) {
	userID := c.Query("user_id")

	hs := GetHomeServer()

	// 解析用户ID
	localpart, domain, ok := ParseMatrixUserID(userID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   "Invalid user ID",
		})
		return
	}

	// 如果不是本地用户，返回错误
	if domain != hs.Config.ServerName {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
		})
		return
	}

	// 返回用户信息
	c.JSON(http.StatusOK, gin.H{
		"user_id":     userID,
		"server_name": hs.Config.ServerName,
		"localpart":   localpart,
	})
}

// handleGetUserSigningKey 获取用户签名密钥
func handleGetUserSigningKey(c *gin.Context) {
	userID := c.Param("userId")

	hs := GetHomeServer()

	// 检查是否是本地用户
	_, domain, ok := ParseMatrixUserID(userID)
	if !ok || domain != hs.Config.ServerName {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
		})
		return
	}

	// TODO: 返回用户的交叉签名密钥
	c.JSON(http.StatusOK, gin.H{
		"user_id": userID,
		"keys":    gin.H{},
	})
}
