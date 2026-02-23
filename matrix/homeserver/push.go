package homeserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// ===== Push Gateway / Notifications 实现 =====

// Pusher 推送器配置
type Pusher struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID     string `gorm:"index;type:varchar(255);not null" json:"-"`
	Pushkey    string `gorm:"type:varchar(512);not null" json:"pushkey"`      // 设备推送令牌
	Kind       string `gorm:"type:varchar(32);not null" json:"kind"`         // http, email
	AppID      string `gorm:"type:varchar(128);not null" json:"app_id"`      // 应用 ID
	AppDisplayName string `gorm:"type:varchar(128)" json:"app_display_name"`
	DeviceDisplayName string `gorm:"type:varchar(128)" json:"device_display_name"`
	ProfileTag string `gorm:"type:varchar(32)" json:"profile_tag,omitempty"`
	Lang       string `gorm:"type:varchar(16)" json:"lang"`
	Data       map[string]interface{} `gorm:"type:text;serializer:json" json:"data"`
	DeviceID   string `gorm:"type:varchar(255);index" json:"-"`
	Enabled    bool   `gorm:"default:true" json:"-"`
	CreatedAt  time.Time `json:"-"`
	UpdatedAt  time.Time `json:"-"`
}

func (Pusher) TableName() string {
	return "matrix_pushers"
}

// PusherData 推送器数据
type PusherData struct {
	URL      string `json:"url,omitempty"`
	Format   string `json:"format,omitempty"` // event_id_only, full
	Brand    string `json:"brand,omitempty"`
}

// PushRule 推送规则
type PushRule struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID      string    `gorm:"index;type:varchar(255);not null" json:"-"`
	Scope       string    `gorm:"index;type:varchar(16);not null" json:"-"` // global, device
	Kind        string    `gorm:"index;type:varchar(32);not null" json:"-"` // override, content, room, sender, underride
	RuleID      string    `gorm:"type:varchar(128);not null" json:"rule_id"`
	Default     bool      `gorm:"default:false" json:"default"`
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	Conditions  string    `gorm:"type:text" json:"-"` // JSON
	Actions     string    `gorm:"type:text" json:"-"` // JSON
	Pattern     string    `gorm:"type:varchar(255)" json:"pattern,omitempty"`
	PriorityClass int64   `json:"-"`
	CreatedAt   time.Time `json:"-"`
	UpdatedAt   time.Time `json:"-"`
}

func (PushRule) TableName() string {
	return "matrix_push_rules"
}

// PushCondition 推送条件
type PushCondition struct {
	Kind    string `json:"kind"` // event_match, contains_display_name, room_member_count
	Key     string `json:"key,omitempty"`    // for event_match
	Pattern string `json:"pattern,omitempty"` // for event_match
	Is      string `json:"is,omitempty"`     // for room_member_count
}

// PushAction 推送动作
type PushAction struct {
	Action string                 `json:"action"` // notify, dont_notify, coalesce, set_tweak
	Tweak  map[string]interface{} `json:"tweak,omitempty"`
}

// PushNotification 推送通知
type PushNotification struct {
	Notification PushNotificationData `json:"notification"`
}

// PushNotificationData 通知数据
type PushNotificationData struct {
	EventID           string                 `json:"event_id"`
	RoomID            string                 `json:"room_id"`
	Type              string                 `json:"type"`
	Sender            string                 `json:"sender"`
	SenderDisplayName string                 `json:"sender_display_name,omitempty"`
	RoomName          string                 `json:"room_name,omitempty"`
	RoomAlias         string                 `json:"room_alias,omitempty"`
	Content           map[string]interface{} `json:"content,omitempty"`
	Unread            int                    `json:"unread,omitempty"`
	Counts            *PushCounts            `json:"counts,omitempty"`
	Devices           []PushDevice           `json:"devices,omitempty"`
}

// PushCounts 未读计数
type PushCounts struct {
	Unread int `json:"unread,omitempty"`
	MissedCalls int `json:"missed_calls,omitempty"`
}

