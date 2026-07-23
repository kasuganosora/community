package homeserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

// ===== Matrix Spaces 实现 =====
// Spaces 允许用户组织和导航房间，类似于文件夹或服务器

// Space constants.
const (
	EventTypeSpaceParent = "m.space.parent"
	EventTypeSpaceChild  = "m.space.child"
	RoomTypeSpace        = "m.space"
)

// SpaceSummary 空间摘要.
type SpaceSummary struct {
	RoomID          string           `json:"room_id"`
	Name            string           `json:"name,omitempty"`
	Topic           string           `json:"topic,omitempty"`
	AvatarURL       string           `json:"avatar_url,omitempty"`
	CanonicalAlias  string           `json:"canonical_alias,omitempty"`
	JoinedMembers   int              `json:"num_joined_members"`
	JoinedMemberIDs []string         `json:"joined_member_ids,omitempty"`
	Children        []SpaceChildInfo `json:"children,omitempty"`
}

// SpaceChildInfo 空间子房间信息.
type SpaceChildInfo struct {
	RoomID    string   `json:"room_id"`
	Name      string   `json:"name,omitempty"`
	Topic     string   `json:"topic,omitempty"`
	AvatarURL string   `json:"avatar_url,omitempty"`
	Type      string   `json:"type,omitempty"` // space, room
	Suggested bool     `json:"suggested,omitempty"`
	Order     string   `json:"order,omitempty"` // 排序权重
	Via       []string `json:"via,omitempty"`   // 联邦服务器列表
}

// SpaceHierarchyResponse 空间层级响应.
type SpaceHierarchyResponse struct {
	Rooms     []SpaceRoomInfo `json:"rooms"`
	NextBatch string          `json:"next_batch,omitempty"`
}

// SpaceRoomInfo 空间中的房间信息.
type SpaceRoomInfo struct {
	RoomID           string           `json:"room_id"`
	Name             string           `json:"name,omitempty"`
	Topic            string           `json:"topic,omitempty"`
	AvatarURL        string           `json:"avatar_url,omitempty"`
	CanonicalAlias   string           `json:"canonical_alias,omitempty"`
	WorldReadable    bool             `json:"world_readable"`
	GuestCanJoin     bool             `json:"guest_can_join"`
	JoinRule         string           `json:"join_rule,omitempty"`
	RoomType         string           `json:"room_type,omitempty"`
	NumJoinedMembers int              `json:"num_joined_members"`
	ChildrenState    []SpaceStateInfo `json:"children_state,omitempty"`
}

// SpaceStateInfo 空间状态信息.
type SpaceStateInfo struct {
	Type     string         `json:"type"`
	StateKey string         `json:"state_key"`
	Content  map[string]any `json:"content"`
	Sender   string         `json:"sender"`
}

// SpacesService 空间服务.
type SpacesService struct {
	hs *HomeServer
}

// NewSpacesService 创建空间服务.
func NewSpacesService(hs *HomeServer) *SpacesService {
	return &SpacesService{hs: hs}
}

// CreateSpace 创建空间.
func (ss *SpacesService) CreateSpace(userID string, req *CreateRoomRequest) (*CreateRoomResponse, *MatrixError) {
	// 设置房间类型为 space
	if req.CreationContent == nil {
		req.CreationContent = make(map[string]any)
	}

	req.CreationContent["type"] = RoomTypeSpace

	// 空间默认需要一些特定的初始状态
	// 创建房间
	return ss.hs.Room.CreateRoom(userID, req)
}

// AddSpaceChild 添加子房间到空间.
func (ss *SpacesService) AddSpaceChild(userID, spaceID, childRoomID string, suggested bool, order string) *MatrixError {
	// 检查用户是否有权限修改空间
	if !ss.hs.Event.canUserSendStateEvent(spaceID, userID, EventType(EventTypeSpaceChild)) {
		return ErrForbidden
	}

	// 创建 m.space.child 状态事件
	content := map[string]any{
		"via": getServerList(ss.hs.Config.ServerName),
	}
	if suggested {
		content["suggested"] = true
	}

	if order != "" {
		content["order"] = order
	}

	stateKey := childRoomID
	_, err := ss.hs.Event.SendRoomEvent(spaceID, userID, string(EventTypeSpaceChild), nil, content, &stateKey)

	return err
}

// RemoveSpaceChild 从空间移除子房间.
func (ss *SpacesService) RemoveSpaceChild(userID, spaceID, childRoomID string) *MatrixError {
	// 检查权限
	if !ss.hs.Event.canUserSendStateEvent(spaceID, userID, EventType(EventTypeSpaceChild)) {
		return ErrForbidden
	}

	// 发送空内容的状态事件来移除
	content := map[string]any{}
	stateKey := childRoomID
	_, err := ss.hs.Event.SendRoomEvent(spaceID, userID, string(EventTypeSpaceChild), nil, content, &stateKey)

	return err
}

