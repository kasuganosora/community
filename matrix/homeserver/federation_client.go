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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/httpclient"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
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
	return c.QueryDirectoryContext(context.Background(), destServer, roomAlias)
}

// QueryDirectoryContext is QueryDirectory bound to ctx.
func (c *FederationClient) QueryDirectoryContext(ctx context.Context, destServer, roomAlias string) (*QueryDirectoryResponse, error) {
	queryURL := fmt.Sprintf("%s/_matrix/federation/v1/query/directory?room_alias=%s",
		c.federationBaseURL(destServer), url.QueryEscape(roomAlias))
	resp, body, err := c.doFederationRequestContext(ctx, "GET", queryURL, destServer, nil, 1<<20) //nolint:bodyclose // closed in doFederationRequest
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

	// No client-level Timeout: deadlines come from the request context (see
	// doFederationRequestContext) so a join can give send_join more time than a
	// key fetch. The discovery transport is shared by every FederationClient so
	// its .well-known/SRV resolutions and negative entries are actually reused.
	c.fedHTTPClient = &http.Client{
		// Safety net only; real deadlines come from the request context.
		Timeout:   federationClientSafetyTimeout,
		Transport: sharedFederationDiscoveryTransport(tlsConfig),
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

// defaultFederationRequestTimeout bounds a federation request whose caller
// supplied no deadline of its own.
const defaultFederationRequestTimeout = 30 * time.Second

// federationClientSafetyTimeout is the http.Client-level backstop. Contexts
// (10s make_join, 20s send_join, 8s key fetches, 30s default) are what
// normally end a request.
const federationClientSafetyTimeout = 5 * time.Minute

// defaultFederationBodyLimit caps ordinary federation responses (send_join
// has its own, larger cap). Before this the whole body was buffered unbounded.
const defaultFederationBodyLimit int64 = 32 << 20

var errFederationResponseTooLarge = errors.New("federation response too large")

// doFederationRequest 执行联邦 HTTP 请求，允许接受非 2xx 状态码.
func (c *FederationClient) doFederationRequest(method, targetURL, destServer string, body any) (*http.Response, []byte, error) {
	return c.doFederationRequestContext(context.Background(), method, targetURL, destServer, body, 0)
}

// doFederationRequestContext is doFederationRequest bound to ctx, with the
// response body capped at limit bytes (<=0: defaultFederationBodyLimit).
func (c *FederationClient) doFederationRequestContext(ctx context.Context, method, targetURL, destServer string, body any, limit int64) (*http.Response, []byte, error) {
	var buf bytes.Buffer
	resp, err := c.doFederationStream(ctx, method, targetURL, destServer, body, limit, func(_ *http.Response, r io.Reader) error {
		_, err := buf.ReadFrom(r)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return resp, buf.Bytes(), nil
}

// doFederationStream sends a signed request and hands the (size-limited)
// response body to consume. A body longer than limit makes consume's reader
// fail with errFederationResponseTooLarge instead of exhausting memory.
func (c *FederationClient) doFederationStream(ctx context.Context, method, targetURL, destServer string, body any, limit int64, consume func(*http.Response, io.Reader) error) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultFederationRequestTimeout)
		defer cancel()
	}
	httpReq, err := c.prepareSignedFederationRequest(ctx, method, targetURL, destServer, body)
	if err != nil {
		return nil, err
	}
	parsedURL := httpReq.URL

	client := c.federationHTTPClient()
	if strings.HasPrefix(parsedURL.Path, "/_matrix/federation/v1/media/") {
		client = c.federationMediaHTTPClient()
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, &federationTransportError{err: err}
	}

	defer func() { _ = resp.Body.Close() }()

	if limit <= 0 {
		limit = defaultFederationBodyLimit
	}
	var responseReader io.Reader = &limitedBody{r: resp.Body, remaining: limit}
	if strings.Contains(parsedURL.Path, "/federation/v1/media/") {
		responseReader = io.LimitReader(resp.Body, matrixMediaLimit+(1<<20))
	}
	if err := consume(resp, responseReader); err != nil {
		if errors.Is(err, errFederationResponseTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return resp, nil
}

// limitedBody fails (rather than silently truncating) once more than
// `remaining` bytes have been read, so an oversized response is never parsed
// as if it were complete.
type limitedBody struct {
	r         io.Reader
	remaining int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, errFederationResponseTooLarge
	}
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return n, errFederationResponseTooLarge
	}
	return n, err
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

// makeJoinVersionQuery lists every room version the vendored library can
// parse and verify (stable, numeric: 1..12 today), so the remote picks the
// room's real version instead of answering M_INCOMPATIBLE_ROOM_VERSION.
func makeJoinVersionQuery() string {
	var versions []int
	for id, impl := range gmsl.RoomVersions() {
		if !impl.Stable() {
			continue
		}
		if n, err := strconv.Atoi(string(id)); err == nil {
			versions = append(versions, n)
		}
	}
	sort.Ints(versions)
	parts := make([]string, len(versions))
	for i, v := range versions {
		parts[i] = "ver=" + strconv.Itoa(v)
	}
	return strings.Join(parts, "&")
}

// MakeJoin 向远程服务器请求加入事件模板.
func (c *FederationClient) MakeJoin(destServer, roomID, userID string) (map[string]any, error) {
	return c.MakeJoinContext(context.Background(), destServer, roomID, userID)
}

// MakeJoinContext is MakeJoin bound to ctx.
func (c *FederationClient) MakeJoinContext(ctx context.Context, destServer, roomID, userID string) (map[string]any, error) {
	makeJoinURL := fmt.Sprintf("%s/_matrix/federation/v1/make_join/%s/%s?%s",
		c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(userID), makeJoinVersionQuery())

	resp, body, err := c.doFederationRequestContext(ctx, "GET", makeJoinURL, destServer, nil, 1<<20) //nolint:bodyclose // closed in doFederationRequest
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
	res, err := c.SendJoinContext(context.Background(), destServer, roomID, event, 0)
	if err != nil {
		return nil, err
	}
	return res.asMap()
}

// SendJoinContext sends the signed join event and decodes the (potentially
// huge) state response incrementally: only one event's bytes are buffered
// beyond the retained raw events, and the whole body is capped at maxBytes
// (<=0: sendJoinBodyLimit()).
func (c *FederationClient) SendJoinContext(ctx context.Context, destServer, roomID string, event map[string]any, maxBytes int64) (*sendJoinResponse, error) {
	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		return nil, errors.New("send_join requires a computed event reference ID")
	}
	if maxBytes <= 0 {
		maxBytes = sendJoinBodyLimit()
	}

	event = maps.Clone(event)
	// Room versions 1 and 2 include the server-qualified event ID in the
	// signed event. Later versions derive it from a reference hash.
	if !strings.Contains(eventID, ":") {
		delete(event, "event_id")
	}
	do := func(version string) (*sendJoinResponse, int, string, error) {
		sendJoinURL := fmt.Sprintf("%s/_matrix/federation/%s/send_join/%s/%s",
			c.federationBaseURL(destServer), version, url.PathEscape(roomID), url.PathEscape(eventID))
		var result *sendJoinResponse
		status := 0
		var errBody string
		_, err := c.doFederationStream(ctx, "PUT", sendJoinURL, destServer, event, maxBytes, func(resp *http.Response, r io.Reader) error {
			status = resp.StatusCode
			if resp.StatusCode != http.StatusOK {
				b, err := io.ReadAll(io.LimitReader(r, 64<<10))
				errBody = string(b)
				return err
			}
			var err error
			result, err = decodeSendJoinResponse(r)
			return err
		})
		return result, status, errBody, err
	}
	// Try v2 first (returns object), fall back to v1 (may return [200, {...}])
	result, status, errBody, err := do("v2")
	if err != nil && ctx.Err() == nil && !errors.Is(err, errFederationResponseTooLarge) || status == http.StatusNotFound {
		result, status, errBody, err = do("v1")
	}
	if err != nil {
		return nil, fmt.Errorf("send_join request failed: %w", err)
	}
	if status != http.StatusOK {
		return nil, &federationHTTPError{Status: status, Message: errBody}
	}
	if result == nil {
		return nil, errors.New("empty send_join response")
	}
	return result, nil
}

// GetStateContext fetches the room state (and its auth chain) at eventID:
// GET /_matrix/federation/v1/state/{roomId}?event_id=... The answer is read
// with the same event-count cap as send_join. The "pdus" list is returned in
// the State field.
func (c *FederationClient) GetStateContext(ctx context.Context, destServer, roomID, eventID string, maxBytes int64) (*sendJoinResponse, error) {
	if maxBytes <= 0 {
		maxBytes = sendJoinBodyLimit()
	}
	stateURL := fmt.Sprintf("%s/_matrix/federation/v1/state/%s?event_id=%s",
		c.federationBaseURL(destServer), url.PathEscape(roomID), url.QueryEscape(eventID))
	var result *sendJoinResponse
	status := 0
	var errBody string
	_, err := c.doFederationStream(ctx, "GET", stateURL, destServer, nil, maxBytes, func(resp *http.Response, r io.Reader) error {
		status = resp.StatusCode
		if resp.StatusCode != http.StatusOK {
			b, err := io.ReadAll(io.LimitReader(r, 64<<10))
			errBody = string(b)
			return err
		}
		var err error
		result, err = decodeSendJoinResponse(r)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("state request failed: %w", err)
	}
	if status != http.StatusOK {
		return nil, &federationHTTPError{Status: status, Message: errBody}
	}
	if result == nil {
		return nil, errors.New("empty state response")
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

		if err := verifyDirectKeyDocument(keyResp, serverName); err != nil {
			return false
		}
		cacheKeyDocument(serverName, keyResp)
		publicKey, found = c.keyCache.Get(serverName, keyID)
		if !found {
			return false
		}
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
	return c.FetchServerKeyContext(context.Background(), serverName)
}

// FetchServerKeyContext is FetchServerKey bound to ctx (1 MiB response cap).
func (c *FederationClient) FetchServerKeyContext(ctx context.Context, serverName string) (*ServerKeyResponse, error) {
	keyURL := c.federationBaseURL(serverName) + "/_matrix/key/v2/server"
	resp, raw, err := c.doFederationRequestContext(ctx, "GET", keyURL, serverName, nil, 1<<20) //nolint:bodyclose // closed in doFederationRequest
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
