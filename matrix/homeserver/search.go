package homeserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/search"
	"gorm.io/gorm"
)

// SearchService 搜索服务
type SearchService struct {
	hs *HomeServer
}

// UserSearchService 用户搜索服务（SearchService 的别名）
type UserSearchService = SearchService

// NewSearchService 创建搜索服务
func NewSearchService(hs *HomeServer) *SearchService {
	return &SearchService{hs: hs}
}

// NewUserSearchService 创建用户搜索服务
func NewUserSearchService(hs *HomeServer) *UserSearchService {
	return NewSearchService(hs)
}

// MatrixSearchResult Matrix 搜索结果
type MatrixSearchResult struct {
	SearchCategories struct {
		RoomEvents    *RoomEventSearchResult     `json:"room_events,omitempty"`
		UserDirectory *UserDirectorySearchResult `json:"user_directory,omitempty"`
	} `json:"search_categories"`
	NextBatch string `json:"next_batch,omitempty"`
}

// RoomEventSearchResult 房间事件搜索结果
type RoomEventSearchResult struct {
	Results    []MatrixSearchEvent `json:"results"`
	Count      int                 `json:"count"`
	NextBatch  string              `json:"next_batch,omitempty"`
	Highlights map[string][]string `json:"highlights,omitempty"`
	State      []MatrixEvent       `json:"state,omitempty"`
}

// MatrixSearchEvent 搜索事件结果 (wraps event in "result" per Matrix spec)
type MatrixSearchEvent struct {
	Rank    float64               `json:"rank"`
	Result  MatrixSearchEventBody `json:"result"`
	Context *EventSearchContext   `json:"context,omitempty"`
}

// MatrixSearchEventBody 搜索事件内容
type MatrixSearchEventBody struct {
	EventID        string                 `json:"event_id"`
	RoomID         string                 `json:"room_id"`
	Sender         string                 `json:"sender"`
	Type           string                 `json:"type"`
	Content        map[string]interface{} `json:"content"`
	OriginServerTS int64                  `json:"origin_server_ts"`
}

// EventSearchContext 事件搜索上下文
type EventSearchContext struct {
	Start        string                  `json:"start,omitempty"`
	End          string                  `json:"end,omitempty"`
	EventsAfter  []MatrixSearchEventBody `json:"events_after"`
	EventsBefore []MatrixSearchEventBody `json:"events_before"`
	ProfileInfo  map[string]ProfileInfo  `json:"profile_info,omitempty"`
}

