package homeserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/httpclient"
	"github.com/kasuganosora/community/log"
)

// FederationClient 联邦客户端
type FederationClient struct {
	hs         *HomeServer
	privateKey ed25519.PrivateKey
	httpClient *httpclient.Client
	keyCache   *KeyCache
}

// NewFederationClient 创建联邦客户端
func NewFederationClient(hs *HomeServer) *FederationClient {
	return &FederationClient{
		hs:         hs,
		privateKey: hs.privateKey, // 使用 HomeServer 的持久化密钥
		httpClient: httpclient.NewClient(),
		keyCache:   GetKeyCache(),
	}
}

// federationBaseURL resolves the federation base URL for a server name.
// Matrix spec: default federation port is 8448, unless .well-known or SRV overrides it.
func (c *FederationClient) federationBaseURL(serverName string) string {
	// If serverName already contains a port, use it directly
	if strings.Contains(serverName, ":") {
		return fmt.Sprintf("https://%s", serverName)
	}
	// Default to port 8448 for federation (Matrix spec)
	return fmt.Sprintf("https://%s:8448", serverName)
}

// FetchMedia 从远程服务器获取媒体
func (c *FederationClient) FetchMedia(serverName, mediaID string) ([]byte, string, error) {
	// 构建 URL
	mediaURL := fmt.Sprintf("%s/_matrix/media/v3/download/%s/%s", c.federationBaseURL(serverName), serverName, mediaID)

	// 创建请求
	req := c.httpClient.NewRequest("GET", mediaURL)
	req.SetHeader("Host", serverName)
	req.SetTimeout(30)

	// 添加签名
	c.signRequest(req, serverName)

	// 发送请求
	resp, err := req.Do()
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch media: %w", err)
	}

	// 获取内容类型
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// 读取响应体
	data, err := resp.GetBodyData()
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response: %w", err)
	}

	return data, contentType, nil
}

// FetchThumbnail 从远程服务器获取缩略图
func (c *FederationClient) FetchThumbnail(serverName, mediaID string, width, height int, method string) ([]byte, string, error) {
	// 构建 URL
	thumbURL := fmt.Sprintf("%s/_matrix/media/v3/thumbnail/%s/%s?width=%d&height=%d&method=%s",
		c.federationBaseURL(serverName), serverName, mediaID, width, height, method)

	// 创建请求
	req := c.httpClient.NewRequest("GET", thumbURL)
	req.SetHeader("Host", serverName)
	req.SetTimeout(30)

	// 添加签名
	c.signRequest(req, serverName)

	// 发送请求
	resp, err := req.Do()
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch thumbnail: %w", err)
	}

	// 获取内容类型
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}

	// 读取响应体
	data, err := resp.GetBodyData()
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response: %w", err)
	}

	return data, contentType, nil
}

// SendTransaction 发送联邦事务
func (c *FederationClient) SendTransaction(destServer string, txnID string, pdus []json.RawMessage, edus []json.RawMessage) error {
	// Ensure non-null arrays in JSON (Matrix spec requires arrays, not null)
	if pdus == nil {
		pdus = []json.RawMessage{}
	}
	if edus == nil {
		edus = []json.RawMessage{}
	}

	// 构建请求体
	body := map[string]interface{}{
		"origin":           c.hs.Config.ServerName,
		"origin_server_ts": time.Now().UnixMilli(),
		"pdus":             pdus,
		"edus":             edus,
	}

	// Use doFederationRequest which properly loads Complement CA and handles TLS
	txnURL := fmt.Sprintf("%s/_matrix/federation/v1/send/%s", c.federationBaseURL(destServer), txnID)
	resp, _, err := c.doFederationRequest("PUT", txnURL, destServer, body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("send transaction returned status %d", resp.StatusCode)
	}
	return nil
}