// PushDevice 目标设备
type PushDevice struct {
	AppID   string `json:"app_id"`
	Pushkey string `json:"pushkey"`
	PushkeyTs int64 `json:"pushkey_ts,omitempty"`
	Data    PusherData `json:"data,omitempty"`
	Tweaks  []map[string]interface{} `json:"tweaks,omitempty"`
}

// PushRulesResponse 推送规则响应
type PushRulesResponse struct {
	Global PushRuleset `json:"global"`
}

// PushRuleset 规则集
type PushRuleset struct {
	Override  []PushRuleResponse `json:"override"`
	Content   []PushRuleResponse `json:"content"`
	Room      []PushRuleResponse `json:"room"`
	Sender    []PushRuleResponse `json:"sender"`
	Underride []PushRuleResponse `json:"underride"`
}

// PushRuleResponse 推送规则响应格式
type PushRuleResponse struct {
	RuleID      string            `json:"rule_id"`
	Default     bool              `json:"default"`
	Enabled     bool              `json:"enabled"`
	Conditions  []PushCondition   `json:"conditions,omitempty"`
	Actions     []interface{}     `json:"actions"`
	Pattern     string            `json:"pattern,omitempty"`
}

// PushService 推送服务
type PushService struct {
	hs     *HomeServer
	client *http.Client
}

