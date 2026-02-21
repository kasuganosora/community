package homeserver

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// ===== MSC2716: 批量发送端点实现 =====
// 用于桥接器批量导入历史消息

// BatchSendRequest 批量发送请求
type BatchSendRequest struct {
	Events          []BatchEvent `json:"events" binding:"required"`
	StateEvents     []BatchEvent `json:"state_events_at_start,omitempty"`
	PrevEventID     string       `json:"prev_event_id,omitempty"`
}

// BatchEvent 批量事件
type BatchEvent struct {
	Type           string                 `json:"type"`
	Sender         string                 `json:"sender"`
	Content        map[string]interface{} `json:"content"`
	Timestamp      int64                  `json:"timestamp,omitempty"`
	Unsigned       map[string]interface{} `json:"unsigned,omitempty"`
	StateKey       *string                `json:"state_key,omitempty"`
}

// BatchSendResponse 批量发送响应
type BatchSendResponse struct {
	EventIDs []string `json:"event_ids"`
}

// BatchSendService 批量发送服务
type BatchSendService struct {
	hs *HomeServer
}

// NewBatchSendService 创建批量发送服务
func NewBatchSendService(hs *HomeServer) *BatchSendService {
	return &BatchSendService{hs: hs}
}

// BatchSend 批量发送事件到房间
func (bss *BatchSendService) BatchSend(roomID string, req *BatchSendRequest) (*BatchSendResponse, error) {
	// 验证房间存在
	_, err := bss.hs.Room.GetRoom(roomID)
	if err != nil {
		return nil, fmt.Errorf("room not found")
	}

	// 获取或创建虚拟用户（用于桥接）
	// 批量发送通常由应用服务或管理员执行

	// 按时间戳排序事件
	sort.Slice(req.Events, func(i, j int) bool {
		return req.Events[i].Timestamp < req.Events[j].Timestamp
	})

	eventIDs := make([]string, 0, len(req.Events))
	var prevEvents []string

	// 如果指定了前序事件，使用它
	if req.PrevEventID != "" {
		prevEvents = []string{req.PrevEventID}
	} else {
		// 获取房间的最后一个事件
		prevEvents = bss.getLatestEvents(roomID)
	}

	// 深度计算
	depth := bss.getNextDepth(roomID)

	// 处理状态事件
	if len(req.StateEvents) > 0 {
		for _, stateEvent := range req.StateEvents {
			eventID, err := bss.createEvent(roomID, &stateEvent, prevEvents, depth, true)
			if err != nil {
				log.Error("Failed to create state event: %v", err)
				continue
			}
			eventIDs = append(eventIDs, eventID)
			prevEvents = []string{eventID}
			depth++
		}
	}

	// 处理消息事件
	for _, event := range req.Events {
		eventID, err := bss.createEvent(roomID, &event, prevEvents, depth, false)
		if err != nil {
			log.Error("Failed to create event: %v", err)
			continue
		}
		eventIDs = append(eventIDs, eventID)
		prevEvents = []string{eventID}
		depth++
	}

	// 更新房间的最后事件
	if len(eventIDs) > 0 {
		lastEventID := eventIDs[len(eventIDs)-1]
		now := time.Now().UnixMilli()
		db.GetDB().Model(&MatrixRoom{}).Where("room_id = ?", roomID).Updates(map[string]interface{}{
			"last_event_id": lastEventID,
			"last_event_ts": now,
		})
	}

	return &BatchSendResponse{
		EventIDs: eventIDs,
	}, nil
}

// createEvent 创建单个事件
func (bss *BatchSendService) createEvent(roomID string, batchEvent *BatchEvent, prevEvents []string, depth int64, isState bool) (string, error) {
	eventID := "$" + uuid.New().String() + ":" + bss.hs.Config.ServerName

	timestamp := batchEvent.Timestamp
	if timestamp == 0 {
		timestamp = time.Now().UnixMilli()
	}

	event := &MatrixEvent{
		EventID:        eventID,
		RoomID:         roomID,
		EventType:      EventType(batchEvent.Type),
		Sender:         batchEvent.Sender,
		Content:        batchEvent.Content,
		PrevEvents:     prevEvents,
		Depth:          depth,
		OriginServerTS: timestamp,
		SenderDomain:   bss.extractDomain(batchEvent.Sender),
	}

	if isState && batchEvent.StateKey != nil {
		event.StateKey = batchEvent.StateKey
	}

	// 保存到数据库
	if err := db.GetDB().Create(event).Error; err != nil {
		return "", err
	}

	// 如果是状态事件，更新房间状态
	if isState && event.StateKey != nil {
		bss.hs.Room.updateRoomState(db.GetDB(), roomID, event)
	}

	// 存储事件关系
	if bss.hs.Relations != nil {
		go bss.hs.Relations.StoreRelation(event)
	}

	return eventID, nil
}

// getLatestEvents 获取房间的最新事件
func (bss *BatchSendService) getLatestEvents(roomID string) []string {
	var room MatrixRoom
	if err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error; err != nil {
		return []string{}
	}

	if room.LastEventID != nil && *room.LastEventID != "" {
		return []string{*room.LastEventID}
	}

	return []string{}
}

// getNextDepth 获取下一个深度
func (bss *BatchSendService) getNextDepth(roomID string) int64 {
	var maxDepth int64
	db.GetDB().Model(&MatrixEvent{}).Where("room_id = ?", roomID).Select("COALESCE(MAX(depth), 0)").Scan(&maxDepth)
	return maxDepth + 1
}

// extractDomain 从用户 ID 提取域名
func (bss *BatchSendService) extractDomain(userID string) string {
	parts := splitUserID(userID)
	if len(parts) == 2 {
		return parts[1]
	}
	return bss.hs.Config.ServerName
}

// splitUserID 分割用户 ID
func splitUserID(userID string) []string {
	// @user:server -> [@user, server]
	atIndex := -1
	for i, c := range userID {
		if c == ':' {
			atIndex = i
			break
		}
	}
	if atIndex == -1 {
		return []string{userID}
	}
	return []string{userID[:atIndex], userID[atIndex+1:]}
}

// ImportHistoricalMessages 导入历史消息（桥接器专用）
func (bss *BatchSendService) ImportHistoricalMessages(roomID string, events []BatchEvent, sender string) (*BatchSendResponse, error) {
	// 验证发送者权限
	// 只有应用服务或管理员可以导入历史消息

	// 确保所有事件都有时间戳
	for i := range events {
		if events[i].Timestamp == 0 {
			events[i].Timestamp = time.Now().UnixMilli()
		}
		if events[i].Sender == "" {
			events[i].Sender = sender
		}
	}

	req := &BatchSendRequest{
		Events: events,
	}

	return bss.BatchSend(roomID, req)
}

// ===== HTTP Handlers =====

// RegisterBatchSendRoutes 注册批量发送路由
func RegisterBatchSendRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 批量发送端点（MSC2716）
	batch := r.Group("/_matrix/client/v1/rooms")
	batch.Use(MatrixAuthMiddleware())
	{
		batch.POST("/:roomId/batch_send", handleBatchSend)
	}

	// 桥接器专用端点
	bridge := r.Group("/_matrix/client/unstable/rooms")
	bridge.Use(MatrixAuthMiddleware())
	{
		bridge.POST("/:roomId/batch_send", handleBatchSend)
	}
}

// handleBatchSend 处理批量发送请求
func handleBatchSend(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")

	// 检查用户权限（简化实现）
	membership, _ := hs.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "User is not in the room",
		})
		return
	}

	var req BatchSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	resp, err := hs.BatchSend.BatchSend(roomID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
