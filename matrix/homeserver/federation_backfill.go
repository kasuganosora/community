package homeserver

import (
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

// BackfillResponse Backfill 响应.
type BackfillResponse struct {
	Origin         string        `json:"origin"`
	OriginServerTS int64         `json:"origin_server_ts"`
	PDUs           []ClientEvent `json:"pdus"`
}

// RegisterBackfillRoutes 注册 Backfill 路由.
func RegisterBackfillRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	fed := r.Group("/_matrix/federation/v1")
	fed.Use(requireFederationAuth())
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
// 参数: v (事件ID列表), limit (限制数量).
func handleBackfill(c *gin.Context) {
	roomID := c.Param("roomId")
	if !requireFederationRoomAccess(c, roomID) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	roots := c.QueryArray("v")
	if len(roots) == 0 {
		federationFailure(c, 400, fmt.Errorf("at least one v event is required"))
		return
	}
	pdus, err := federationAncestors(roomID, roots, nil, limit, 0, true)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"origin": GetHomeServer().Config.ServerName, "origin_server_ts": time.Now().UnixMilli(), "pdus": pdus})
}

// handleTimestampToEvent 时间戳映射到事件
// GET /_matrix/federation/v1/timestamp_to_event/:roomId.
func handleTimestampToEvent(c *gin.Context) {
	roomID := c.Param("roomId")
	tsStr := c.Query("ts")
	dir := c.DefaultQuery("dir", "f") // f=forward, b=backward

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM", //nolint:goconst
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

	if !requireFederationRoomAccess(c, roomID) {
		return
	}

	// 查找最接近的事件
	var (
		event       MatrixEvent
		queryResult error
	)

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
		"event_id":         event.EventID, //nolint:goconst
		"origin_server_ts": event.OriginServerTS,
	})
}

// handleEventAuth 获取事件授权信息
// GET /_matrix/federation/v1/event_auth/:roomId/:eventId.
func handleEventAuth(c *gin.Context) {
	roomID := c.Param("roomId")
	if !requireFederationRoomAccess(c, roomID) {
		return
	}
	chain, err := federationAuthChain(roomID, c.Param("eventId"))
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"auth_chain": chain})
}

// QueryAuthRequest Query Auth 请求.
type QueryAuthRequest struct {
	Event ClientEvent `json:"event"`
}

// handleQueryAuth 查询授权
// POST /_matrix/federation/v1/query_auth/:roomId/:eventId.
func handleQueryAuth(c *gin.Context) {
	roomID := c.Param("roomId")
	if !requireFederationRoomAccess(c, roomID) {
		return
	}
	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		federationFailure(c, 400, err)
		return
	}
	chain, err := federationAuthChain(roomID, c.Param("eventId"))
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"auth_chain": chain, "rejects": []string{}, "missing": []string{}})
}

// GetMissingEventsRequest 获取缺失事件请求.
type GetMissingEventsRequest struct {
	EarliestEvents []string `json:"earliest_events"`
	LatestEvents   []string `json:"latest_events"`
	Limit          int      `json:"limit"`
	MinDepth       int      `json:"min_depth"`
}

// GetMissingEventsResponse 获取缺失事件响应.
type GetMissingEventsResponse struct {
	Events []ClientEvent `json:"events"`
}

// handleGetMissingEvents 获取缺失事件
// POST /_matrix/federation/v1/get_missing_events/:roomId.
func handleGetMissingEvents(c *gin.Context) {
	roomID := c.Param("roomId")
	var req GetMissingEventsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		federationFailure(c, 400, err)
		return
	}
	if !requireFederationRoomAccess(c, roomID) {
		return
	}
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 100
	}
	events, err := federationAncestors(roomID, req.LatestEvents, req.EarliestEvents, req.Limit, req.MinDepth, false)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"events": events})
}

// federationAncestors follows verified DAG references, never timestamp ordering.
// Every lookup is room-scoped, including caller-provided boundary events.
func federationAncestors(roomID string, roots, stops []string, limit, minDepth int, includeRoots bool) ([]map[string]any, error) {
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, id := range stops {
		seen[id] = true
	}
	queue := append([]string{}, roots...)
	rootSet := map[string]bool{}
	for _, id := range roots {
		rootSet[id] = true
	}
	for len(queue) > 0 && len(out) < limit {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		var event MatrixEvent
		if err := db.GetDB().Where("room_id = ? AND event_id = ?", roomID, id).First(&event).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("unknown room event %s: %w", id, err)
			}
			return nil, err
		}
		if event.Depth < int64(minDepth) {
			continue
		}
		queue = append(queue, event.PrevEvents...)
		if !includeRoots && rootSet[id] {
			continue
		}
		wire, err := matrixEventToPDU(&event)
		if err != nil {
			return nil, err
		}
		out = append(out, wire)
	}
	return out, nil
}

func federationAuthChain(roomID, eventID string) ([]map[string]any, error) {
	out := []map[string]any{}
	queue := []string{eventID}
	seen := map[string]bool{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		var event MatrixEvent
		if err := db.GetDB().Where("room_id = ? AND event_id = ?", roomID, id).First(&event).Error; err != nil {
			return nil, err
		}
		queue = append(queue, event.AuthEvents...)
		if id == eventID {
			continue
		}
		wire, err := matrixEventToPDU(&event)
		if err != nil {
			return nil, err
		}
		out = append(out, wire)
	}
	return out, nil
}

// RegisterAdditionalFederationRoutes 注册额外的联邦路由.
func RegisterAdditionalFederationRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	fed := r.Group("/_matrix/federation/v1")
	fed.Use(requireFederationAuth())
	{
		// 注册用户查询
		fed.GET("/query/user", handleQueryUser)

		// 获取用户签名
		fed.GET("/user/signing_key/:userId", handleGetUserSigningKey)
	}
}

// handleQueryUser 查询用户.
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
			"error":   "User not found", //nolint:goconst
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

// handleGetUserSigningKey 获取用户签名密钥.
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