// NewPushService 创建推送服务
func NewPushService(hs *HomeServer) *PushService {
	return &PushService{
		hs:     hs,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetPushers 获取用户的所有推送器
func (ps *PushService) GetPushers(userID string) ([]Pusher, error) {
	var pushers []Pusher
	if err := db.GetDB().Where("user_id = ? AND enabled = ?", userID, true).Find(&pushers).Error; err != nil {
		return nil, err
	}
	return pushers, nil
}

// SetPusher 设置推送器（创建或更新）
func (ps *PushService) SetPusher(userID string, pusher *Pusher) error {
	// 检查是否已存在
	var existing Pusher
	result := db.GetDB().Where("user_id = ? AND pushkey = ? AND app_id = ?", userID, pusher.Pushkey, pusher.AppID).First(&existing)

	pusher.UserID = userID
	if result.Error == nil {
		// 更新
		existing.AppDisplayName = pusher.AppDisplayName
		existing.DeviceDisplayName = pusher.DeviceDisplayName
		existing.ProfileTag = pusher.ProfileTag
		existing.Lang = pusher.Lang
		existing.Data = pusher.Data
		existing.Enabled = true
		existing.UpdatedAt = time.Now()
		return db.GetDB().Save(&existing).Error
	}

	// 创建
	pusher.CreatedAt = time.Now()
	pusher.UpdatedAt = time.Now()
	return db.GetDB().Create(pusher).Error
}

// RemovePusher 删除推送器
func (ps *PushService) RemovePusher(userID, pushkey, appID string) error {
	return db.GetDB().Where("user_id = ? AND pushkey = ? AND app_id = ?", userID, pushkey, appID).
		Delete(&Pusher{}).Error
}

// GetPushRules 获取推送规则
func (ps *PushService) GetPushRules(userID string) (*PushRulesResponse, error) {
	var rules []PushRule
	if err := db.GetDB().Where("user_id = ? AND scope = ?", userID, "global").Find(&rules).Error; err != nil {
		return nil, err
	}

	// 如果没有规则，创建默认规则
	if len(rules) == 0 {
		ps.createDefaultRules(userID)
		db.GetDB().Where("user_id = ? AND scope = ?", userID, "global").Find(&rules)
	}

	response := &PushRulesResponse{
		Global: PushRuleset{
			Override:  []PushRuleResponse{},
			Content:   []PushRuleResponse{},
			Room:      []PushRuleResponse{},
			Sender:    []PushRuleResponse{},
			Underride: []PushRuleResponse{},
		},
	}

	for _, rule := range rules {
		resp := ps.ruleToResponse(&rule)
		switch rule.Kind {
		case "override":
			response.Global.Override = append(response.Global.Override, resp)
		case "content":
			response.Global.Content = append(response.Global.Content, resp)
		case "room":
			response.Global.Room = append(response.Global.Room, resp)
		case "sender":
			response.Global.Sender = append(response.Global.Sender, resp)
		case "underride":
			response.Global.Underride = append(response.Global.Underride, resp)
		}
	}

	return response, nil
}

// ruleToResponse 转换规则为响应格式
func (ps *PushService) ruleToResponse(rule *PushRule) PushRuleResponse {
	resp := PushRuleResponse{
		RuleID:  rule.RuleID,
		Default: rule.Default,
		Enabled: rule.Enabled,
		Pattern: rule.Pattern,
	}

	// 解析条件
	if rule.Conditions != "" {
		json.Unmarshal([]byte(rule.Conditions), &resp.Conditions)
	}

	// 解析动作
	if rule.Actions != "" {
		var actions []interface{}
		json.Unmarshal([]byte(rule.Actions), &actions)
		resp.Actions = actions
	}

	return resp
}

// SetPushRule 设置推送规则
func (ps *PushService) SetPushRule(userID, scope, kind, ruleID string, rule *PushRule) error {
	rule.UserID = userID
	rule.Scope = scope
	rule.Kind = kind
	rule.RuleID = ruleID
	rule.UpdatedAt = time.Now()

	// 检查是否已存在
	var existing PushRule
	result := db.GetDB().Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?", userID, scope, kind, ruleID).First(&existing)

	if result.Error == nil {
		// 更新
		existing.Enabled = rule.Enabled
		existing.Conditions = rule.Conditions
		existing.Actions = rule.Actions
		existing.Pattern = rule.Pattern
		existing.UpdatedAt = time.Now()
		return db.GetDB().Save(&existing).Error
	}

	// 创建
	rule.CreatedAt = time.Now()
	return db.GetDB().Create(rule).Error
}

// DeletePushRule 删除推送规则
func (ps *PushService) DeletePushRule(userID, scope, kind, ruleID string) error {
	return db.GetDB().Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?", userID, scope, kind, ruleID).
		Delete(&PushRule{}).Error
}

// createDefaultRules 创建默认推送规则
func (ps *PushService) createDefaultRules(userID string) {
	defaultRules := []PushRule{
		// .m.rule.master - 主规则（禁用所有通知）
		{
			UserID:    userID,
			Scope:     "global",
			Kind:      "override",
			RuleID:    ".m.rule.master",
			Default:   true,
			Enabled:   false,
			Actions:   `["dont_notify"]`,
		},
		// .m.rule.suppress_notices - 禁止通知消息
		{
			UserID:    userID,
			Scope:     "global",
			Kind:      "override",
			RuleID:    ".m.rule.suppress_notices",
			Default:   true,
			Enabled:   true,
			Conditions: `[{"kind":"event_match","key":"content.msgtype","pattern":"m.notice"}]`,
			Actions:   `["dont_notify"]`,
		},
		// .m.rule.invite_for_me - 邀请通知
		{
			UserID:    userID,
			Scope:     "global",
			Kind:      "underride",
			RuleID:    ".m.rule.invite_for_me",
			Default:   true,
			Enabled:   true,
			Conditions: `[{"kind":"event_match","key":"type","pattern":"m.room.member"},{"kind":"event_match","key":"content.membership","pattern":"invite"},{"kind":"event_match","key":"state_key","pattern":"` + userID + `"}]`,
			Actions:   `["notify",{"set_tweak":"sound","value":"default"},{"set_tweak":"highlight","value":false}]`,
		},
		// .m.rule.contains_display_name - 提到显示名称
		{
			UserID:    userID,
			Scope:     "global",
			Kind:      "override",
			RuleID:    ".m.rule.contains_display_name",
			Default:   true,
			Enabled:   true,
			Actions:   `["notify",{"set_tweak":"sound","value":"default"},{"set_tweak":"highlight","value":true}]`,
		},
		// .m.rule.roomnotif - @room 提及
		{
			UserID:    userID,
			Scope:     "global",
			Kind:      "override",
			RuleID:    ".m.rule.roomnotif",
			Default:   true,
			Enabled:   true,
			Conditions: `[{"kind":"event_match","key":"content.body","pattern":"@room"}]`,
			Actions:   `["notify",{"set_tweak":"highlight","value":true}]`,
		},
	}

	for _, rule := range defaultRules {
		rule.CreatedAt = time.Now()
		rule.UpdatedAt = time.Now()
		db.GetDB().Create(&rule)
	}
}

// NotifyEvent 评估事件并发送推送通知
func (ps *PushService) NotifyEvent(event *MatrixEvent, room *MatrixRoom) {
	// 获取房间中的所有成员
	var members []MatrixRoomMember
	db.GetDB().Where("room_id = ? AND membership = ?", event.RoomID, MembershipJoin).Find(&members)

	for _, member := range members {
		// 不通知发送者自己
		if member.UserID == event.Sender {
			continue
		}

		// 评估规则
		shouldNotify, actions := ps.evaluateRules(member.UserID, event, room)
		if !shouldNotify {
			continue
		}

		// 发送推送
		ps.sendPush(member.UserID, event, room, actions)
	}
}

// evaluateRules 评估推送规则
func (ps *PushService) evaluateRules(userID string, event *MatrixEvent, room *MatrixRoom) (bool, []PushAction) {
	// 获取用户的推送规则
	var rules []PushRule
	db.GetDB().Where("user_id = ? AND scope = ? AND enabled = ?", userID, "global", true).
		Order("priority_class DESC, id ASC").
		Find(&rules)

	// 评估每个规则
	for _, rule := range rules {
		if ps.matchesRule(&rule, event, room, userID) {
			var actions []PushAction
			if err := json.Unmarshal([]byte(rule.Actions), &actions); err == nil {
				// 检查是否是 dont_notify
				for _, action := range actions {
					if action.Action == "dont_notify" {
						return false, nil
					}
				}
				return true, actions
			}
		}
	}

	return false, nil
}

// matchesRule 检查事件是否匹配规则
func (ps *PushService) matchesRule(rule *PushRule, event *MatrixEvent, room *MatrixRoom, userID string) bool {
	// 如果是内容规则，检查模式
	if rule.Kind == "content" && rule.Pattern != "" {
		if body, ok := event.Content["body"].(string); ok {
			if strings.Contains(body, rule.Pattern) {
				return true
			}
		}
	}

	// 如果是房间规则，检查房间 ID
	if rule.Kind == "room" {
		return rule.RuleID == event.RoomID
	}

	// 如果是发送者规则，检查发送者
	if rule.Kind == "sender" {
		return rule.RuleID == event.Sender
	}

	// 评估条件
	if rule.Conditions == "" {
		return false
	}

	var conditions []PushCondition
	if err := json.Unmarshal([]byte(rule.Conditions), &conditions); err != nil {
		return false
	}

	for _, cond := range conditions {
		if !ps.matchesCondition(&cond, event, room, userID) {
			return false
		}
	}

	return true
}

// matchesCondition 检查条件是否匹配
func (ps *PushService) matchesCondition(cond *PushCondition, event *MatrixEvent, room *MatrixRoom, userID string) bool {
	switch cond.Kind {
	case "event_match":
		return ps.matchEventField(event, cond.Key, cond.Pattern)
	case "contains_display_name":
		// 检查消息体是否包含用户的显示名称
		if body, ok := event.Content["body"].(string); ok {
			// 获取用户的显示名称
			var member MatrixRoomMember
			db.GetDB().Where("room_id = ? AND user_id = ?", event.RoomID, userID).First(&member)
			if member.DisplayName != nil && *member.DisplayName != "" {
				return strings.Contains(body, *member.DisplayName)
			}
			// 如果没有显示名称，使用用户 ID 的用户名部分
			parts := strings.Split(userID, ":")
			if len(parts) > 0 {
				username := strings.TrimPrefix(parts[0], "@")
				return strings.Contains(body, username)
			}
		}
		return false
	case "room_member_count":
		// 检查房间成员数
		var count int64
		db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND membership = ?", event.RoomID, MembershipJoin).Count(&count)
		
		// 简单解析：只处理 "2" 这样的数字
		if cond.Is == fmt.Sprintf("%d", count) {
			return true
		}
		return false
	}
	return false
}

// matchEventField 匹配事件字段
func (ps *PushService) matchEventField(event *MatrixEvent, key, pattern string) bool {
	var value string
	
	switch key {
	case "type":
		value = string(event.EventType)
	case "sender":
		value = event.Sender
	case "room_id":
		value = event.RoomID
	default:
		// 尝试从 content 获取
		if strings.HasPrefix(key, "content.") {
			field := strings.TrimPrefix(key, "content.")
			if v, ok := event.Content[field].(string); ok {
				value = v
			}
		}
	}
	
	return value == pattern
}

// sendPush 发送推送通知
func (ps *PushService) sendPush(userID string, event *MatrixEvent, room *MatrixRoom, actions []PushAction) {
	// 获取用户的推送器
	pushers, err := ps.GetPushers(userID)
	if err != nil || len(pushers) == 0 {
		return
	}

	// 构建通知
	notification := ps.buildNotification(userID, event, room, pushers)

	// 发送到每个推送器
	for _, pusher := range pushers {
		if pusher.Kind != "http" {
			continue
		}

		// 解析推送器数据
		url, _ := pusher.Data["url"].(string)
		if url == "" {
			continue
		}

		// 发送 HTTP 请求
		go ps.sendPushRequest(url, notification)
	}
}

// buildNotification 构建推送通知
func (ps *PushService) buildNotification(userID string, event *MatrixEvent, room *MatrixRoom, pushers []Pusher) *PushNotification {
	// 获取发送者的显示名称
	var senderMember MatrixRoomMember
	db.GetDB().Where("room_id = ? AND user_id = ?", event.RoomID, event.Sender).First(&senderMember)
	
	senderDisplayName := event.Sender
	if senderMember.DisplayName != nil && *senderMember.DisplayName != "" {
		senderDisplayName = *senderMember.DisplayName
	}

	// 获取房间名称
	roomName := event.RoomID
	if room != nil && room.Name != nil && *room.Name != "" {
		roomName = *room.Name
	}

	// 构建设备列表
	devices := make([]PushDevice, 0, len(pushers))
	for _, pusher := range pushers {
		var data PusherData
		if pusher.Data != nil {
			data.URL, _ = pusher.Data["url"].(string)
			data.Format, _ = pusher.Data["format"].(string)
			data.Brand, _ = pusher.Data["brand"].(string)
		}

		devices = append(devices, PushDevice{
			AppID:     pusher.AppID,
			Pushkey:   pusher.Pushkey,
			PushkeyTs: pusher.UpdatedAt.Unix(),
			Data:      data,
		})
	}

	// 获取未读计数
	var unreadCount int64
	db.GetDB().Model(&MatrixReceipt{}).
		Where("room_id = ? AND user_id = ? AND receipt_type = ?", event.RoomID, userID, "m.read").
		Count(&unreadCount)

	return &PushNotification{
		Notification: PushNotificationData{
			EventID:           event.EventID,
			RoomID:            event.RoomID,
			Type:              string(event.EventType),
			Sender:            event.Sender,
			SenderDisplayName: senderDisplayName,
			RoomName:          roomName,
			Content:           event.Content,
			Counts: &PushCounts{
				Unread: int(unreadCount),
			},
			Devices: devices,
		},
	}
}

// sendPushRequest 发送推送请求
func (ps *PushService) sendPushRequest(url string, notification *PushNotification) {
	data, err := json.Marshal(notification)
	if err != nil {
		return
	}

	resp, err := ps.client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Error("Failed to send push notification: %v", err)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		log.Error("Push gateway returned error: %d", resp.StatusCode)
	}
}

// ===== HTTP Handlers =====

// RegisterPushRoutes 注册推送 API 路由
func RegisterPushRoutes(r gin.IRouter) {
	// 推送路由已在 handlers.go 中定义，这里不需要重复
	// 这个函数只是为了保持 API 一致性
}