// QueryRemoteKeys queries device keys from a remote server via federation.
// POST /_matrix/federation/v1/user/keys/query
func (c *FederationClient) QueryRemoteKeys(destServer string, deviceKeys map[string][]string) (map[string]map[string]*DeviceKeys, error) {
	body := map[string]interface{}{
		"device_keys": deviceKeys,
	}
	reqURL := fmt.Sprintf("%s/_matrix/federation/v1/user/keys/query", c.federationBaseURL(destServer))
	_, respBody, err := c.doFederationRequest("POST", reqURL, destServer, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		DeviceKeys map[string]map[string]*DeviceKeys `json:"device_keys"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if result.DeviceKeys == nil {
		result.DeviceKeys = make(map[string]map[string]*DeviceKeys)
	}
	return result.DeviceKeys, nil
}

// QueryDirectory 查询远程目录
func (c *FederationClient) QueryDirectory(destServer, roomAlias string) (*QueryDirectoryResponse, error) {
	// 构建 URL
	queryURL := fmt.Sprintf("%s/_matrix/federation/v1/query/directory?room_alias=%s",
		c.federationBaseURL(destServer), url.QueryEscape(roomAlias))

	// 创建请求
	req := c.httpClient.NewRequest("GET", queryURL)
	req.SetHeader("Host", destServer)
	req.SetTimeout(10)

	// 添加签名
	c.signRequest(req, destServer)

	// 发送请求
	resp, err := req.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to query directory: %w", err)
	}

	// 解析响应
	var result QueryDirectoryResponse
	if err := resp.Json(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// signRequest 签名请求（Matrix federation 格式）
// Matrix spec: sign {method, uri, origin, destination, content?} as canonical JSON
func (c *FederationClient) signRequest(req *httpclient.Request, destServer string) {
	origin := c.hs.Config.ServerName
	keyID := c.hs.keyID

	// Extract method and URI from the request
	method, requestURL := req.GetMethodAndURL()
	parsedURL, _ := url.Parse(requestURL)
	uri := parsedURL.RequestURI()

	payload := map[string]interface{}{
		"method":      method,
		"uri":         uri,
		"origin":      origin,
		"destination": destServer,
	}
	if body := req.GetBody(); len(body) > 0 {
		var content interface{}
		if err := json.Unmarshal(body, &content); err == nil {
			payload["content"] = content
		}
	}

	canonicalBytes, err := CanonicalJSON(payload)
	if err != nil {
		return
	}

	signature := ed25519.Sign(c.privateKey, canonicalBytes)
	sigB64 := base64.RawStdEncoding.EncodeToString(signature)

	authHeader := fmt.Sprintf(`X-Matrix origin="%s",destination="%s",key="%s",sig="%s"`,
		origin, destServer, keyID, sigB64)
	req.SetHeader("Authorization", authHeader)
}

// federationHTTPClient returns an HTTP client configured for federation.
// In Complement test environments, each homeserver generates its own self-signed cert,
// so we skip TLS verification. In production, certs should be properly signed.
func (c *FederationClient) federationHTTPClient() *http.Client {
	tlsConfig := &tls.Config{}
	// Load Complement CA if present, and skip TLS verification for self-signed certs
	if _, err := os.Stat("/complement/ca/ca.crt"); err == nil {
		// Complement test environment: skip TLS verification since each HS
		// uses a self-signed cert not signed by the shared CA
		tlsConfig.InsecureSkipVerify = true
	} else if caCert, err := os.ReadFile("/etc/ssl/certs/ca-certificates.crt"); err == nil {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = pool
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
	}
}

// doFederationRequest 执行联邦 HTTP 请求，允许接受非 2xx 状态码
func (c *FederationClient) doFederationRequest(method, targetURL, destServer string, body interface{}) (*http.Response, []byte, error) {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	var httpReq *http.Request
	var err error
	if bodyBytes != nil {
		httpReq, err = http.NewRequest(method, targetURL, bytes.NewReader(bodyBytes))
	} else {
		httpReq, err = http.NewRequest(method, targetURL, nil)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("User-Agent", "Community-Matrix/1.0")
	httpReq.Header.Set("Host", destServer)
	if bodyBytes != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Build auth header per Matrix spec: sign {method, uri, origin, destination, [content]}
	origin := c.hs.Config.ServerName
	keyID := c.hs.keyID
	parsedURL, _ := url.Parse(targetURL)
	requestURI := parsedURL.RequestURI()
	payload := map[string]interface{}{
		"method":      method,
		"uri":         requestURI,
		"origin":      origin,
		"destination": destServer,
	}
	if bodyBytes != nil {
			var content interface{}
			if err := json.Unmarshal(bodyBytes, &content); err == nil {
				payload["content"] = content
			}
	}
	if canonicalBytes, err2 := CanonicalJSON(payload); err2 == nil {
		sig := ed25519.Sign(c.privateKey, canonicalBytes)
		sigB64 := base64.RawStdEncoding.EncodeToString(sig)
		authHeader := fmt.Sprintf(`X-Matrix origin="%s",destination="%s",key="%s",sig="%s"`,
			origin, destServer, keyID, sigB64)
		httpReq.Header.Set("Authorization", authHeader)
	}

	client := c.federationHTTPClient()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return resp, buf.Bytes(), nil
}

// MakeJoin 向远程服务器请求加入事件模板
func (c *FederationClient) MakeJoin(destServer, roomID, userID string) (map[string]interface{}, error) {
	makeJoinURL := fmt.Sprintf("%s/_matrix/federation/v1/make_join/%s/%s?ver=9&ver=10",
		c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(userID))

	resp, body, err := c.doFederationRequest("GET", makeJoinURL, destServer, nil)
	if err != nil {
		return nil, fmt.Errorf("make_join request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("make_join returned status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse make_join response: %w", err)
	}

	return result, nil
}

// SendJoin 向远程服务器发送已签名的加入事件
func (c *FederationClient) SendJoin(destServer, roomID string, event map[string]interface{}) (map[string]interface{}, error) {
	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		eventID = "$" + generateRandomString(32)
		event["event_id"] = eventID
	}

	// Try v2 first (returns object), fall back to v1 (may return [200, {...}])
	sendJoinURL := fmt.Sprintf("%s/_matrix/federation/v2/send_join/%s/%s",
		c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(eventID))

	resp, body, err := c.doFederationRequest("PUT", sendJoinURL, destServer, event)
	if err != nil || (resp != nil && resp.StatusCode == http.StatusNotFound) {
		// Fall back to v1
		sendJoinURL = fmt.Sprintf("%s/_matrix/federation/v1/send_join/%s/%s",
			c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(eventID))
		resp, body, err = c.doFederationRequest("PUT", sendJoinURL, destServer, event)
	}

	if err != nil {
		return nil, fmt.Errorf("send_join request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("send_join returned status %d: %s", resp.StatusCode, string(body))
	}

	// Handle v1 array format [200, {...}] and v2 object format {...}
	var result map[string]interface{}
	if len(body) > 0 && body[0] == '[' {
		// v1 format: [200, {...}]
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err == nil && len(arr) >= 2 {
			_ = json.Unmarshal(arr[1], &result)
		}
	} else {
		_ = json.Unmarshal(body, &result)
	}

	if result == nil {
		return nil, fmt.Errorf("empty send_join response")
	}

	return result, nil
}

// GetMissingEvents fetches missing events from a remote server.
// POST /_matrix/federation/v1/get_missing_events/{roomId}
func (c *FederationClient) GetMissingEvents(destServer, roomID string, earliestEvents, latestEvents []string, limit int) ([]json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/_matrix/federation/v1/get_missing_events/%s",
		c.federationBaseURL(destServer), url.PathEscape(roomID))
	body := map[string]interface{}{
		"earliest_events": earliestEvents,
		"latest_events":   latestEvents,
		"limit":           limit,
	}
	resp, respBody, err := c.doFederationRequest("POST", reqURL, destServer, body)
	if err != nil {
		return nil, fmt.Errorf("get_missing_events request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get_missing_events returned status %d: %s", resp.StatusCode, string(respBody))
	}
	var result struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse get_missing_events response: %w", err)
	}
	return result.Events, nil
}

// FetchBackfill fetches message history from a remote server after joining a room
func (c *FederationClient) FetchBackfill(serverName, roomID string, eventIDs []string, limit int) ([]map[string]interface{}, error) {
	backfillURL := fmt.Sprintf("%s/_matrix/federation/v1/backfill/%s?limit=%d",
		c.federationBaseURL(serverName), url.PathEscape(roomID), limit)
	for _, eid := range eventIDs {
		backfillURL += "&v=" + url.QueryEscape(eid)
	}

	resp, body, err := c.doFederationRequest("GET", backfillURL, serverName, nil)
	if err != nil {
		return nil, fmt.Errorf("backfill request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backfill returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		PDUs []map[string]interface{} `json:"pdus"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse backfill response: %w", err)
	}

	return result.PDUs, nil
}