// SetSpaceParent 设置房间的父空间.
func (ss *SpacesService) SetSpaceParent(userID, roomID, parentSpaceID string, canonical bool) *MatrixError {
	// 检查房间权限
	if !ss.hs.Event.canUserSendStateEvent(roomID, userID, EventType(EventTypeSpaceParent)) {
		return ErrForbidden
	}

	// 创建 m.space.parent 状态事件
	content := map[string]any{
		"via": getServerList(ss.hs.Config.ServerName),
	}
	if canonical {
		content["canonical"] = true
	}

	stateKey := parentSpaceID
	_, err := ss.hs.Event.SendRoomEvent(roomID, userID, string(EventTypeSpaceParent), nil, content, &stateKey)

	return err
}

// GetSpaceHierarchy 获取空间层级结构.
func (ss *SpacesService) GetSpaceHierarchy(userID, spaceID string, limit, maxDepth int, from string) (*SpaceHierarchyResponse, error) {
	// 检查用户是否可以访问空间
	membership, _ := ss.hs.Room.GetUserMembership(spaceID, userID)
	if membership != MembershipJoin {
		// 检查房间是否是公开的
		room, err := ss.hs.Room.GetRoom(spaceID)
		if err != nil || room.JoinRule != "public" {
			return nil, errors.New("access denied")
		}
	}

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	if maxDepth <= 0 || maxDepth > 10 {
		maxDepth = 3
	}

	// 获取空间的直接子房间
	children, err := ss.getSpaceChildren(spaceID)
	if err != nil {
		return nil, err
	}

	// 构建响应
	rooms := make([]SpaceRoomInfo, 0)
	visited := make(map[string]bool)
	visited[spaceID] = true

	// 添加空间本身的信息
	spaceInfo, err := ss.getSpaceRoomInfo(spaceID)
	if err == nil {
		rooms = append(rooms, *spaceInfo)
	}

	// 递归获取子空间
	ss.collectHierarchy(userID, children, &rooms, visited, maxDepth-1, limit)

	return &SpaceHierarchyResponse{
		Rooms: rooms,
	}, nil
}

// getSpaceChildren 获取空间的子房间.
func (ss *SpacesService) getSpaceChildren(spaceID string) ([]SpaceChildInfo, error) {
	// 查询 m.space.child 状态事件
	var events []MatrixEvent
	err := db.GetDB().Where("room_id = ? AND event_type = ?", spaceID, EventTypeSpaceChild).Find(&events).Error
	if err != nil {
		return nil, err
	}

	children := make([]SpaceChildInfo, 0, len(events))

	for _, event := range events {
		if event.StateKey == nil {
			continue
		}

		childRoomID := *event.StateKey

		// 获取子房间信息
		childInfo := SpaceChildInfo{
			RoomID: childRoomID,
			Via:    []string{ss.hs.Config.ServerName},
		}

		// 解析内容
		if via, ok := event.Content["via"].([]any); ok {
			childInfo.Via = make([]string, len(via))

			for i, v := range via {
				if s, ok := v.(string); ok {
					childInfo.Via[i] = s
				}
			}
		}

		if suggested, ok := event.Content["suggested"].(bool); ok {
			childInfo.Suggested = suggested
		}

		if order, ok := event.Content["order"].(string); ok {
			childInfo.Order = order
		}

		// 获取房间基本信息
		var room MatrixRoom
		err := db.GetDB().Where("room_id = ?", childRoomID).First(&room).Error
		if err == nil {
			if room.Name != nil {
				childInfo.Name = *room.Name
			}

			if room.Topic != nil {
				childInfo.Topic = *room.Topic
			}

			if room.AvatarURL != nil {
				childInfo.AvatarURL = *room.AvatarURL
			}

			if room.RoomType != nil && *room.RoomType == RoomTypeSpace {
				childInfo.Type = "space"
			} else {
				childInfo.Type = "room"
			}
		}

		children = append(children, childInfo)
	}

	return children, nil
}

// collectHierarchy 递归收集层级结构.
func (ss *SpacesService) collectHierarchy(userID string, children []SpaceChildInfo, rooms *[]SpaceRoomInfo, visited map[string]bool, depth, limit int) { //nolint:unparam // param reserved for future use
	if depth <= 0 || len(*rooms) >= limit {
		return
	}

	for _, child := range children {
		if visited[child.RoomID] {
			continue
		}

		visited[child.RoomID] = true

		roomInfo, err := ss.getSpaceRoomInfo(child.RoomID)
		if err != nil {
			continue
		}

		*rooms = append(*rooms, *roomInfo)

		// 如果是子空间，递归获取其子房间
		if child.Type == "space" {
			subChildren, err := ss.getSpaceChildren(child.RoomID)
			if err == nil {
				ss.collectHierarchy(userID, subChildren, rooms, visited, depth-1, limit)
			}
		}

		if len(*rooms) >= limit {
			break
		}
	}
}

