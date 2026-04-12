package matrix

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/httpclient"
	"github.com/pkg/errors"
)

// Client Matrix Client-Server API 客户端
type Client struct {
	homeserverURL string
	asToken       string
	httpClient    *httpclient.Client
}

// NewClient 创建 Matrix 客户端
func NewClient() *Client {
	LoadConfig()
	return &Client{
		homeserverURL: strings.TrimSuffix(GlobalConfig.HomeserverURL, "/"),
		asToken:       GlobalConfig.ASToken,
		httpClient:    httpclient.NewClient(),
	}
}

// NewClientWithConfig 使用指定配置创建客户端
func NewClientWithConfig(cfg *Config) *Client {
	return &Client{
		homeserverURL: strings.TrimSuffix(cfg.HomeserverURL, "/"),
		asToken:       cfg.ASToken,
		httpClient:    httpclient.NewClient(),
	}
}

// SetASToken 设置 AS Token
func (c *Client) SetASToken(token string) {
	c.asToken = token
}

// GetUserID 获取指定用户的信息
func (c *Client) GetUserID(userID string) (*UserProfile, error) {
	encodedUserID := url.PathEscape(userID)
	path := fmt.Sprintf("/_matrix/client/v3/profile/%s", encodedUserID)

	var profile UserProfile
	if err := c.doRequest("GET", path, nil, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetRoomState 获取房间状态事件
func (c *Client) GetRoomState(roomID, eventType, stateKey string) (*Event, error) {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/state/%s", encodedRoomID, eventType)
	if stateKey != "" {
		path += "/" + url.PathEscape(stateKey)
	}

	var event Event
	if err := c.doRequest("GET", path, nil, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

// GetRoomMembers 获取房间成员列表
func (c *Client) GetRoomMembers(roomID string) ([]Event, error) {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/members", encodedRoomID)

	var resp struct {
		Chunk []Event `json:"chunk"`
	}
	if err := c.doRequest("GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Chunk, nil
}

// CreateRoom 创建房间
func (c *Client) CreateRoom(req *RoomCreateRequest, asUserID string) (*RoomCreateResponse, error) {
	path := "/_matrix/client/v3/createRoom"
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	var resp RoomCreateResponse
	if err := c.doRequest("POST", path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// InviteUser 邀请用户加入房间
func (c *Client) InviteUser(roomID, userID, asUserID string) error {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/invite", encodedRoomID)
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	body := map[string]string{"user_id": userID}
	return c.doRequest("POST", path, body, nil)
}

// JoinRoom 加入房间
func (c *Client) JoinRoom(roomID, asUserID string) error {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/join", encodedRoomID)
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	return c.doRequest("POST", path, struct{}{}, nil)
}

// LeaveRoom 离开房间
func (c *Client) LeaveRoom(roomID, asUserID string) error {
	encodedRoomID := url.PathEscape(roomID)
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/leave", encodedRoomID)
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	return c.doRequest("POST", path, struct{}{}, nil)
}

// SendMessage 发送消息事件
func (c *Client) SendMessage(roomID, asUserID string, content *RoomMessageContent) (*SendEventResponse, error) {
	encodedRoomID := url.PathEscape(roomID)
	txnID := generateTxnID()
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/%s/%s", encodedRoomID, EventTypeRoomMessage, txnID)
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	var resp SendEventResponse
	if err := c.doRequest("PUT", path, content, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SendEvent 发送自定义事件
func (c *Client) SendEvent(roomID, eventType, asUserID string, content map[string]interface{}) (*SendEventResponse, error) {
	encodedRoomID := url.PathEscape(roomID)
	txnID := generateTxnID()
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/%s/%s", encodedRoomID, eventType, txnID)
	if asUserID != "" {
		path += "?user_id=" + url.QueryEscape(asUserID)
	}

	var resp SendEventResponse
	if err := c.doRequest("PUT", path, content, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetDisplayName 设置用户显示名称
func (c *Client) SetDisplayName(asUserID, displayName string) error {
	encodedUserID := url.PathEscape(asUserID)
	path := fmt.Sprintf("/_matrix/client/v3/profile/%s/displayname?user_id=%s", encodedUserID, url.QueryEscape(asUserID))

	body := map[string]string{"displayname": displayName}
	return c.doRequest("PUT", path, body, nil)
}

// SetAvatarURL 设置用户头像
func (c *Client) SetAvatarURL(asUserID, avatarURL string) error {
	encodedUserID := url.PathEscape(asUserID)
	path := fmt.Sprintf("/_matrix/client/v3/profile/%s/avatar_url?user_id=%s", encodedUserID, url.QueryEscape(asUserID))

	body := map[string]string{"avatar_url": avatarURL}
	return c.doRequest("PUT", path, body, nil)
}

// RegisterUser 注册虚拟用户（使用 AS 特权）
func (c *Client) RegisterUser(username string) error {
	path := "/_matrix/client/v3/register"

	body := map[string]interface{}{
		"type":          "m.login.application_service",
		"username":      username,
		"inhibit_login": true,
	}

	var resp interface{}
	return c.doRequest("POST", path, body, &resp)
}

// Sync 同步事件（可选，用于主动拉取）
func (c *Client) Sync(asUserID, since string, timeout int) (*SyncResponse, error) {
	path := fmt.Sprintf("/_matrix/client/v3/sync?user_id=%s", url.QueryEscape(asUserID))
	if since != "" {
		path += "&since=" + url.QueryEscape(since)
	}
	if timeout > 0 {
		path += fmt.Sprintf("&timeout=%d", timeout)
	}

	var resp SyncResponse
	if err := c.doRequest("GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// doRequest 执行 HTTP 请求
func (c *Client) doRequest(method, path string, body interface{}, result interface{}) error {
	return doMatrixRequest(c.httpClient, c.homeserverURL, c.asToken, method, path, body, result)
}

// doMatrixRequest 执行 Matrix HTTP 请求（Client 和 ASClient 共用）
func doMatrixRequest(httpClient *httpclient.Client, homeserverURL, asToken, method, path string, body interface{}, result interface{}) error {
	fullURL := homeserverURL + path

	req := httpClient.NewRequest(method, fullURL).
		SetHeader("Authorization", "Bearer "+asToken).
		SetHeader("Content-Type", "application/json").
		SetTimeout(30).
		SetTryTimes(3)

	if body != nil {
		req.SetJsonBody(body)
	}

	resp, err := req.Do()
	if err != nil {
		return errors.Wrap(err, "Matrix API request failed")
	}

	// 检查错误响应
	if !resp.StatusOk() {
		var errResp ErrorResponse
		if parseErr := resp.Json(&errResp); parseErr == nil && errResp.ErrCode != "" {
			return fmt.Errorf("Matrix API error: %s - %s", errResp.ErrCode, errResp.Error)
		}
		return fmt.Errorf("Matrix API HTTP error: %d", resp.StatusCode)
	}

	// 如果不需要解析响应体
	if result == nil {
		return nil
	}

	if err := resp.Json(result); err != nil {
		return errors.Wrap(err, "failed to parse Matrix API response")
	}

	return nil
}

// generateTxnID 生成事务 ID（使用 UUID 保证唯一性）
func generateTxnID() string {
	return "community_" + uuid.New().String()
}
