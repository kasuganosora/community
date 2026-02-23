package homeserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/httpclient"
)

// ===== 事件签名验证增强 =====

// EventAuthVerifier 事件认证验证器
type EventAuthVerifier struct {
	HS *HomeServer
}

// NewEventAuthVerifier 创建事件认证验证器
func NewEventAuthVerifier(hs *HomeServer) *EventAuthVerifier {
	return &EventAuthVerifier{HS: hs}
}

// VerifyEventSignature 验证事件签名（基于 Dendrite 实现）
func (v *EventAuthVerifier) VerifyEventSignature(event map[string]interface{}, allowedServers []string) error {
	// 提取签名
	signatures, ok := event["signatures"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing signatures")
	}

	// 验证每个服务器的签名
	for serverName, serverSigs := range signatures {
		// 检查是否允许该服务器
		if !contains(allowedServers, serverName) {
			continue // 跳过不允许的服务器签名
		}

		serverSigMap, ok := serverSigs.(map[string]interface{})
		if !ok {
			continue
		}

		// 获取服务器公钥
		publicKey, err := v.getServerPublicKey(serverName)
		if err != nil {
			return fmt.Errorf("failed to get server key for %s: %w", serverName, err)
		}

		// 验证每个密钥的签名
		for keyID, sig := range serverSigMap {
			sigStr, ok := sig.(string)
			if !ok {
				continue
			}

			if err := v.verifySignature(event, serverName, keyID, sigStr, publicKey); err != nil {
				return fmt.Errorf("signature verification failed for %s/%s: %w", serverName, keyID, err)
			}
		}
	}

	return nil
}

// VerifyFederationEvent 验证联邦事件（完整的认证流程）
func (v *EventAuthVerifier) VerifyFederationEvent(event map[string]interface{}) error {
	// 1. 验证事件基本结构
	if err := v.validateEventStructure(event); err != nil {
		return fmt.Errorf("invalid event structure: %w", err)
	}

	// 2. 获取发送服务器
	sender, _ := event["sender"].(string)
	serverName := extractServerName(sender)

	// 3. 验证发送服务器签名
	if err := v.VerifyEventSignature(event, []string{serverName}); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	// 4. 验证事件 ID 格式和内容哈希
	if err := v.verifyEventID(event); err != nil {
		return fmt.Errorf("event ID verification failed: %w", err)
	}

	// 5. 验证事件权限（auth_events 链）
	if err := v.verifyAuthEvents(event); err != nil {
		return fmt.Errorf("auth events verification failed: %w", err)
	}

	return nil
}

// validateEventStructure 验证事件基本结构
func (v *EventAuthVerifier) validateEventStructure(event map[string]interface{}) error {
	requiredFields := []string{"event_id", "type", "room_id", "sender", "content", "origin_server_ts"}
	for _, field := range requiredFields {
		if _, ok := event[field]; !ok {
			return fmt.Errorf("missing required field: %s", field)
		}
	}

	// 验证事件类型
	eventType, _ := event["type"].(string)
	if eventType == "" {
		return fmt.Errorf("invalid event type")
	}

	// 验证房间 ID
	roomID, _ := event["room_id"].(string)
	if !strings.HasPrefix(roomID, "!") {
		return fmt.Errorf("invalid room ID format")
	}

	// 验证发送者
	sender, _ := event["sender"].(string)
	if !strings.HasPrefix(sender, "@") {
		return fmt.Errorf("invalid sender format")
	}

	return nil
}