// ProfileInfo 用户信息
type ProfileInfo struct {
	DisplayName string `json:"displayname,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// UserDirectorySearchResult 用户目录搜索结果
type UserDirectorySearchResult struct {
	Results []UserDirectoryResult `json:"results"`
	Limited bool                  `json:"limited"`
}

// UserDirectoryResult 用户目录结果
type UserDirectoryResult struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// SearchRoomEvents 搜索房间事件
func (s *SearchService) SearchRoomEvents(query string, roomIDs []string, limit int, from string, ctx *EventContextReq) (*RoomEventSearchResult, error) {
	meiliClient := search.GetMeilisearchClient()

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	results := &RoomEventSearchResult{
		Results: []MatrixSearchEvent{},
	}

	if meiliClient.IsEnabled() {
		statusIDs, err := meiliClient.SearchStatuses(query, int64(limit), 0, "", nil)
		if err == nil {
			for _, statusIDStr := range statusIDs {
				statusID, _ := strconv.ParseUint(statusIDStr, 10, 64)
				events := s.convertStatusToMatrixEvents(statusID, query)
				results.Results = append(results.Results, events...)
			}
		}
		results.Count = len(results.Results)
	} else {
		results = s.searchRoomEventsFromDB(query, roomIDs, limit, from, ctx)
	}

	return results, nil
}

// searchRoomEventsFromDB 从数据库搜索房间事件
func (s *SearchService) searchRoomEventsFromDB(query string, roomIDs []string, limit int, from string, ctx *EventContextReq) *RoomEventSearchResult {
	result := &RoomEventSearchResult{
		Results: []MatrixSearchEvent{},
	}

	// Word-based search: each word in the query must appear in the content
	// This matches Matrix spec behavior (e.g. "Message 4" matches "Message number 4")
	words := strings.Fields(query)

	// Build base query with word-based matching
	buildQuery := func() *gorm.DB {
		q := db.GetDB().Model(&MatrixEvent{}).
			Where("event_type = ?", "m.room.message").
			Where("content != '{}' AND content != 'null'")
		for _, word := range words {
			q = q.Where("content LIKE ?", "%"+word+"%")
		}
		if len(roomIDs) > 0 {
			q = q.Where("room_id IN ?", roomIDs)
		}
		return q
	}

	// Count total matching events first (exclude redacted events with empty content)
	var totalCount int64
	buildQuery().Count(&totalCount)
	result.Count = int(totalCount)

	// Apply pagination offset
	offset := 0
	if from != "" {
		_, _ = fmt.Sscanf(from, "%d", &offset)
	}

	// Fetch matching events
	var events []MatrixEvent
	buildQuery().Offset(offset).Limit(limit).Order("origin_server_ts DESC").Find(&events)

	for _, event := range events {
		se := MatrixSearchEvent{
			Rank: 1.0,
			Result: MatrixSearchEventBody{
				EventID:        event.EventID,
				RoomID:         event.RoomID,
				Sender:         event.Sender,
				Type:           string(event.EventType),
				Content:        event.Content,
				OriginServerTS: event.OriginServerTS,
			},
		}

		// Add context if requested
		if ctx != nil {
			se.Context = s.getEventContext(event, ctx.BeforeLimit, ctx.AfterLimit)
		}

		result.Results = append(result.Results, se)
	}

	// Set next_batch if there might be more results
	// Use len(events) == limit as heuristic: if we got a full page, there might be more
	nextOffset := offset + len(events)
	if len(events) >= limit {
		result.NextBatch = fmt.Sprintf("%d", nextOffset)
	}

	return result
}

// getEventContext fetches surrounding events for search context
func (s *SearchService) getEventContext(event MatrixEvent, beforeLimit, afterLimit int) *EventSearchContext {
	ctx := &EventSearchContext{
		EventsBefore: make([]MatrixSearchEventBody, 0),
		EventsAfter:  make([]MatrixSearchEventBody, 0),
	}

	if beforeLimit > 0 {
		var before []MatrixEvent
		db.GetDB().Where("room_id = ? AND origin_server_ts < ?",
			event.RoomID, event.OriginServerTS).
			Order("origin_server_ts DESC").Limit(beforeLimit).Find(&before)
		ctx.EventsBefore = make([]MatrixSearchEventBody, len(before))
		for i, e := range before {
			ctx.EventsBefore[i] = MatrixSearchEventBody{
				EventID: e.EventID, RoomID: e.RoomID, Sender: e.Sender,
				Type: string(e.EventType), Content: e.Content, OriginServerTS: e.OriginServerTS,
			}
		}
	}

	if afterLimit > 0 {
		var after []MatrixEvent
		db.GetDB().Where("room_id = ? AND origin_server_ts > ?",
			event.RoomID, event.OriginServerTS).
			Order("origin_server_ts ASC").Limit(afterLimit).Find(&after)
		ctx.EventsAfter = make([]MatrixSearchEventBody, len(after))
		for i, e := range after {
			ctx.EventsAfter[i] = MatrixSearchEventBody{
				EventID: e.EventID, RoomID: e.RoomID, Sender: e.Sender,
				Type: string(e.EventType), Content: e.Content, OriginServerTS: e.OriginServerTS,
			}
		}
	}

	return ctx
}

// convertStatusToMatrixEvents 将社区 Status 转换为 Matrix 事件搜索结果
func (s *SearchService) convertStatusToMatrixEvents(statusID uint64, query string) []MatrixSearchEvent {
	results := []MatrixSearchEvent{}

	var status models.Status
	if err := db.GetDB().Preload("Account").First(&status, statusID).Error; err != nil {
		return results
	}

	// 查找对应的 Matrix 房间（如果存在）
	var roomMapping MatrixRoomMapping
	err := db.GetDB().Where("conversation_id = ? OR group_id = ?", status.ConversationId, 0).First(&roomMapping).Error
	if err != nil {
		return results
	}

	// 构建 Matrix 用户 ID
	matrixUserID := fmt.Sprintf("@%s:%s", status.Account.Username, s.hs.Config.ServerName)

	// 构建事件内容
	content := map[string]interface{}{
		"msgtype": "m.text",
		"body":    status.Content,
	}

	results = append(results, MatrixSearchEvent{
		Rank: 1.0,
		Result: MatrixSearchEventBody{
			EventID:        fmt.Sprintf("$%d:community", status.Id),
			RoomID:         roomMapping.RoomID,
			Sender:         matrixUserID,
			Type:           "m.room.message",
			Content:        content,
			OriginServerTS: status.CreatedAt.UnixMilli(),
		},
	})

	return results
}

// SearchUserDirectory 搜索用户目录
func (s *SearchService) SearchUserDirectory(query string, limit int) (*UserDirectorySearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	results := &UserDirectorySearchResult{
		Results: []UserDirectoryResult{},
		Limited: false,
	}

	// Build search terms: strip @ prefix and :domain suffix for localpart matching
	searchQuery := query
	searchQuery = strings.TrimPrefix(searchQuery, "@")
	if idx := strings.Index(searchQuery, ":"); idx >= 0 {
		searchQuery = searchQuery[:idx]
	}

	// Matrix spec: user directory should only return users who are "visible" --
	// users in at least one public room, or users who share a room with the searcher.
	var publicRoomIDs []string
	db.GetDB().Model(&MatrixRoom{}).Where("join_rule = ?", "public").Pluck("room_id", &publicRoomIDs)

	if len(publicRoomIDs) == 0 {
		return results, nil
	}

	// Get distinct visible user IDs and their localparts
	var publicMembers []MatrixRoomMember
	db.GetDB().Where("room_id IN ? AND membership = ?", publicRoomIDs, MembershipJoin).Find(&publicMembers)

	localparts := make([]string, 0)
	seen := make(map[string]bool)
	for _, m := range publicMembers {
		if seen[m.UserID] {
			continue
		}
		seen[m.UserID] = true
		if lp, _, ok := ParseMatrixUserID(m.UserID); ok {
			localparts = append(localparts, lp)
		}
	}

	if len(localparts) == 0 {
		return results, nil
	}

	// Find accounts for visible users that match the search query
	var accounts []models.Account
	db.GetDB().Where("username IN ?", localparts).
		Where("username LIKE ? OR display_name LIKE ?",
			"%"+searchQuery+"%", "%"+searchQuery+"%").
		Limit(limit).
		Find(&accounts)

	// 转换为 Matrix 用户格式
	for _, account := range accounts {
		matrixUserID := fmt.Sprintf("@%s:%s", account.Username, s.hs.Config.ServerName)

		// 构建头像 URL
		avatarURL := ""
		if account.Avatar != "" {
			avatarURL = s.convertAvatarToMXC(account.Avatar)
		}

		results.Results = append(results.Results, UserDirectoryResult{
			UserID:      matrixUserID,
			DisplayName: account.DisplayName,
			AvatarURL:   avatarURL,
		})
	}

	return results, nil
}

// convertAvatarToMXC 将头像 URL 转换为 MXC 格式
func (s *SearchService) convertAvatarToMXC(avatarURL string) string {
	if strings.HasPrefix(avatarURL, "/uploads/") {
		parts := strings.Split(avatarURL, "/")
		if len(parts) >= 4 {
			mediaID := strings.Split(parts[len(parts)-1], ".")[0]
			return fmt.Sprintf("mxc://%s/%s", s.hs.Config.ServerName, mediaID)
		}
	}
	return ""
}

// MatrixRoomMapping 房间映射（复用现有 bridge 逻辑）
type MatrixRoomMapping struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	RoomID         string `gorm:"uniqueIndex;size:255"`
	ConversationID *uint64
	GroupID        *uint64
	CreatedAt      time.Time
}

func (MatrixRoomMapping) TableName() string {
	return "matrix_room_mappings"
}

// ===== HTTP Handlers =====

// handleSearch 处理搜索请求
func handleSearch(c *gin.Context) {
	userID := getUserID(c)

	var req SearchRequestInternal
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
	}

	hs := GetHomeServer()
	searchService := NewSearchService(hs)

	result := &MatrixSearchResult{
		SearchCategories: struct {
			RoomEvents    *RoomEventSearchResult     `json:"room_events,omitempty"`
			UserDirectory *UserDirectorySearchResult `json:"user_directory,omitempty"`
		}{},
	}

	// 搜索房间事件
	if req.SearchCategories.RoomEvents != nil {
		roomSearch := req.SearchCategories.RoomEvents
		limit := 10

		// Extract rooms and limit from filter object
		var filterRooms []string
		if roomSearch.Filter != nil {
			if rooms, ok := roomSearch.Filter["rooms"].([]interface{}); ok {
				for _, r := range rooms {
					if rs, ok := r.(string); ok {
						filterRooms = append(filterRooms, rs)
					}
				}
			}
			if l, ok := roomSearch.Filter["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
		}

		// Use filter.rooms if top-level rooms not set
		candidateRooms := roomSearch.Rooms
		if len(candidateRooms) == 0 {
			candidateRooms = filterRooms
		}

		var roomIDs []string
		if len(candidateRooms) > 0 {
			// 只搜索用户已加入的房间
			var joinedRooms []MatrixRoomMember
			db.GetDB().Where("user_id = ? AND room_id IN ? AND membership = ?",
				userID, candidateRooms, "join").Find(&joinedRooms)
			for _, m := range joinedRooms {
				roomIDs = append(roomIDs, m.RoomID)
			}
		}

		// Also check query param next_batch
		nextBatch := req.NextBatch
		if nextBatch == "" {
			nextBatch = c.Query("next_batch")
		}

		results, err := searchService.SearchRoomEvents(roomSearch.SearchTerm, roomIDs, limit, nextBatch, roomSearch.EventContext)
		if err == nil {
			result.SearchCategories.RoomEvents = results
		}
	}

	// 搜索用户目录
	if req.SearchCategories.UserDirectory != nil {
		userSearch := req.SearchCategories.UserDirectory
		limit := userSearch.Limit
		if limit <= 0 {
			limit = 10
		}

		results, err := searchService.SearchUserDirectory(userSearch.SearchTerm, limit)
		if err == nil {
			result.SearchCategories.UserDirectory = results
		}
	}

	c.JSON(http.StatusOK, result)
}

// SearchRequestInternal 内部搜索请求
type SearchRequestInternal struct {
	SearchCategories struct {
		RoomEvents    *RoomEventsSearchReq    `json:"room_events,omitempty"`
		UserDirectory *UserDirectorySearchReq `json:"user_directory,omitempty"`
	} `json:"search_categories"`
	NextBatch string `json:"next_batch,omitempty"`
}

// RoomEventsSearchReq 房间事件搜索请求
type RoomEventsSearchReq struct {
	SearchTerm   string                 `json:"search_term"`
	Keys         []string               `json:"keys,omitempty"`
	Filter       map[string]interface{} `json:"filter,omitempty"`
	Rooms        []string               `json:"rooms,omitempty"`
	OrderBy      string                 `json:"order_by,omitempty"`
	EventContext *EventContextReq       `json:"event_context,omitempty"`
}

// EventContextReq 搜索结果上下文请求
type EventContextReq struct {
	BeforeLimit int `json:"before_limit"`
	AfterLimit  int `json:"after_limit"`
}

// UserDirectorySearchReq 用户目录搜索请求
type UserDirectorySearchReq struct {
	SearchTerm string `json:"search_term"`
	Limit      int    `json:"limit,omitempty"`
}

// handleUserDirectory 处理用户目录搜索
func handleUserDirectory(c *gin.Context) {
	var req struct {
		SearchTerm string `json:"search_term"`
		Limit      int    `json:"limit"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
	}

	hs := GetHomeServer()
	searchService := NewSearchService(hs)

	results, err := searchService.SearchUserDirectory(req.SearchTerm, req.Limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Search failed",
		})
		return
	}

	c.JSON(http.StatusOK, results)
}
