package matrix

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/httpclient"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
)

// ASClient Application Service API 客户端
// 用于与 Matrix Homeserver 的 AS API 交互
type ASClient struct {
	homeserverURL string
	asToken       string
	hsToken       string
	httpClient    *httpclient.Client
}

// NewASClient 创建 AS 客户端
func NewASClient() *ASClient {
	LoadConfig()
	return &ASClient{
		homeserverURL: strings.TrimSuffix(GlobalConfig.HomeserverURL, "/"),
		asToken:       GlobalConfig.ASToken,
		hsToken:       GlobalConfig.HSToken,
		httpClient:    httpclient.NewClient(),
	}
}

// NewASClientWithConfig 使用指定配置创建 AS 客户端
func NewASClientWithConfig(cfg *Config) *ASClient {
	return &ASClient{
		homeserverURL: strings.TrimSuffix(cfg.HomeserverURL, "/"),
		asToken:       cfg.ASToken,
		hsToken:       cfg.HSToken,
		httpClient:    httpclient.NewClient(),
	}
}

// ValidateHSToken 验证来自 HS 的 Token
func (c *ASClient) ValidateHSToken(token string) bool {
	return token == c.hsToken
}

// PingHS 测试与 Homeserver 的连接
func (c *ASClient) PingHS() error {
	// 使用 Client API 的版本端点测试连接
	path := "/_matrix/client/versions"

	req := c.httpClient.NewRequest("GET", c.homeserverURL+path).
		SetHeader("Authorization", "Bearer "+c.asToken).
		SetTimeout(10)

	resp, err := req.Do()
	if err != nil {
		return errors.Wrap(err, "failed to connect to Homeserver")
	}

	if !resp.StatusOk() {
		return fmt.Errorf("Homeserver returned status %d", resp.StatusCode)
	}

	return nil
}

// RegisterVirtualUser 注册虚拟用户
func (c *ASClient) RegisterVirtualUser(localpart string) error {
	path := "/_matrix/client/v3/register"

	body := map[string]interface{}{
		"type":          "m.login.application_service",
		"username":      localpart,
		"inhibit_login": true,
	}

	var result interface{}
	return c.doASRequest("POST", path, body, &result)
}

// EnsureVirtualUser 确保虚拟用户存在
func (c *ASClient) EnsureVirtualUser(localpart string) error {
	// 尝试注册，如果用户已存在则忽略错误
	err := c.RegisterVirtualUser(localpart)
	if err != nil {
		// 如果是用户已存在的错误，忽略
		if strings.Contains(err.Error(), "M_USER_IN_USE") {
			return nil
		}
		return err
	}
	return nil
}