// verifySignature 验证单个签名
func (v *EventAuthVerifier) verifySignature(event map[string]interface{}, serverName, keyID, signature string, publicKey ed25519.PublicKey) error {
	// 创建不含签名的副本进行验证
	eventCopy := make(map[string]interface{})
	for k, v := range event {
		if k != "signatures" && k != "unsigned" {
			eventCopy[k] = v
		}
	}

	// 使用 canonical JSON（Matrix 规范要求）
	data, err := CanonicalJSON(eventCopy)
	if err != nil {
		return fmt.Errorf("failed to canonicalize event: %w", err)
	}

	// 解码签名
	sigBytes, err := base64.RawStdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// 验证签名（直接验证 canonical bytes，不做 hash）
	if !ed25519.Verify(publicKey, data, sigBytes) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// verifyEventID 验证事件 ID（基于内容哈希）
func (v *EventAuthVerifier) verifyEventID(event map[string]interface{}) error {
	eventID, _ := event["event_id"].(string)
	sender, _ := event["sender"].(string)
	roomID, _ := event["room_id"].(string)
	originServerTS, _ := event["origin_server_ts"].(float64)

	// 所有 Matrix 事件 ID 都以 $ 开头
	if !strings.HasPrefix(eventID, "$") {
		return fmt.Errorf("invalid event ID format: must start with $")
	}

	// v3+ 房间版本使用基于哈希的事件 ID
	// 提取预期哈希并验证
	expectedHash := eventID[1:]

	// 计算实际哈希
	hash := sha256.New()
	hash.Write([]byte(fmt.Sprintf("%s:%s:%d", sender, roomID, int64(originServerTS))))
	actualHash := base64.RawURLEncoding.EncodeToString(hash.Sum(nil))

	if expectedHash != actualHash {
		return fmt.Errorf("event ID hash mismatch")
	}

	return nil
}

// verifyAuthEvents 验证 auth_events 链
func (v *EventAuthVerifier) verifyAuthEvents(event map[string]interface{}) error {
	authEvents, ok := event["auth_events"].([]interface{})
	if !ok || len(authEvents) == 0 {
		// 某些事件类型不需要 auth_events
		eventType, _ := event["type"].(string)
		if eventType != "m.room.create" {
			return fmt.Errorf("missing auth_events")
		}
		return nil
	}

	// 对于每个 auth_event，验证它存在且有效
	for _, authEventID := range authEvents {
		eventID, ok := authEventID.(string)
		if !ok {
			continue
		}

		var authEvent MatrixEvent
		if err := db.GetDB().Where("event_id = ?", eventID).First(&authEvent).Error; err != nil {
			return fmt.Errorf("auth event not found: %s", eventID)
		}

		// 验证 auth_event 的签名
		var authEventMap map[string]interface{}
		authEventData, _ := json.Marshal(authEvent)
		json.Unmarshal(authEventData, &authEventMap)

		if err := v.VerifyEventSignature(authEventMap, []string{authEvent.SenderDomain}); err != nil {
			return fmt.Errorf("auth event signature invalid: %s", eventID)
		}
	}

	return nil
}

// getServerPublicKey 获取服务器公钥（带缓存）
func (v *EventAuthVerifier) getServerPublicKey(serverName string) (ed25519.PublicKey, error) {
	// 使用全局密钥缓存
	keyCache := GetKeyCache()

	// 先查询数据库中的缓存
	var serverKey MatrixServerKey
	if err := db.GetDB().Where("server_name = ? AND valid_until > ?", serverName, time.Now()).
		Order("valid_until DESC").
		First(&serverKey).Error; err == nil {
		// 使用缓存的密钥
		keyBytes, err := base64.RawStdEncoding.DecodeString(serverKey.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode cached key: %w", err)
		}
		
		// 更新内存缓存
		keyCache.Set(serverName, serverKey.KeyID, ed25519.PublicKey(keyBytes), serverKey.ValidUntil)
		
		return keyBytes, nil
	}

	// 获取远程服务器密钥 - 使用 FederationClient
	client := NewFederationClient(v.HS)

	// 获取 .well-known 信息
	baseURL, err := client.GetWellKnown(serverName)
	if err != nil {
		// 使用默认 URL
		baseURL = "https://" + serverName
	}

	// 获取服务器密钥
	keyResp, err := fetchServerKeys(client, baseURL, serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch server keys: %w", err)
	}

	// 提取公钥
	for keyID, keyInfo := range keyResp.VerifyKeys {
		keyBytes, err := base64.RawStdEncoding.DecodeString(keyInfo.Key)
		if err != nil {
			continue
		}

		// 计算过期时间
		validUntil := time.Now().Add(24 * time.Hour)
		if keyResp.ValidUntil > 0 {
			validUntil = time.UnixMilli(keyResp.ValidUntil)
		}

		// 保存到数据库
		keyCache.saveToDB(serverName, keyID, &KeyCacheEntry{
			PublicKey:   ed25519.PublicKey(keyBytes),
			ValidUntil:  validUntil,
			LastUpdated: time.Now(),
		})

		return keyBytes, nil
	}

	return nil, fmt.Errorf("no valid key found for server %s", serverName)
}

// fetchServerKeys 从远程服务器获取密钥
func fetchServerKeys(client *FederationClient, baseURL, serverName string) (*ServerKeyResponse, error) {
	// 构建请求 URL
	urlStr := baseURL + "/_matrix/key/v2/server"

	// 使用 httpclient 发送请求
	httpReq := httpclient.NewClient().NewRequest("GET", urlStr)
	httpReq.SetTimeout(30)

	resp, err := httpReq.Do()
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	body, err := resp.GetBodyData()
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var keyResp ServerKeyResponse
	if err := json.Unmarshal(body, &keyResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &keyResp, nil
}

// ===== 联邦请求认证中间件 =====

// FederationAuthMiddleware 联邦请求认证中间件
func FederationAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		hs := GetHomeServer()
		if hs == nil || !hs.Config.Enabled {
			c.JSON(http.StatusServiceUnavailable, MatrixError{
				ErrCode: "M_UNKNOWN",
				Error:   "Homeserver not available",
			})
			c.Abort()
			return
		}

		// 获取请求头中的 Authorization
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// 某些联邦请求可能没有 Authorization（如获取服务器密钥）
			c.Next()
			return
		}

		// 解析 X-Matrix 认证
		if strings.HasPrefix(authHeader, "X-Matrix origin=") {
			origin, destination, keyID, signature, err := parseXMatrixAuth(authHeader)
			if err != nil {
				c.JSON(http.StatusUnauthorized, MatrixError{
					ErrCode: "M_UNAUTHORIZED",
					Error:   "Invalid authorization header: " + err.Error(),
				})
				c.Abort()
				return
			}

			// 验证签名
			verifier := NewEventAuthVerifier(hs)
			publicKey, err := verifier.getServerPublicKey(origin)
			if err != nil {
				c.JSON(http.StatusUnauthorized, MatrixError{
					ErrCode: "M_UNAUTHORIZED",
					Error:   "Failed to get server key: " + err.Error(),
				})
				c.Abort()
				return
			}

			// 验证请求签名
			if !verifyRequestSignature(c.Request, origin, destination, keyID, signature, publicKey) {
				c.JSON(http.StatusUnauthorized, MatrixError{
					ErrCode: "M_UNAUTHORIZED",
					Error:   "Invalid request signature",
				})
				c.Abort()
				return
			}

			// 将 origin 存储在上下文中
			c.Set("origin", origin)
		}

		c.Next()
	}
}