// getSpaceRoomInfo 获取空间房间信息.
func (ss *SpacesService) getSpaceRoomInfo(roomID string) (*SpaceRoomInfo, error) {
	var room MatrixRoom
	err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error
	if err != nil {
		return nil, err
	}

	info := &SpaceRoomInfo{
		RoomID:        roomID,
		JoinRule:      room.JoinRule,
		WorldReadable: room.HistoryVisibility == "world_readable", //nolint:goconst
		GuestCanJoin:  room.GuestAccess == "can_join",
	}

	if room.Name != nil {
		info.Name = *room.Name
	}

	if room.Topic != nil {
		info.Topic = *room.Topic
	}

	if room.AvatarURL != nil {
		info.AvatarURL = *room.AvatarURL
	}

	if room.RoomType != nil {
		info.RoomType = *room.RoomType
	}

	// 获取成员数
	var memberCount int64

	db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Count(&memberCount)
	info.NumJoinedMembers = int(memberCount)

	return info, nil
}

// GetSpaceSummary 获取空间摘要.
func (ss *SpacesService) GetSpaceSummary(userID, spaceID string) (*SpaceSummary, error) {
	// 检查访问权限
	membership, _ := ss.hs.Room.GetUserMembership(spaceID, userID)
	if membership != MembershipJoin {
		return nil, errors.New("access denied")
	}

	var room MatrixRoom
	if err := db.GetDB().Where("room_id = ?", spaceID).First(&room).Error; err != nil {
		return nil, err
	}

	summary := &SpaceSummary{
		RoomID: spaceID,
	}

	if room.Name != nil {
		summary.Name = *room.Name
	}

	if room.Topic != nil {
		summary.Topic = *room.Topic
	}

	if room.AvatarURL != nil {
		summary.AvatarURL = *room.AvatarURL
	}

	// 获取子房间
	children, err := ss.getSpaceChildren(spaceID)
	if err == nil {
		summary.Children = children
	}

	// 获取成员
	var members []MatrixRoomMember

	db.GetDB().Where("room_id = ? AND membership = ?", spaceID, MembershipJoin).Find(&members)
	summary.JoinedMembers = len(members)

	return summary, nil
}

// JoinSpace 加入空间及其子房间.
func (ss *SpacesService) JoinSpace(userID, spaceID string, via []string) (*CreateRoomResponse, *MatrixError) {
	// 首先加入空间本身
	resp, err := ss.hs.Room.JoinRoom(userID, spaceID, nil)
	if err != nil {
		return nil, err
	}

	return &CreateRoomResponse{RoomID: resp}, nil
}

// ===== HTTP Handlers =====

// RegisterSpacesRoutes 注册空间 API 路由.
func RegisterSpacesRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	spaces := r.Group("/_matrix/client/v1")
	spaces.Use(MatrixAuthMiddleware())
	{
		// 空间层级结构
		spaces.GET("/rooms/:roomId/hierarchy", handleSpaceHierarchy)

		// 空间摘要（MSCs 会添加此端点）
		spaces.GET("/rooms/:roomId/spaces", handleSpaceSummary)
	}

	// 旧版 API (v3)
	v3 := r.Group("/_matrix/client/v3")
	v3.Use(MatrixAuthMiddleware())
	{
		v3.POST("/rooms/:roomId/space/children", handleSpaceChildren)
	}
}

// handleSpaceHierarchy 处理空间层级请求.
func handleSpaceHierarchy(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	spaceID := c.Param("roomId")

	// 解析查询参数
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	maxDepth, _ := strconv.Atoi(c.DefaultQuery("max_depth", "3"))
	from := c.Query("from")

	result, err := hs.Spaces.GetSpaceHierarchy(userID, spaceID, limit, maxDepth, from)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN", //nolint:goconst
			"error":   err.Error(),   //nolint:goconst
		})

		return
	}

	c.JSON(http.StatusOK, result)
}

// handleSpaceSummary 处理空间摘要请求.
func handleSpaceSummary(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	spaceID := c.Param("roomId")

	result, err := hs.Spaces.GetSpaceSummary(userID, spaceID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, result)
}

// handleSpaceChildren 处理空间子房间请求.
func handleSpaceChildren(c *gin.Context) {
	// 与 hierarchy 类似
	handleSpaceHierarchy(c)
}

// 辅助函数.
func getServerList(serverName string) []string {
	return []string{serverName}
}
