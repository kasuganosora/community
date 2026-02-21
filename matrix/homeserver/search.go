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
		RoomEvents *RoomEventSearchResult `json:"room_events,omitempty"`
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

// MatrixSearchEvent 搜索事件结果
type MatrixSearchEvent struct {
	EventID        string                 `json:"event_id"`
	RoomID         string                 `json:"room_id"`
	Sender         string                 `json:"sender"`
	Type           string                 `json:"type"`
	Content        map[string]interface{} `json:"content"`
	OriginServerTS int64                  `json:"origin_server_ts"`
	Rank           float64                `json:"rank"`
	Context        *EventSearchContext    `json:"context,omitempty"`
}

// EventSearchContext 事件搜索上下文
type EventSearchContext struct {
	Start       string         `json:"start"`
	End         string         `json:"end"`
	EventsAfter []MatrixEvent  `json:"events_after"`
	EventsBefore []MatrixEvent `json:"events_before"`
	ProfileInfo map[string]ProfileInfo `json:"profile_info,omitempty"`
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
func (s *SearchService) SearchRoomEvents(query string, roomIDs []string, limit int, from string) (*RoomEventSearchResult, error) {
	// 复用 Meilisearch 搜索
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
	
	// 如果 Meilisearch 启用，使用它进行搜索
	if meiliClient.IsEnabled() {
		// 搜索 Statuses（对应 Matrix 消息）
		statusIDs, err := meiliClient.SearchStatuses(query, int64(limit), 0, "", nil)
		if err == nil {
			for _, statusIDStr := range statusIDs {
				// 转换为 Matrix 事件
				statusID, _ := strconv.ParseUint(statusIDStr, 10, 64)
				events := s.convertStatusToMatrixEvents(statusID, query)
				results.Results = append(results.Results, events...)
			}
		}
	} else {
		// 回退到数据库搜索
		results.Results = s.searchRoomEventsFromDB(query, roomIDs, limit)
	}
	
	results.Count = len(results.Results)
	return results, nil
}

// searchRoomEventsFromDB 从数据库搜索房间事件
func (s *SearchService) searchRoomEventsFromDB(query string, roomIDs []string, limit int) []MatrixSearchEvent {
	results := []MatrixSearchEvent{}
	
	// 搜索 Matrix 事件
	dbQuery := db.GetDB().Model(&MatrixEvent{}).
		Where("event_type = ?", "m.room.message").
		Where("content LIKE ?", "%"+query+"%")
	
	if len(roomIDs) > 0 {
		dbQuery = dbQuery.Where("room_id IN ?", roomIDs)
	}
	
	var events []MatrixEvent
	dbQuery.Limit(limit).Order("origin_server_ts DESC").Find(&events)
	
	for _, event := range events {
		// Content 已经是 map 类型
		results = append(results, MatrixSearchEvent{
			EventID:        event.EventID,
			RoomID:         event.RoomID,
			Sender:         event.Sender,
			Type:           string(event.EventType),
			Content:        event.Content,
			OriginServerTS: event.OriginServerTS,
			Rank:           1.0, // 简单排名
		})
	}
	
	return results
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
		EventID:        fmt.Sprintf("$%d:community", status.Id),
		RoomID:         roomMapping.RoomID,
		Sender:         matrixUserID,
		Type:           "m.room.message",
		Content:        content,
		OriginServerTS: status.CreatedAt.UnixMilli(),
		Rank:           1.0,
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
	
	// 复用社区的用户搜索逻辑
	meiliClient := search.GetMeilisearchClient()
	var accounts []models.Account
	
	if meiliClient.IsEnabled() {
		// 使用 Meilisearch
		accountIDs, err := meiliClient.SearchAccounts(query, int64(limit), 0, nil)
		if err == nil {
			db.GetDB().Where("id IN ?", accountIDs).Find(&accounts)
		}
	} else {
		// 回退到数据库搜索
		db.GetDB().Where("username LIKE ? OR display_name LIKE ?", 
			"%"+query+"%", "%"+query+"%").
			Where("domain IS NULL"). // 只搜索本地用户
			Limit(limit).
			Find(&accounts)
	}
	
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
			RoomEvents    *RoomEventSearchResult    `json:"room_events,omitempty"`
			UserDirectory *UserDirectorySearchResult `json:"user_directory,omitempty"`
		}{},
	}
	
	// 搜索房间事件
	if req.SearchCategories.RoomEvents != nil {
		roomSearch := req.SearchCategories.RoomEvents
		limit := 10
		if roomSearch.Filter != nil {
			// 解析 filter 中的 limit
		}
		
		var roomIDs []string
		if len(roomSearch.Rooms) > 0 {
			// 只搜索用户已加入的房间
			var joinedRooms []MatrixRoomMember
			db.GetDB().Where("user_id = ? AND room_id IN ? AND membership = ?", 
				userID, roomSearch.Rooms, "join").Find(&joinedRooms)
			for _, m := range joinedRooms {
				roomIDs = append(roomIDs, m.RoomID)
			}
		}
		
		results, err := searchService.SearchRoomEvents(roomSearch.SearchTerm, roomIDs, limit, req.NextBatch)
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
	SearchTerm string                 `json:"search_term"`
	Filter     map[string]interface{} `json:"filter,omitempty"`
	Rooms      []string               `json:"rooms,omitempty"`
	OrderBy    string                 `json:"order_by,omitempty"`
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