// parseXMatrixAuth 解析 X-Matrix 认证头
func parseXMatrixAuth(authHeader string) (origin, destination, keyID, signature string, err error) {
	// 格式: X-Matrix origin="origin.hs",destination="dest.hs",key="ed25519:abc",sig="base64sig"
	authHeader = strings.TrimPrefix(authHeader, "X-Matrix ")

	parts := strings.Split(authHeader, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "origin=") {
			origin = strings.Trim(part[7:], `"`)
		} else if strings.HasPrefix(part, "destination=") {
			destination = strings.Trim(part[12:], `"`)
		} else if strings.HasPrefix(part, "key=") {
			keyID = strings.Trim(part[4:], `"`)
		} else if strings.HasPrefix(part, "sig=") {
			signature = strings.Trim(part[4:], `"`)
		}
	}

	if origin == "" || keyID == "" || signature == "" {
		return "", "", "", "", fmt.Errorf("missing required fields")
	}

	return origin, destination, keyID, signature, nil
}

// verifyRequestSignature 验证请求签名
// Matrix spec: the signed payload is a JSON object {method, uri, origin, destination, content?}
func verifyRequestSignature(req *http.Request, origin, destination, keyID, signature string, publicKey ed25519.PublicKey) bool {
	// 构建 URI (path + query)
	uri := req.URL.RequestURI()

	// 读取请求体并恢复，以便后续中间件可以继续读取
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	// 构建待签名的 JSON 对象（Matrix federation spec）
	payload := map[string]interface{}{
		"method":      req.Method,
		"uri":         uri,
		"origin":      origin,
		"destination": destination,
	}
	if len(body) > 0 {
		var content interface{}
		if err := json.Unmarshal(body, &content); err == nil {
			payload["content"] = content
		}
	}

	// Canonical JSON
	canonicalBytes, err := CanonicalJSON(payload)
	if err != nil {
		return false
	}

	// 解码签名
	sigBytes, err := base64.RawStdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}

	// 验证签名
	return ed25519.Verify(publicKey, canonicalBytes, sigBytes)
}

// ===== 辅助函数 =====

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func extractServerName(userID string) string {
	parts := strings.Split(userID, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
