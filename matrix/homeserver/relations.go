package homeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

// ===== Relations API 实现 =====
// 支持回复、反应、编辑、线程等消息关系

// RelationType 关系类型
type RelationType string

const (
	RelationTypeReply   RelationType = "m.in_reply_to"   // 回复
	RelationTypeReaction RelationType = "m.annotation"   // 反应
	RelationTypeReplace  RelationType = "m.replace"       // 编辑
	RelationTypeThread   RelationType = "m.thread"        // 线程
	RelationTypeReference RelationType = "m.reference"    // 引用
)

// EventRelation 事件关系（存储在数据库中）
type EventRelation struct {
	ID             uint64       `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID        string       `gorm:"index;type:varchar(255);not null" json:"event_id"`
	RoomID         string       `gorm:"index;type:varchar(255);not null" json:"room_id"`
	RelatesTo      string       `gorm:"index;type:varchar(255)" json:"relates_to"` // 关联的目标事件ID
	RelationType   RelationType `gorm:"index;type:varchar(32);not null" json:"relation_type"`
	Sender         string       `gorm:"type:varchar(255);not null" json:"sender"`
	Content        string       `gorm:"type:text" json:"content"` // JSON
	AggregationKey string       `gorm:"type:varchar(255)" json:"aggregation_key,omitempty"` // 用于反应（emoji）
	OriginServerTS int64        `gorm:"index" json:"origin_server_ts"`
	CreatedAt      time.Time    `gorm:"autoCreateTime" json:"created_at"`
}

func (EventRelation) TableName() string {
	return "matrix_event_relations"
}

// Aggregations 聚合数据（用于 /relations API 响应）
type Aggregations struct {
	Annotations *AnnotationAggregations `json:"m.annotation,omitempty"`
	Replies     *ReplyAggregations      `json:"m.in_reply_to,omitempty"`
	Thread      *ThreadAggregations     `json:"m.thread,omitempty"`
}

// AnnotationAggregations 反应聚合
type AnnotationAggregations struct {
	Chunk []EventRelationResponse `json:"chunk"`
	Count int                     `json:"count"`
}

// ReplyAggregations 回复聚合
type ReplyAggregations struct {
	Count int `json:"count"`
}

// ThreadAggregations 线程聚合
type ThreadAggregations struct {
	LatestEvent *ClientEvent `json:"latest_event,omitempty"`
	Count       int          `json:"count"`
	CurrentUserParticipated bool `json:"current_user_participated"`
}

// EventRelationResponse 关系事件响应格式
type EventRelationResponse struct {
	EventID  string                 `json:"event_id"`
	Sender   string                 `json:"sender"`
	Type     string                 `json:"type"`
	Content  map[string]interface{} `json:"content"`
	OriginServerTS int64            `json:"origin_server_ts"`
	AggregationKey string           `json:"key,omitempty"`
	Count    int                    `json:"count,omitempty"`
}

// RelationsRequest 获取关系请求
type RelationsRequest struct {
	RelType      string `form:"rel_type"`       // m.annotation, m.in_reply_to, m.thread
	EventType    string `form:"event_type"`     // m.room.message, m.reaction
	Direction    string `form:"dir"`            // f (forward), b (backward)
	Limit        int    `form:"limit"`
	From         string `form:"from"`           // 分页令牌
}

// RelationsResponse 获取关系响应
type RelationsResponse struct {
	Chunk     []ClientEvent `json:"chunk"`
	NextBatch string        `json:"next_batch,omitempty"`
	PrevBatch string        `json:"prev_batch,omitempty"`
}

// RelationsService 关系服务
type RelationsService struct {
	HS *HomeServer
}

// NewRelationsService 创建关系服务
func NewRelationsService(hs *HomeServer) *RelationsService {
	return &RelationsService{HS: hs}
}

// StoreRelation 存储事件关系
func (rs *RelationsService) StoreRelation(event *MatrixEvent) error {
	// 解析 m.relates_to
	relatesTo, relationType, aggKey, err := rs.parseRelation(event.Content)
	if err != nil || relatesTo == "" {
		return nil // 没有关系
	}

	// 序列化内容
	contentJSON, _ := json.Marshal(event.Content)

	relation := &EventRelation{
		EventID:        event.EventID,
		RoomID:         event.RoomID,
		RelatesTo:      relatesTo,
		RelationType:   RelationType(relationType),
		Sender:         event.Sender,
		Content:        string(contentJSON),
		AggregationKey: aggKey,
		OriginServerTS: event.OriginServerTS,
	}

	return db.GetDB().Create(relation).Error
}

// parseRelation 解析事件中的关系
func (rs *RelationsService) parseRelation(content map[string]interface{}) (relatesTo, relationType, aggKey string, err error) {
	// 检查 m.relates_to
	relatesToInterface, ok := content["m.relates_to"]
	if !ok {
		return "", "", "", nil
	}

	relatesToMap, ok := relatesToInterface.(map[string]interface{})
	if !ok {
		return "", "", "", nil
	}

	// 获取 event_id
	if eventID, ok := relatesToMap["event_id"].(string); ok {
		relatesTo = eventID
	}

	// 获取 rel_type
	if rt, ok := relatesToMap["rel_type"].(string); ok {
		relationType = rt
	}

	// 处理不同类型的关系
	if relationType == "" {
		// 检查是否是旧式回复（直接 in_reply_to）
		if _, ok := relatesToMap["m.in_reply_to"]; ok {
			relationType = string(RelationTypeReply)
		}
	}

	// 获取 key（用于反应）
	if key, ok := relatesToMap["key"].(string); ok {
		aggKey = key
	}

	return
}

// GetRelations 获取事件的关联事件
func (rs *RelationsService) GetRelations(roomID, eventID string, req *RelationsRequest) (*RelationsResponse, error) {
	query := db.GetDB().Model(&EventRelation{}).
		Where("room_id = ? AND relates_to = ?", roomID, eventID)

	// 按关系类型过滤
	if req.RelType != "" {
		query = query.Where("relation_type = ?", req.RelType)
	}

	// Filter by event type in DB query (via join with events table)
	if req.EventType != "" {
		query = query.Where("event_id IN (?)",
			db.GetDB().Model(&MatrixEvent{}).Select("event_id").Where("event_type = ?", req.EventType))
	}

	// 限制返回数量
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Direction: default backward (newest first)
	dir := req.Direction
	if dir == "" {
		dir = "b"
	}

	// Pagination: parse from token (format "s<timestamp>" or "rel_<id>")
	if req.From != "" {
		var fromTS int64
		if _, err := fmt.Sscanf(req.From, "s%d", &fromTS); err != nil {
			// Try rel_ format (our own pagination token)
			fmt.Sscanf(req.From, "rel_%d", &fromTS)
		}
		if fromTS > 0 {
			if dir == "f" {
				query = query.Where("origin_server_ts > ?", fromTS)
			} else {
				query = query.Where("origin_server_ts < ?", fromTS)
			}
		}
	}

	// Order by origin_server_ts (matches sync token semantics)
	if dir == "f" {
		query = query.Order("origin_server_ts ASC, id ASC")
	} else {
		query = query.Order("origin_server_ts DESC, id DESC")
	}

	// Fetch limit+1 to determine if there are more results
	var relations []EventRelation
	if err := query.Limit(limit + 1).Find(&relations).Error; err != nil {
		return nil, err
	}

	hasMore := len(relations) > limit
	if hasMore {
		relations = relations[:limit]
	}

	// 转换为客户端事件格式
	chunk := make([]ClientEvent, 0, len(relations))
	for _, rel := range relations {
		// 加载原始事件
		var event MatrixEvent
		if err := db.GetDB().Where("event_id = ?", rel.EventID).First(&event).Error; err != nil {
			continue
		}

		clientEvent := event.ToClientEvent()
		if clientEvent.Unsigned == nil {
			clientEvent.Unsigned = make(map[string]interface{})
		}

		chunk = append(chunk, *clientEvent)
	}

	resp := &RelationsResponse{
		Chunk: chunk,
	}

	// Set next_batch if there are more results
	if hasMore && len(relations) > 0 {
		lastRel := relations[len(relations)-1]
		resp.NextBatch = fmt.Sprintf("rel_%d", lastRel.OriginServerTS)
	}

	return resp, nil
}

// GetAggregations 获取事件的聚合数据
func (rs *RelationsService) GetAggregations(roomID, eventID string, userID string) (*Aggregations, error) {
	aggregations := &Aggregations{}

	// 获取反应聚合
	annotations, err := rs.getAnnotationAggregations(roomID, eventID)
	if err == nil && annotations != nil {
		aggregations.Annotations = annotations
	}

	// 获取回复计数
	replyCount, err := rs.getReplyCount(roomID, eventID)
	if err == nil && replyCount > 0 {
		aggregations.Replies = &ReplyAggregations{Count: replyCount}
	}

	// 获取线程聚合
	threadInfo, err := rs.getThreadAggregations(roomID, eventID, userID)
	if err == nil && threadInfo != nil {
		aggregations.Thread = threadInfo
	}

	return aggregations, nil
}

// getAnnotationAggregations 获取反应聚合
func (rs *RelationsService) getAnnotationAggregations(roomID, eventID string) (*AnnotationAggregations, error) {
	var relations []EventRelation
	if err := db.GetDB().Where("room_id = ? AND relates_to = ? AND relation_type = ?",
		roomID, eventID, RelationTypeReaction).Find(&relations).Error; err != nil {
		return nil, err
	}

	if len(relations) == 0 {
		return nil, nil
	}

	// 按 emoji 分组计数
	emojiCounts := make(map[string]int)
	chunk := make([]EventRelationResponse, 0)

	for _, rel := range relations {
		emoji := rel.AggregationKey
		if emoji == "" {
			// 从内容中提取 emoji
			emoji = rs.extractEmojiFromReaction(rel.Content)
		}
		
		emojiCounts[emoji]++

		// 只添加一次每个用户的反应（最新的一次）
		found := false
		for _, existing := range chunk {
			if existing.Sender == rel.Sender {
				found = true
				break
			}
		}
		if !found {
			chunk = append(chunk, EventRelationResponse{
				EventID:  rel.EventID,
				Sender:   rel.Sender,
				Type:     "m.reaction",
				Content:  map[string]interface{}{},
				AggregationKey: emoji,
			})
		}
	}

	// 添加计数
	for i := range chunk {
		chunk[i].Count = emojiCounts[chunk[i].AggregationKey]
	}

	return &AnnotationAggregations{
		Chunk: chunk,
		Count: len(relations),
	}, nil
}

// getReplyCount 获取回复数量
func (rs *RelationsService) getReplyCount(roomID, eventID string) (int, error) {
	var count int64
	err := db.GetDB().Model(&EventRelation{}).
		Where("room_id = ? AND relates_to = ? AND relation_type = ?",
			roomID, eventID, RelationTypeReply).
		Count(&count).Error
	return int(count), err
}

// getThreadAggregations 获取线程聚合
func (rs *RelationsService) getThreadAggregations(roomID, eventID, userID string) (*ThreadAggregations, error) {
	var count int64
	err := db.GetDB().Model(&EventRelation{}).
		Where("room_id = ? AND relates_to = ? AND relation_type = ?",
			roomID, eventID, RelationTypeThread).
		Count(&count).Error
	if err != nil {
		return nil, err
	}

	if count == 0 {
		return nil, nil
	}

	// 获取最新事件
	var latestRel EventRelation
	if err := db.GetDB().Where("room_id = ? AND relates_to = ? AND relation_type = ?",
		roomID, eventID, RelationTypeThread).
		Order("created_at DESC").
		First(&latestRel).Error; err == nil {
		
		var latestEvent MatrixEvent
		if err := db.GetDB().Where("event_id = ?", latestRel.EventID).First(&latestEvent).Error; err == nil {
			clientEvent := latestEvent.ToClientEvent()
			
			// 检查当前用户是否参与
			var userCount int64
			db.GetDB().Model(&EventRelation{}).
				Where("room_id = ? AND relates_to = ? AND relation_type = ? AND sender = ?",
					roomID, eventID, RelationTypeThread, userID).
				Count(&userCount)
			
			return &ThreadAggregations{
				LatestEvent: clientEvent,
				Count:       int(count),
				CurrentUserParticipated: userCount > 0,
			}, nil
		}
	}

	return &ThreadAggregations{
		Count: int(count),
	}, nil
}

// extractEmojiFromReaction 从反应内容中提取 emoji
func (rs *RelationsService) extractEmojiFromReaction(contentJSON string) string {
	var content map[string]interface{}
	if err := json.Unmarshal([]byte(contentJSON), &content); err != nil {
		return ""
	}

	// 检查 m.relates_to.key
	if relatesTo, ok := content["m.relates_to"].(map[string]interface{}); ok {
		if key, ok := relatesTo["key"].(string); ok {
			return key
		}
	}

	return ""
}

// SendReaction 发送反应（emoji）
func (rs *RelationsService) SendReaction(roomID, sender, eventID, key string) (*SendMessageResponse, *MatrixError) {
	content := map[string]interface{}{
		"m.relates_to": map[string]interface{}{
			"rel_type":  "m.annotation",
			"event_id":  eventID,
			"key":       key,
		},
	}

	return rs.HS.Event.SendRoomEvent(roomID, sender, "m.reaction", nil, content, nil)
}

// SendReply 发送回复
func (rs *RelationsService) SendReply(roomID, sender, eventID, body, formattedBody string) (*SendMessageResponse, *MatrixError) {
	content := map[string]interface{}{
		"msgtype": "m.text",
		"body":    body,
		"m.relates_to": map[string]interface{}{
			"rel_type": "m.in_reply_to",
			"event_id": eventID,
		},
	}

	if formattedBody != "" {
		content["format"] = "org.matrix.custom.html"
		content["formatted_body"] = formattedBody
	}

	return rs.HS.Event.SendRoomEvent(roomID, sender, "m.room.message", nil, content, nil)
}

// SendEdit 发送编辑
func (rs *RelationsService) SendEdit(roomID, sender, eventID string, newBody, newFormattedBody string) (*SendMessageResponse, *MatrixError) {
	content := map[string]interface{}{
		"msgtype": "m.text",
		"body":    fmt.Sprintf("* %s", newBody),
		"m.new_content": map[string]interface{}{
			"msgtype": "m.text",
			"body":    newBody,
		},
		"m.relates_to": map[string]interface{}{
			"rel_type": "m.replace",
			"event_id": eventID,
		},
	}

	if newFormattedBody != "" {
		content["format"] = "org.matrix.custom.html"
		content["formatted_body"] = fmt.Sprintf("* %s", newFormattedBody)
		content["m.new_content"].(map[string]interface{})["format"] = "org.matrix.custom.html"
		content["m.new_content"].(map[string]interface{})["formatted_body"] = newFormattedBody
	}

	return rs.HS.Event.SendRoomEvent(roomID, sender, "m.room.message", nil, content, nil)
}

// SendThreadReply 发送线程回复
func (rs *RelationsService) SendThreadReply(roomID, sender, rootEventID, body string, isReplyInThread bool) (*SendMessageResponse, *MatrixError) {
	content := map[string]interface{}{
		"msgtype": "m.text",
		"body":    body,
		"m.relates_to": map[string]interface{}{
			"rel_type": "m.thread",
			"event_id": rootEventID,
			"is_falling_back": !isReplyInThread,
		},
	}

	return rs.HS.Event.SendRoomEvent(roomID, sender, "m.room.message", nil, content, nil)
}

// RedactEvent 删除事件（并实现关系删除）
func (rs *RelationsService) RedactEvent(roomID, sender, eventID, reason string) (*SendMessageResponse, *MatrixError) {
	content := map[string]interface{}{}
	if reason != "" {
		content["reason"] = reason
	}

	return rs.HS.Event.SendRoomEvent(roomID, sender, "m.room.redaction", nil, content, &eventID)
}

// ===== HTTP Handlers =====

// RegisterRelationsRoutes 注册关系 API 路由
func RegisterRelationsRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 需要认证的组
	relations := r.Group("/_matrix/client/v1/rooms")
	relations.Use(MatrixAuthMiddleware())
	{
		// 获取事件的关系
		relations.GET("/:roomId/relations/:eventId", handleGetRelations)
		relations.GET("/:roomId/relations/:eventId/:relType", handleGetRelationsByType)
		relations.GET("/:roomId/relations/:eventId/:relType/:eventType", handleGetRelationsByTypeAndEventType)
	}

	// 线程列表
	relations.GET("/:roomId/threads", handleGetThreads)

	// 发送反应
	r.POST("/_matrix/client/v3/rooms/:roomId/reaction/:eventId", MatrixAuthMiddleware(), handleSendReaction)
}

// handleGetRelations 获取事件的所有关系
func handleGetRelations(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	userID := c.GetString("user_id")

	// 检查用户是否在房间中
	membership, _ := hs.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		c.JSON(http.StatusForbidden, MatrixError{
			ErrCode: "M_FORBIDDEN",
			Error:   "User is not in the room",
		})
		return
	}

	// 解析请求
	var req RelationsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_INVALID_PARAM",
			Error:   err.Error(),
		})
		return
	}

	// Override with path params if set by handleGetRelationsByType/handleGetRelationsByTypeAndEventType
	if rt, exists := c.Get("path_rel_type"); exists {
		req.RelType = rt.(string)
	}
	if et, exists := c.Get("path_event_type"); exists {
		req.EventType = et.(string)
	}

	resp, err := hs.Relations.GetRelations(roomID, eventID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, MatrixError{
			ErrCode: "M_INTERNAL_ERROR",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// handleGetRelationsByType 按关系类型获取关系
func handleGetRelationsByType(c *gin.Context) {
	c.Set("path_rel_type", c.Param("relType"))
	handleGetRelations(c)
}

// handleGetRelationsByTypeAndEventType 按关系类型和事件类型获取关系
func handleGetRelationsByTypeAndEventType(c *gin.Context) {
	c.Set("path_rel_type", c.Param("relType"))
	c.Set("path_event_type", c.Param("eventType"))
	handleGetRelations(c)
}

// handleGetThreads returns thread roots in the room, ordered by latest reply
func handleGetThreads(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")
	userID := c.GetString("user_id")

	membership, _ := hs.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		c.JSON(http.StatusForbidden, MatrixError{ErrCode: "M_FORBIDDEN", Error: "User is not in the room"})
		return
	}

	// Find all distinct thread root event IDs
	var threadRoots []string
	db.GetDB().Model(&EventRelation{}).
		Where("room_id = ? AND relation_type = ?", roomID, RelationTypeThread).
		Distinct("relates_to").
		Pluck("relates_to", &threadRoots)

	type threadEntry struct {
		RootEvent   *ClientEvent
		LatestEvent *ClientEvent
		LatestTS    int64
		Count       int
	}

	entries := make([]threadEntry, 0, len(threadRoots))
	for _, rootID := range threadRoots {
		var rootEvent MatrixEvent
		if db.GetDB().Where("event_id = ?", rootID).First(&rootEvent).Error != nil {
			continue
		}

		// Get count and latest reply
		var count int64
		db.GetDB().Model(&EventRelation{}).
			Where("room_id = ? AND relates_to = ? AND relation_type = ?", roomID, rootID, RelationTypeThread).
			Count(&count)

		var latestRel EventRelation
		db.GetDB().Where("room_id = ? AND relates_to = ? AND relation_type = ?", roomID, rootID, RelationTypeThread).
			Order("created_at DESC").First(&latestRel)

		var latestEvent MatrixEvent
		if db.GetDB().Where("event_id = ?", latestRel.EventID).First(&latestEvent).Error != nil {
			continue
		}

		ce := rootEvent.ToClientEvent()
		latestCE := latestEvent.ToClientEvent()

		// Add thread aggregation to unsigned
		if ce.Unsigned == nil {
			ce.Unsigned = make(map[string]interface{})
		}
		ce.Unsigned["m.relations"] = map[string]interface{}{
			"m.thread": map[string]interface{}{
				"latest_event":             latestCE,
				"count":                    count,
				"current_user_participated": latestEvent.Sender == userID || rootEvent.Sender == userID,
			},
		}

		entries = append(entries, threadEntry{
			RootEvent:   ce,
			LatestEvent: latestCE,
			LatestTS:    latestEvent.OriginServerTS,
			Count:       int(count),
		})
	}

	// Sort by latest reply timestamp descending
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].LatestTS > entries[i].LatestTS {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	chunk := make([]interface{}, len(entries))
	for i, e := range entries {
		chunk[i] = e.RootEvent
	}

	c.JSON(http.StatusOK, gin.H{
		"chunk":      chunk,
		"next_batch": "",
	})
}

// handleSendReaction 发送反应
func handleSendReaction(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	userID := c.GetString("user_id")

	var req struct {
		Key string `json:"key" binding:"required"` // emoji
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_BAD_JSON",
			Error:   err.Error(),
		})
		return
	}

	resp, matrixErr := hs.Relations.SendReaction(roomID, userID, eventID, req.Key)
	if matrixErr != nil {
		c.JSON(http.StatusBadRequest, matrixErr)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// MatrixAuthMiddleware Matrix 认证中间件（简化版）
func MatrixAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 Authorization 头获取 Token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// 检查查询参数中的 access_token
			authHeader = "Bearer " + c.Query("access_token")
		}

		if authHeader == "" || len(authHeader) < 8 {
			c.JSON(http.StatusUnauthorized, MatrixError{
				ErrCode: "M_MISSING_TOKEN",
				Error:   "Missing access token",
			})
			c.Abort()
			return
		}

		// 提取 token
		token := authHeader[7:] // 去掉 "Bearer "

		// 验证 token
		hs := GetHomeServer()
		if hs == nil {
			c.JSON(http.StatusServiceUnavailable, MatrixError{
				ErrCode: "M_UNKNOWN",
				Error:   "Homeserver not available",
			})
			c.Abort()
			return
		}

		session, matrixErr := hs.Auth.GetDeviceByToken(token)
		if matrixErr != nil {
			c.JSON(http.StatusUnauthorized, MatrixError{
				ErrCode: "M_UNKNOWN_TOKEN",
				Error:   "Invalid access token",
			})
			c.Abort()
			return
		}

		c.Set("user_id", session.UserID)
		c.Set("device_id", session.DeviceID)
		c.Next()
	}
}

// AddAggregationsToEvent 为事件添加聚合数据
func AddAggregationsToEvent(event *ClientEvent, userID string) {
	hs := GetHomeServer()
	if hs == nil || hs.Relations == nil {
		return
	}

	aggregations, err := hs.Relations.GetAggregations(event.RoomID, event.EventID, userID)
	if err != nil {
		return
	}

	if aggregations.Annotations != nil || aggregations.Replies != nil || aggregations.Thread != nil {
		if event.Unsigned == nil {
			event.Unsigned = make(map[string]interface{})
		}
		event.Unsigned["m.relations"] = aggregations
	}
}

// init 初始化时注册数据库迁移
func init() {
	// 数据库迁移将在 homeserver 初始化时执行
}
