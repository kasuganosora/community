package homeserver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/httpclient"
	"github.com/kasuganosora/community/log"
)

// FederationClient 联邦客户端.
type FederationClient struct {
	hs                 *HomeServer
	privateKey         ed25519.PrivateKey
	httpClient         *httpclient.Client
	keyCache           *KeyCache
	fedMediaHTTPClient *http.Client
	fedHTTPClient      *http.Client // cached federation HTTP client
	fedHTTPClientMu    sync.Mutex
}

// NewFederationClient 创建联邦客户端.
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
		return "https://" + serverName
	}
	// Default to port 8448 for federation (Matrix spec)
	return fmt.Sprintf("https://%s", net.JoinHostPort(serverName, "8448"))
}

// FetchMedia 从远程服务器获取媒体.
func (c *FederationClient) FetchMedia(serverName, mediaID string) ([]byte, string, error) {
	media, err := c.fetchAuthenticatedMedia(serverName, mediaID, "download", "")
	if err != nil {
		return nil, "", err
	}
	return media.Data, media.ContentType, nil
}
func (c *FederationClient) FetchThumbnail(serverName, mediaID string, width, height int, method string) ([]byte, string, error) {
	query := fmt.Sprintf("?width=%d&height=%d&method=%s", width, height, url.QueryEscape(method))
	media, err := c.fetchAuthenticatedMedia(serverName, mediaID, "thumbnail", query)
	if err != nil {
		return nil, "", err
	}
	return media.Data, media.ContentType, nil
}

// SendTransaction 发送联邦事务.
func (c *FederationClient) SendTransaction(destServer string, txnID string, pdus []json.RawMessage, edus []json.RawMessage) error {
	// Ensure non-null arrays in JSON (Matrix spec requires arrays, not null)
	if pdus == nil {
		pdus = []json.RawMessage{}
	}

	if edus == nil {
		edus = []json.RawMessage{}
	}

	// 构建请求体
	body := map[string]any{
		"origin":           c.hs.Config.ServerName, //nolint:goconst
		"origin_server_ts": time.Now().UnixMilli(), //nolint:goconst
		"pdus":             pdus,                   //nolint:goconst
		"edus":             edus,
	}

	// Use doFederationRequest which properly loads Complement CA and handles TLS
	txnURL := fmt.Sprintf("%s/_matrix/federation/v1/send/%s", c.federationBaseURL(destServer), txnID)

	resp, _, err := c.doFederationRequest("PUT", txnURL, destServer, body) //nolint:bodyclose // body already closed inside doFederationRequest
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("send transaction returned status %d", resp.StatusCode)
	}

	return nil
}

// QueryRemoteKeys queries device keys from a remote server via federation.
// POST /_matrix/federation/v1/user/keys/query.
func (c *FederationClient) QueryRemoteKeys(destServer string, deviceKeys map[string][]string) (map[string]map[string]*DeviceKeys, error) {
	bundle, err := c.QueryRemoteKeyBundle(destServer, deviceKeys)
	if err != nil {
		return nil, err
	}
	return bundle.DeviceKeys, nil
}