// CreateDirectRoom 创建私信房间
func (c *ASClient) CreateDirectRoom(creator, invitee string, isDirect bool) (*RoomCreateResponse, error) {
	path := "/_matrix/client/v3/createRoom?user_id=" + url.QueryEscape(creator)

	req := &RoomCreateRequest{
		Preset:   "trusted_private_chat", // 私信房间预设
		IsDirect: isDirect,
		Invite:   []string{invitee},
		InitialState: []Event{
			{
				Type:     EventTypeRoomCreate,
				Content:  map[string]interface{}{"creator": creator, "is_direct": isDirect},
				StateKey: utils.GetPtr(""),
			},
		},
		CreationContent: map[string]interface{}{
			"is_direct": isDirect,
		},
	}

	var resp RoomCreateResponse
	if err := c.doASRequest("POST", path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SendRoomMessage 发送房间消息
func (c *ASClient) SendRoomMessage(roomID, sender string, content *RoomMessageContent) (*SendEventResponse, error) {
	encodedRoomID := url.PathEscape(roomID)
	txnID := generateTxnID()
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/%s/%s?user_id=%s",
		encodedRoomID, EventTypeRoomMessage, txnID, url.QueryEscape(sender))

	var resp SendEventResponse
	if err := c.doASRequest("PUT", path, content, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SendRoomMessageSimple 发送简单文本消息
func (c *ASClient) SendRoomMessageSimple(roomID, sender, body string) (*SendEventResponse, error) {
	content := &RoomMessageContent{
		MsgType: MsgTypeText,
		Body:    body,
	}
	return c.SendRoomMessage(roomID, sender, content)
}

// InviteToRoom 邀请用户加入房间
func (c *ASClient) InviteToRoom(roomID, inviter, invitee string) error {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/invite?user_id=%s",
		encodedRoomID, url.QueryEscape(inviter))

	body := map[string]string{"user_id": invitee}
	return c.doASRequest("POST", path, body, nil)
}

// InviteUser 邀请用户加入房间（简化版，使用 bridge 用户邀请）
func (c *ASClient) InviteUser(roomID, invitee string) error {
	LoadConfig()
	inviter := "@" + GlobalConfig.BridgeUsername + ":" + GlobalConfig.ServerName
	return c.InviteToRoom(roomID, inviter, invitee)
}

// CreateRoomConfig 房间创建配置
type CreateRoomConfig struct {
	Name       string   `json:"name,omitempty"`
	Topic      string   `json:"topic,omitempty"`
	Alias      string   `json:"room_alias_name,omitempty"`
	Preset     string   `json:"preset,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	IsDirect   bool     `json:"is_direct,omitempty"`
	Invite     []string `json:"invite,omitempty"`
}

// CreateRoom 创建房间
func (c *ASClient) CreateRoom(config *CreateRoomConfig) (string, error) {
	LoadConfig()
	bridgeUser := "@" + GlobalConfig.BridgeUsername + ":" + GlobalConfig.ServerName
	path := "/_matrix/client/v3/createRoom?user_id=" + url.QueryEscape(bridgeUser)

	if config.Preset == "" {
		config.Preset = "private_chat"
	}

	var resp RoomCreateResponse
	if err := c.doASRequest("POST", path, config, &resp); err != nil {
		return "", err
	}
	return resp.RoomID, nil
}

// JoinRoom 加入房间
func (c *ASClient) JoinRoom(roomID, userID string) error {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/join?user_id=%s",
		encodedRoomID, url.QueryEscape(userID))

	return c.doASRequest("POST", path, struct{}{}, nil)
}

// LeaveRoom 离开房间
func (c *ASClient) LeaveRoom(roomID, userID string) error {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/leave?user_id=%s",
		encodedRoomID, url.QueryEscape(userID))

	return c.doASRequest("POST", path, struct{}{}, nil)
}

// SetUserProfile 设置用户档案
func (c *ASClient) SetUserProfile(userID, displayName, avatarURL string) error {
	// 设置显示名称
	if displayName != "" {
		encodedUserID := url.PathEscape(userID)
		path := fmt.Sprintf("/_matrix/client/v3/profile/%s/displayname?user_id=%s",
			encodedUserID, url.QueryEscape(userID))
		body := map[string]string{"displayname": displayName}
		if err := c.doASRequest("PUT", path, body, nil); err != nil {
			return err
		}
	}

	// 设置头像
	if avatarURL != "" {
		encodedUserID := url.PathEscape(userID)
		path := fmt.Sprintf("/_matrix/client/v3/profile/%s/avatar_url?user_id=%s",
			encodedUserID, url.QueryEscape(userID))
		body := map[string]string{"avatar_url": avatarURL}
		if err := c.doASRequest("PUT", path, body, nil); err != nil {
			return err
		}
	}

	return nil
}

// GetRoomStateEvent 获取房间状态事件
func (c *ASClient) GetRoomStateEvent(roomID, eventType, stateKey, asUserID string) (*Event, error) {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/state/%s", encodedRoomID, eventType)
	if stateKey != "" {
		path += "/" + url.PathEscape(stateKey)
	}
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	var event Event
	if err := c.doASRequest("GET", path, nil, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

// GetRoomMembers 获取房间成员列表
func (c *ASClient) GetRoomMembers(roomID, asUserID string) ([]Event, error) {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/members", encodedRoomID)
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	var resp struct {
		Chunk []Event `json:"chunk"`
	}
	if err := c.doASRequest("GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Chunk, nil
}

// SyncRoomEvents 同步房间事件
func (c *ASClient) SyncRoomEvents(asUserID, since string, timeout int) (*SyncResponse, error) {
	path := fmt.Sprintf("/_matrix/client/v3/sync?user_id=%s&set_presence=offline", url.QueryEscape(asUserID))
	if since != "" {
		path += "&since=" + url.QueryEscape(since)
	}
	if timeout > 0 {
		path += fmt.Sprintf("&timeout=%d", timeout)
	}
	// 只同步已加入房间的消息
	path += "&filter=" + url.QueryEscape(`{"room":{"timeline":{"limit":50}}}`)

	var resp SyncResponse
	if err := c.doASRequest("GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// doASRequest 执行 AS API 请求
func (c *ASClient) doASRequest(method, path string, body interface{}, result interface{}) error {
	fullURL := c.homeserverURL + path

	req := c.httpClient.NewRequest(method, fullURL).
		SetHeader("Authorization", "Bearer "+c.asToken).
		SetHeader("Content-Type", "application/json").
		SetTimeout(30).
		SetTryTimes(3)

	if body != nil {
		req.SetJsonBody(body)
	}

	resp, err := req.Do()
	if err != nil {
		return errors.Wrap(err, "AS API request failed")
	}

	if !resp.StatusOk() {
		var errResp ErrorResponse
		if parseErr := resp.Json(&errResp); parseErr == nil && errResp.ErrCode != "" {
			return fmt.Errorf("AS API error: %s - %s", errResp.ErrCode, errResp.Error)
		}
		return fmt.Errorf("AS API HTTP error: %d", resp.StatusCode)
	}

	if result == nil {
		return nil
	}

	if err := resp.Json(result); err != nil {
		return errors.Wrap(err, "failed to parse AS API response")
	}

	return nil
}