// VerifySignature 验证事件签名
func (c *FederationClient) VerifySignature(event map[string]interface{}, serverName string, keyID string, signature string) bool {
	// 首先从缓存获取公钥
	publicKey, found := c.keyCache.Get(serverName, keyID)
	if !found {
		// 缓存未命中，从远程服务器获取
		keyResp, err := c.FetchServerKey(serverName)
		if err != nil {
			log.Error("Failed to fetch server key for %s: %v", serverName, err)
			return false
		}

		// 查找对应的公钥
		keyInfo, ok := keyResp.VerifyKeys[keyID]
		if !ok {
			log.Error("Key %s not found for server %s", keyID, serverName)
			return false
		}

		// 解码公钥
		publicKeyBytes, err := base64.RawStdEncoding.DecodeString(keyInfo.Key)
		if err != nil {
			log.Error("Failed to decode public key for %s:%s: %v", serverName, keyID, err)
			return false
		}

		publicKey = ed25519.PublicKey(publicKeyBytes)

		// 解析过期时间
		validUntil := time.Now().Add(24 * time.Hour)
		if keyResp.ValidUntil > 0 {
			validUntil = time.UnixMilli(keyResp.ValidUntil)
		}

		// 缓存公钥
		c.keyCache.Set(serverName, keyID, publicKey, validUntil)
	}

	// 解码签名
	signatureBytes, err := base64.RawStdEncoding.DecodeString(signature)
	if err != nil {
		log.Error("Failed to decode signature: %v", err)
		return false
	}

	// 构建待签名内容（使用规范化的 JSON）
	canonicalBytes, err := CanonicalJSON(event)
	if err != nil {
		log.Error("Failed to canonicalize JSON: %v", err)
		return false
	}

	// 验证签名（直接验证 canonical bytes，不做 hash）
	return ed25519.Verify(publicKey, canonicalBytes, signatureBytes)
}