// QueryDirectory 查询远程目录.
func (c *FederationClient) QueryDirectory(destServer, roomAlias string) (*QueryDirectoryResponse, error) {
	queryURL := fmt.Sprintf("%s/_matrix/federation/v1/query/directory?room_alias=%s",
		c.federationBaseURL(destServer), url.QueryEscape(roomAlias))
	resp, body, err := c.doFederationRequest("GET", queryURL, destServer, nil) //nolint:bodyclose // closed in doFederationRequest
	if err != nil {
		return nil, fmt.Errorf("failed to query directory: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: resp.StatusCode, Message: string(body)}
	}
	var result QueryDirectoryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if !strings.HasPrefix(result.RoomID, "!") || len(result.RoomID) < 2 || strings.ContainsAny(result.RoomID, " \t\r\n") {
		return nil, fmt.Errorf("directory returned an invalid room ID")
	}
	return &result, nil
}

// signRequest 签名请求（Matrix federation 格式）
// Matrix spec: sign {method, uri, origin, destination, content?} as canonical JSON.
func (c *FederationClient) signRequest(req *httpclient.Request, destServer string) {
	origin := c.hs.Config.ServerName
	keyID := c.hs.keyID

	// Extract method and URI from the request
	method, requestURL := req.GetMethodAndURL()
	parsedURL, _ := url.Parse(requestURL)
	uri := parsedURL.RequestURI()

	payload := map[string]any{
		"method":      method,
		"uri":         uri,
		"origin":      origin,
		"destination": destServer,
	}

	if body := req.GetBody(); len(body) > 0 {
		var content any
		err := json.Unmarshal(body, &content)
		if err == nil {
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
// A laboratory CA extends the system trust store without disabling certificate
// chain or hostname verification.
func (c *FederationClient) federationHTTPClient() *http.Client {
	c.fedHTTPClientMu.Lock()
	defer c.fedHTTPClientMu.Unlock()
	// Return cached client if already initialized
	if c.fedHTTPClient != nil {
		return c.fedHTTPClient
	}

	tlsConfig := federationTLSConfig("/complement/ca/ca.crt")

	c.fedHTTPClient = &http.Client{
		Timeout:   30 * time.Second,
		Transport: newFederationDiscoveryTransport(tlsConfig),
		// A redirect must not forward a signed request to a different destination.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	return c.fedHTTPClient
}

func federationTLSConfig(extraCAPath string) *tls.Config {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if caCert, err := os.ReadFile(extraCAPath); err == nil {
		pool.AppendCertsFromPEM(caCert)
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// doFederationRequest 执行联邦 HTTP 请求，允许接受非 2xx 状态码.
func (c *FederationClient) doFederationRequest(method, targetURL, destServer string, body any) (*http.Response, []byte, error) {
	httpReq, err := c.prepareSignedFederationRequest(context.Background(), method, targetURL, destServer, body)
	if err != nil {
		return nil, nil, err
	}
	parsedURL := httpReq.URL

	client := c.federationHTTPClient()
	if strings.HasPrefix(parsedURL.Path, "/_matrix/federation/v1/media/") {
		client = c.federationMediaHTTPClient()
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	var buf bytes.Buffer
	var responseReader io.Reader = resp.Body
	if strings.Contains(parsedURL.Path, "/federation/v1/media/") {
		responseReader = io.LimitReader(resp.Body, matrixMediaLimit+(1<<20))
	}
	if _, err := buf.ReadFrom(responseReader); err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return resp, buf.Bytes(), nil
}

// prepareSignedFederationRequest centralizes canonical signatures without binding
// the long-lived HTTP connection pool to a particular request context.
func (c *FederationClient) prepareSignedFederationRequest(ctx context.Context, method, targetURL, destServer string, body any) (*http.Request, error) {
	var bodyBytes []byte

	if body != nil {
		var err error

		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	var (
		httpReq *http.Request
		err     error
	)
	if bodyBytes != nil {
		httpReq, err = http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(bodyBytes))
	} else {
		httpReq, err = http.NewRequestWithContext(ctx, method, targetURL, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
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
	payload := map[string]any{
		"method":      method,
		"uri":         requestURI,
		"origin":      origin,
		"destination": destServer,
	}

	if bodyBytes != nil {
		var content any
		err := json.Unmarshal(bodyBytes, &content)
		if err == nil {
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

	return httpReq, nil
}

// MakeJoin 向远程服务器请求加入事件模板.
func (c *FederationClient) MakeJoin(destServer, roomID, userID string) (map[string]any, error) {
	makeJoinURL := fmt.Sprintf("%s/_matrix/federation/v1/make_join/%s/%s?ver=1&ver=2&ver=3&ver=4&ver=5&ver=6&ver=7&ver=8&ver=9&ver=10&ver=11&ver=12",
		c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(userID))

	resp, body, err := c.doFederationRequest("GET", makeJoinURL, destServer, nil) //nolint:bodyclose // closed in doFederationRequest
	if err != nil {
		return nil, fmt.Errorf("make_join request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: resp.StatusCode, Message: string(body)}
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse make_join response: %w", err)
	}

	return result, nil
}

// SendJoin 向远程服务器发送已签名的加入事件.
func (c *FederationClient) SendJoin(destServer, roomID string, event map[string]any) (map[string]any, error) {
	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		return nil, errors.New("send_join requires a computed event reference ID")
	}

	event = maps.Clone(event)
	// Room versions 1 and 2 include the server-qualified event ID in the
	// signed event. Later versions derive it from a reference hash.
	if !strings.Contains(eventID, ":") {
		delete(event, "event_id")
	}
	// Try v2 first (returns object), fall back to v1 (may return [200, {...}])
	sendJoinURL := fmt.Sprintf("%s/_matrix/federation/v2/send_join/%s/%s",
		c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(eventID))

	resp, body, err := c.doFederationRequest("PUT", sendJoinURL, destServer, event) //nolint:bodyclose // body closed inside doFederationRequest
	if err != nil || (resp != nil && resp.StatusCode == http.StatusNotFound) {
		// Fall back to v1
		sendJoinURL = fmt.Sprintf("%s/_matrix/federation/v1/send_join/%s/%s",
			c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(eventID))
		resp, body, err = c.doFederationRequest("PUT", sendJoinURL, destServer, event) //nolint:bodyclose // closed in doFederationRequest
	}

	if err != nil {
		return nil, fmt.Errorf("send_join request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: resp.StatusCode, Message: string(body)}
	}

	// Handle v1 array format [200, {...}] and v2 object format {...}
	var result map[string]any

	if len(body) > 0 && body[0] == '[' {
		// v1 format: [200, {...}]
		var arr []json.RawMessage
		err := json.Unmarshal(body, &arr)
		if err == nil && len(arr) >= 2 {
			_ = json.Unmarshal(arr[1], &result)
		}
	} else {
		_ = json.Unmarshal(body, &result)
	}

	if result == nil {
		return nil, errors.New("empty send_join response")
	}

	return result, nil
}

// GetMissingEvents fetches missing events from a remote server.
// POST /_matrix/federation/v1/get_missing_events/{roomId}.
func (c *FederationClient) GetMissingEvents(destServer, roomID string, earliestEvents, latestEvents []string, limit int) ([]json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/_matrix/federation/v1/get_missing_events/%s",
		c.federationBaseURL(destServer), url.PathEscape(roomID))
	body := map[string]any{
		"earliest_events": earliestEvents,
		"latest_events":   latestEvents,
		"limit":           limit,
	}

	resp, respBody, err := c.doFederationRequest("POST", reqURL, destServer, body) //nolint:bodyclose
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

// FetchBackfill fetches message history from a remote server after joining a room.
func (c *FederationClient) FetchBackfill(serverName, roomID string, eventIDs []string, limit int) ([]map[string]any, error) {
	backfillURL := fmt.Sprintf("%s/_matrix/federation/v1/backfill/%s?limit=%d",
		c.federationBaseURL(serverName), url.PathEscape(roomID), limit)
	var backfillURLSb426 strings.Builder
	for _, eid := range eventIDs {
		backfillURLSb426.WriteString("&v=" + url.QueryEscape(eid))
	}
	backfillURL += backfillURLSb426.String()

	resp, body, err := c.doFederationRequest("GET", backfillURL, serverName, nil) //nolint:bodyclose
	if err != nil {
		return nil, fmt.Errorf("backfill request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backfill returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		PDUs []map[string]any `json:"pdus"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse backfill response: %w", err)
	}

	return result.PDUs, nil
}

// VerifySignature 验证事件签名.
func (c *FederationClient) VerifySignature(event map[string]any, serverName string, keyID string, signature string) bool {
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

// FetchServerKey 获取远程服务器密钥.
func (c *FederationClient) FetchServerKey(serverName string) (*ServerKeyResponse, error) {
	keyURL := c.federationBaseURL(serverName) + "/_matrix/key/v2/server"
	resp, raw, err := c.doFederationRequest("GET", keyURL, serverName, nil) //nolint:bodyclose // closed in doFederationRequest
	if err != nil {
		return nil, fmt.Errorf("failed to fetch server key: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: resp.StatusCode, Message: string(raw)}
	}
	var result ServerKeyResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result.RawJSON = append([]byte(nil), raw...)
	return &result, nil
}

// GetWellKnown 获取 .well-known 配置.
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
		return serverName + ":8448", nil //nolint:nilerr
	}

	// 解析响应
	var result struct {
		Server string `json:"m.server"`
	}
	if err := resp.Json(&result); err != nil {
		return serverName + ":8448", nil //nolint:nilerr
	}

	if result.Server == "" {
		return serverName + ":8448", nil
	}

	return result.Server, nil
}

// DownloadAndSaveMedia 下载并保存远程媒体.
// DownloadAndSaveMedia creates a local mirror with a new local MXC identity.
// It must not relabel the random local ID as a remote cache key.
func (c *FederationClient) DownloadAndSaveMedia(serverName, mediaID string) (*MatrixMedia, error) {
	media, err := c.fetchAuthenticatedMedia(serverName, mediaID, "download", "")
	if err != nil {
		return nil, err
	}
	return NewMediaService(c.hs).UploadMedia(bytes.NewReader(media.Data), &multipart.FileHeader{Filename: media.Filename, Size: int64(len(media.Data))}, media.ContentType, "")
}

type federationHTTPError struct {
	Status  int
	Message string
}

func (e *federationHTTPError) Error() string {
	return fmt.Sprintf("federation returned status %d: %s", e.Status, e.Message)
}
