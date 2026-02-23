package homeserver

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// ===== Application Services (Appservices) 实现 =====
// 应用服务允许桥接其他聊天平台（Telegram, Discord, WhatsApp 等）

// AppService 应用服务配置
type AppService struct {
	ID              string            `gorm:"primaryKey;type:varchar(64)" json:"id"`
	URL             string            `gorm:"type:varchar(512);not null" json:"url"`           // 事件推送 URL
	Token           string            `gorm:"type:varchar(512);not null" json:"token"`         // 访问令牌
	HS_TOKEN        string            `gorm:"type:varchar(512);not null" json:"hs_token"`      // 验证令牌
	SenderLocalpart string            `gorm:"type:varchar(128);not null" json:"sender_localpart"` // 发送者本地部分 (@bot:server)
	Protocols       []string          `gorm:"type:json;serializer:json" json:"protocols,omitempty"` // 支持的协议
	Namespaces      AppServiceNamespaces `gorm:"type:json;serializer:json" json:"namespaces"`    // 命名空间配置
	RateLimited     bool              `gorm:"default:true" json:"rate_limited"`
	Enabled         bool              `gorm:"default:true" json:"enabled"`
	CreatedAt       time.Time         `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time         `gorm:"autoUpdateTime" json:"updated_at"`
}

// AppServiceNamespaces 命名空间配置
type AppServiceNamespaces struct {
	Users  []NamespaceConfig `json:"users,omitempty"`
	Rooms  []NamespaceConfig `json:"rooms,omitempty"`
	Aliases []NamespaceConfig `json:"aliases,omitempty"`
}

// NamespaceConfig 命名空间配置项
type NamespaceConfig struct {
	Exclusive bool   `json:"exclusive"`
	Regex     string `json:"regex"`
	GroupID   string `json:"group_id,omitempty"`
}

func (AppService) TableName() string {
	return "matrix_appservices"
}

// AppServiceService 应用服务管理
type AppServiceService struct {
	hs     *HomeServer
	client *http.Client
}

// NewAppServiceService 创建应用服务管理器
func NewAppServiceService(hs *HomeServer) *AppServiceService {
	return &AppServiceService{
		hs:     hs,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// RegisterAppService 注册应用服务
func (ass *AppServiceService) RegisterAppService(as *AppService) error {
	// 验证 ID 格式
	if as.ID == "" || !isValidAppServiceID(as.ID) {
		return fmt.Errorf("invalid appservice ID")
	}

	// 验证 URL
	if as.URL == "" {
		return fmt.Errorf("appservice URL is required")
	}

	// 生成令牌（如果没有提供）
	if as.Token == "" {
		as.Token = generateSecureToken()
	}
	if as.HS_TOKEN == "" {
		as.HS_TOKEN = generateSecureToken()
	}

	// 检查 ID 是否已存在
	var existing AppService
	if err := db.GetDB().Where("id = ?", as.ID).First(&existing).Error; err == nil {
		return fmt.Errorf("appservice with ID %s already exists", as.ID)
	}

	// 创建用户（如果指定了 sender_localpart）
	if as.SenderLocalpart != "" {
		userID := fmt.Sprintf("@%s:%s", as.SenderLocalpart, ass.hs.Config.ServerName)
		// 确保应用服务用户存在
		ass.ensureAppServiceUser(userID, as.ID)
	}

	return db.GetDB().Create(as).Error
}

// GetAppService 获取应用服务
func (ass *AppServiceService) GetAppService(id string) (*AppService, error) {
	var as AppService
	if err := db.GetDB().Where("id = ? AND enabled = ?", id, true).First(&as).Error; err != nil {
		return nil, err
	}
	return &as, nil
}

// GetAppServiceByToken 通过令牌获取应用服务
func (ass *AppServiceService) GetAppServiceByToken(token string) (*AppService, error) {
	var as AppService
	if err := db.GetDB().Where("token = ? AND enabled = ?", token, true).First(&as).Error; err != nil {
		return nil, err
	}
	return &as, nil
}

// ListAppServices 列出所有应用服务
func (ass *AppServiceService) ListAppServices() ([]AppService, error) {
	var services []AppService
	if err := db.GetDB().Where("enabled = ?", true).Find(&services).Error; err != nil {
		return nil, err
	}
	return services, nil
}

// UpdateAppService 更新应用服务
func (ass *AppServiceService) UpdateAppService(as *AppService) error {
	as.UpdatedAt = time.Now()
	return db.GetDB().Save(as).Error
}

// DeleteAppService 删除应用服务
func (ass *AppServiceService) DeleteAppService(id string) error {
	return db.GetDB().Model(&AppService{}).Where("id = ?", id).Update("enabled", false).Error
}

// IsAppServiceUser 检查用户是否属于应用服务
func (ass *AppServiceService) IsAppServiceUser(userID string) (*AppService, bool) {
	services, err := ass.ListAppServices()
	if err != nil {
		return nil, false
	}

	for _, as := range services {
		// 检查用户命名空间
		for _, ns := range as.Namespaces.Users {
			if matched, _ := regexp.MatchString(ns.Regex, userID); matched {
				return &as, true
			}
		}
	}

	return nil, false
}

// IsExclusiveAlias 检查别名是否被应用服务独占
func (ass *AppServiceService) IsExclusiveAlias(alias string) (*AppService, bool) {
	services, err := ass.ListAppServices()
	if err != nil {
		return nil, false
	}

	for _, as := range services {
		for _, ns := range as.Namespaces.Aliases {
			if ns.Exclusive {
				if matched, _ := regexp.MatchString(ns.Regex, alias); matched {
					return &as, true
				}
			}
		}
	}

	return nil, false
}

// CanAppServiceClaimUser 检查应用服务是否可以声称某个用户
func (ass *AppServiceService) CanAppServiceClaimUser(as *AppService, userID string) bool {
	for _, ns := range as.Namespaces.Users {
		if matched, _ := regexp.MatchString(ns.Regex, userID); matched {
			return true
		}
	}
	return false
}

// CanAppServiceClaimRoom 检查应用服务是否可以声称某个房间
func (ass *AppServiceService) CanAppServiceClaimRoom(as *AppService, roomID string) bool {
	for _, ns := range as.Namespaces.Rooms {
		if matched, _ := regexp.MatchString(ns.Regex, roomID); matched {
			return true
		}
	}
	return false
}

// CanAppServiceClaimAlias 检查应用服务是否可以声称某个别名
func (ass *AppServiceService) CanAppServiceClaimAlias(as *AppService, alias string) bool {
	for _, ns := range as.Namespaces.Aliases {
		if matched, _ := regexp.MatchString(ns.Regex, alias); matched {
			return true
		}
	}
	return false
}

// PushEvent 推送事件到应用服务
func (ass *AppServiceService) PushEvent(as *AppService, event *MatrixEvent) {
	if as.URL == "" {
		return
	}

	payload := map[string]interface{}{
		"events": []map[string]interface{}{eventToMap(event)},
	}

	data, _ := json.Marshal(payload)

	go func() {
		req, err := http.NewRequest("POST", as.URL, bytes.NewReader(data))
		if err != nil {
			log.Error("Failed to create appservice request: %v", err)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", as.HS_TOKEN))

		resp, err := ass.client.Do(req)
		if err != nil {
			log.Error("Failed to push event to appservice %s: %v", as.ID, err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Error("Appservice %s returned status %d", as.ID, resp.StatusCode)
		}
	}()
}

// PushEvents 批量推送事件
func (ass *AppServiceService) PushEvents(events []*MatrixEvent) {
	services, err := ass.ListAppServices()
	if err != nil {
		return
	}

	for _, as := range services {
		// 过滤属于该应用服务的事件
		var asEvents []map[string]interface{}
		for _, event := range events {
			if ass.shouldReceiveEvent(&as, event) {
				asEvents = append(asEvents, eventToMap(event))
			}
		}

		if len(asEvents) > 0 {
			payload := map[string]interface{}{
				"events": asEvents,
			}
			data, _ := json.Marshal(payload)

			go func(service AppService) {
				req, _ := http.NewRequest("POST", service.URL, bytes.NewReader(data))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", service.HS_TOKEN))

				resp, err := ass.client.Do(req)
				if err != nil {
					log.Error("Failed to push events to appservice %s: %v", service.ID, err)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					log.Error("Appservice %s returned status %d", service.ID, resp.StatusCode)
				}
			}(as)
		}
	}
}

// shouldReceiveEvent 检查应用服务是否应该接收此事件
func (ass *AppServiceService) shouldReceiveEvent(as *AppService, event *MatrixEvent) bool {
	// 检查发送者是否属于该应用服务
	if matched, _ := ass.IsAppServiceUser(event.Sender); matched != nil && matched.ID == as.ID {
		return true
	}

	// 检查房间命名空间
	for _, ns := range as.Namespaces.Rooms {
		if matched, _ := regexp.MatchString(ns.Regex, event.RoomID); matched {
			return true
		}
	}

	// 检查用户命名空间（目标用户）
	if event.StateKey != nil {
		for _, ns := range as.Namespaces.Users {
			if matched, _ := regexp.MatchString(ns.Regex, *event.StateKey); matched {
				return true
			}
		}
	}

	return false
}

// ensureAppServiceUser 确保应用服务用户存在
func (ass *AppServiceService) ensureAppServiceUser(userID, appServiceID string) error {
	// 检查用户是否已存在
	var existing MatrixUserSession
	if err := db.GetDB().Where("user_id = ?", userID).First(&existing).Error; err == nil {
		return nil
	}

	// 创建应用服务用户
	session := &MatrixUserSession{
		UserID:     userID,
		DeviceID:   "APPSERVICE_" + appServiceID,
		AccessToken: "AS_" + generateSecureToken(),
	}

	return db.GetDB().Create(session).Error
}

// QueryUser 查询用户（应用服务 API）
func (ass *AppServiceService) QueryUser(userID string) (*AppServiceUserInfo, error) {
	// 检查是否是应用服务用户
	_, isAS := ass.IsAppServiceUser(userID)
	if !isAS {
		return nil, fmt.Errorf("user not found")
	}

	// 返回用户信息
	return &AppServiceUserInfo{
		UserID:      userID,
		DisplayName: extractDisplayName(userID),
		AvatarURL:   "",
	}, nil
}

// QueryRoom 查询房间别名（应用服务 API）
func (ass *AppServiceService) QueryRoom(alias string) (*AppServiceRoomInfo, error) {
	// 查找房间
	var roomState MatrixRoomState
	if err := db.GetDB().Where("event_type = ? AND content->>'alias' = ?", EventTypeRoomCanonicalAlias, alias).First(&roomState).Error; err != nil {
		// 检查应用服务是否可以创建此别名
		services, _ := ass.ListAppServices()
		for _, as := range services {
			if ass.CanAppServiceClaimAlias(&as, alias) {
				// 应用服务可以创建此别名
				return nil, fmt.Errorf("room not found, but appservice can create it")
			}
		}
		return nil, fmt.Errorf("room not found")
	}

	return &AppServiceRoomInfo{
		RoomID: roomState.RoomID,
		Servers: []string{ass.hs.Config.ServerName},
	}, nil
}

// AppServiceUserInfo 应用服务用户信息
type AppServiceUserInfo struct {
	UserID      string   `json:"user_id"`
	DisplayName string   `json:"displayname,omitempty"`
	AvatarURL   string   `json:"avatar_url,omitempty"`
}

// AppServiceRoomInfo 应用服务房间信息
type AppServiceRoomInfo struct {
	RoomID  string   `json:"room_id"`
	Servers []string `json:"servers"`
}

// ===== HTTP Handlers =====

// RegisterAppServiceRoutes 注册应用服务 API 路由
func RegisterAppServiceRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 应用服务查询 API（需要 appservice token）
	asGroup := r.Group("/_matrix/app/v1")
	asGroup.Use(AppServiceAuthMiddleware())
	{
		// 用户查询
		asGroup.GET("/users/:userId", handleAppServiceQueryUser)
		// 房间查询
		asGroup.GET("/rooms/:roomAlias", handleAppServiceQueryRoom)
		// 第三方协议查询
		asGroup.GET("/thirdparty/protocol/:protocol", handleAppServiceThirdPartyProtocol)
		asGroup.GET("/thirdparty/location/:protocol", handleAppServiceThirdPartyLocation)
		asGroup.GET("/thirdparty/user/:protocol", handleAppServiceThirdPartyUser)
	}

	// 注册应用服务（管理员 API）
	admin := r.Group("/_matrix/client/v3/admin/appservices")
	admin.Use(MatrixAuthMiddleware())
	{
		admin.POST("/register", handleRegisterAppService)
		admin.GET("/list", handleListAppServices)
		admin.DELETE("/:id", handleDeleteAppService)
	}
}

// AppServiceAuthMiddleware 应用服务认证中间件
func AppServiceAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		hs := GetHomeServer()
		if hs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"errcode": "M_UNKNOWN",
				"error":   "Homeserver not available",
			})
			c.Abort()
			return
		}

		// 从 Authorization 头获取 token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"errcode": "M_MISSING_TOKEN",
				"error":   "Missing access token",
			})
			c.Abort()
			return
		}

		// 提取 Bearer token
		if len(authHeader) < 8 || !strings.HasPrefix(authHeader, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"errcode": "M_UNKNOWN_TOKEN",
				"error":   "Invalid token format",
			})
			c.Abort()
			return
		}

		token := authHeader[7:]

		// 验证应用服务
		as, err := hs.AppService.GetAppServiceByToken(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"errcode": "M_UNKNOWN_TOKEN",
				"error":   "Invalid appservice token",
			})
			c.Abort()
			return
		}

		c.Set("appservice", as)
		c.Set("appservice_id", as.ID)
		c.Next()
	}
}

// handleAppServiceQueryUser 处理应用服务用户查询
func handleAppServiceQueryUser(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")
	as := c.MustGet("appservice").(*AppService)

	// 检查应用服务是否可以声称此用户
	if !hs.AppService.CanAppServiceClaimUser(as, userID) {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
		})
		return
	}

	// 查询用户信息
	userInfo, err := hs.AppService.QueryUser(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, userInfo)
}

// handleAppServiceQueryRoom 处理应用服务房间查询
func handleAppServiceQueryRoom(c *gin.Context) {
	hs := GetHomeServer()
	roomAlias := c.Param("roomAlias")
	as := c.MustGet("appservice").(*AppService)

	// 检查应用服务是否可以声称此别名
	if !hs.AppService.CanAppServiceClaimAlias(as, roomAlias) {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}

	// 查询房间信息
	roomInfo, err := hs.AppService.QueryRoom(roomAlias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, roomInfo)
}

// handleAppServiceThirdPartyProtocol 处理第三方协议查询
func handleAppServiceThirdPartyProtocol(c *gin.Context) {
	protocol := c.Param("protocol")
	_ = protocol

	// 返回协议信息
	c.JSON(http.StatusOK, gin.H{
		"user_fields":   []string{},
		"location_fields": []string{},
		"icon":          "",
		"field_types":   gin.H{},
	})
}

// handleAppServiceThirdPartyLocation 处理第三方位置查询
func handleAppServiceThirdPartyLocation(c *gin.Context) {
	// 返回空列表
	c.JSON(http.StatusOK, []interface{}{})
}

// handleAppServiceThirdPartyUser 处理第三方用户查询
func handleAppServiceThirdPartyUser(c *gin.Context) {
	// 返回空列表
	c.JSON(http.StatusOK, []interface{}{})
}

// handleRegisterAppService 注册应用服务（管理员）
func handleRegisterAppService(c *gin.Context) {
	hs := GetHomeServer()

	var as AppService
	if err := c.ShouldBindJSON(&as); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.AppService.RegisterAppService(&as); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   err.Error(),
		})
		return
	}

	// 返回注册信息（包含生成的令牌）
	c.JSON(http.StatusOK, gin.H{
		"id":       as.ID,
		"token":    as.Token,
		"hs_token": as.HS_TOKEN,
	})
}

// handleListAppServices 列出应用服务（管理员）
func handleListAppServices(c *gin.Context) {
	hs := GetHomeServer()

	services, err := hs.AppService.ListAppServices()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"appservices": services,
	})
}

// handleDeleteAppService 删除应用服务（管理员）
func handleDeleteAppService(c *gin.Context) {
	hs := GetHomeServer()
	id := c.Param("id")

	if err := hs.AppService.DeleteAppService(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// ===== 辅助函数 =====

func isValidAppServiceID(id string) bool {
	// ID 只能包含字母、数字、下划线和连字符
	matched, _ := regexp.MatchString("^[a-zA-Z0-9_-]+$", id)
	return matched
}

func generateSecureToken() string {
	// 生成安全的随机令牌
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 如果加密随机数生成失败，使用更安全的回退方案
		return hex.EncodeToString([]byte(fmt.Sprintf("%d%d", time.Now().UnixNano(), time.Now().UnixNano())))
	}
	return hex.EncodeToString(b)
}

func eventToMap(event *MatrixEvent) map[string]interface{} {
	return map[string]interface{}{
		"event_id":         event.EventID,
		"type":             string(event.EventType),
		"room_id":          event.RoomID,
		"sender":           event.Sender,
		"origin_server_ts": event.OriginServerTS,
		"content":          event.Content,
		"state_key":        event.StateKey,
	}
}

func extractDisplayName(userID string) string {
	parts := strings.Split(userID, ":")
	if len(parts) > 0 {
		name := strings.TrimPrefix(parts[0], "@")
		return name
	}
	return userID
}
