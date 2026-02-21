package homeserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/url"
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
	// 使用现有密钥或生成新密钥
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)

	return &FederationClient{
		hs:         hs,
		privateKey: privateKey,
		httpClient: httpclient.NewClient(),
		keyCache:   GetKeyCache(),
	}
}

// FetchMedia 从远程服务器获取媒体
func (c *FederationClient) FetchMedia(serverName, mediaID string) ([]byte, string, error) {
	// 构建 URL
	mediaURL := fmt.Sprintf("https://%s/_matrix/media/v3/download/%s/%s", serverName, serverName, mediaID)

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
	thumbURL := fmt.Sprintf("https://%s/_matrix/media/v3/thumbnail/%s/%s?width=%d&height=%d&method=%s",
		serverName, serverName, mediaID, width, height, method)

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
	// 构建请求体
	body := map[string]interface{}{
		"origin":           c.hs.Config.ServerName,
		"origin_server_ts": time.Now().UnixMilli(),
		"pdus":             pdus,
		"edus":             edus,
	}

	// 构建 URL
	txnURL := fmt.Sprintf("https://%s/_matrix/federation/v1/send/%s", destServer, txnID)

	// 创建请求
	req := c.httpClient.NewRequest("PUT", txnURL)
	req.SetJsonBody(body)
	req.SetHeader("Host", destServer)
	req.SetTimeout(60)

	// 添加签名
	c.signRequest(req, destServer)

	// 发送请求
	_, err := req.Do()
	return err
}

// QueryDirectory 查询远程目录
func (c *FederationClient) QueryDirectory(destServer, roomAlias string) (*QueryDirectoryResponse, error) {
	// 构建 URL
	queryURL := fmt.Sprintf("https://%s/_matrix/federation/v1/query/directory?room_alias=%s",
		destServer, url.QueryEscape(roomAlias))

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

// signRequest 签名请求
func (c *FederationClient) signRequest(req *httpclient.Request, destServer string) {
	// 生成 X-Matrix 签名头
	ts := time.Now().UnixMilli()
	origin := c.hs.Config.ServerName

	// 签名内容
	sigContent := fmt.Sprintf("%s\n%d\n%s\n%s\n%s", "", ts, origin, destServer, "")
	hash := sha256.Sum256([]byte(sigContent))
	signature := ed25519.Sign(c.privateKey, hash[:])
	sigB64 := base64.RawStdEncoding.EncodeToString(signature)

	// 构建签名头
	keyID := fmt.Sprintf("ed25519:1")
	authHeader := fmt.Sprintf("X-Matrix origin=%s,key_id=\"%s\",sig=\"%s\"", origin, keyID, sigB64)

	req.SetHeader("Authorization", authHeader)
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
	canonicalJSON, err := CanonicalJSON(event)
	if err != nil {
		log.Error("Failed to canonicalize JSON: %v", err)
		return false
	}

	hash := sha256.Sum256(canonicalJSON)

	// 验证签名
	return ed25519.Verify(publicKey, hash[:], signatureBytes)
}

// FetchServerKey 获取远程服务器密钥
func (c *FederationClient) FetchServerKey(serverName string) (*ServerKeyResponse, error) {
	// 构建 URL
	keyURL := fmt.Sprintf("https://%s/_matrix/key/v2/server", serverName)

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