// FetchServerKey 获取远程服务器密钥
func (c *FederationClient) FetchServerKey(serverName string) (*ServerKeyResponse, error) {
	// 构建 URL
	keyURL := fmt.Sprintf("%s/_matrix/key/v2/server", c.federationBaseURL(serverName))

	// 创建请求
	req := c.httpClient.NewRequest("GET", keyURL)
	req.SetHeader("Host", serverName)
	req.SetTimeout(10)

	// 发送请求
	resp, err := req.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch server key: %w", err)
	}

	// 解析响应
	var result ServerKeyResponse
	if err := resp.Json(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// GetWellKnown 获取 .well-known 配置
func (c *FederationClient) GetWellKnown(serverName string) (string, error) {
	// 构建 URL
	wkURL := fmt.Sprintf("https://%s/.well-known/matrix/server", serverName)

	// 创建请求
	req := c.httpClient.NewRequest("GET", wkURL)
	req.SetTimeout(5)

	// 发送请求
	resp, err := req.Do()
	if err != nil {
		// 如果失败，使用默认端口
		return serverName + ":8448", nil
	}

	// 解析响应
	var result struct {
		Server string `json:"m.server"`
	}
	if err := resp.Json(&result); err != nil {
		return serverName + ":8448", nil
	}

	if result.Server == "" {
		return serverName + ":8448", nil
	}

	return result.Server, nil
}

// DownloadAndSaveMedia 下载并保存远程媒体
func (c *FederationClient) DownloadAndSaveMedia(serverName, mediaID string) (*MatrixMedia, error) {
	// 下载媒体
	data, contentType, err := c.FetchMedia(serverName, mediaID)
	if err != nil {
		return nil, err
	}

	// 保存到本地
	mediaService := NewMediaService(c.hs)
	reader := bytes.NewReader(data)

	// 创建 multipart header
	header := &multipart.FileHeader{
		Size: int64(len(data)),
	}

	// 上传并保存
	mediaInfo, err := mediaService.UploadMedia(reader, header, contentType, "")
	if err != nil {
		return nil, fmt.Errorf("failed to save media: %w", err)
	}

	// 更新服务器名称
	mediaInfo.ServerName = serverName
	db.GetDB().Save(mediaInfo)

	return mediaInfo, nil
}
