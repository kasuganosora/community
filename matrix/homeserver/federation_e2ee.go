package homeserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

// ===== 联邦 E2EE 端点 =====

// RegisterFederationE2EERoutes 注册联邦 E2EE 路由
func RegisterFederationE2EERoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	fed := r.Group("/_matrix/federation/v1")
	{
		// 设备密钥查询 - 联邦服务器查询本服务器用户的设备密钥
		fed.POST("/user/keys/query", handleFederationQueryKeys)
		fed.POST("/user/keys/claim", handleFederationClaimKeys)

		// To-Device 消息 - 联邦服务器发送加密消息到本服务器用户
		fed.PUT("/send_to_device/:eventType/:txnId", handleFederationSendToDevice)
	}
}

// ===== 设备密钥查询 =====

// FederationQueryKeysRequest 联邦密钥查询请求
type FederationQueryKeysRequest struct {
	DeviceKeys map[string][]string `json:"device_keys"` // user_id -> device_ids
}

// FederationQueryKeysResponse 联邦密钥查询响应
type FederationQueryKeysResponse struct {
	DeviceKeys      map[string]map[string]*DeviceKeys `json:"device_keys"`
	MasterKeys      map[string]*CrossSigningKey       `json:"master_keys,omitempty"`
	SelfSigningKeys map[string]*CrossSigningKey       `json:"self_signing_keys,omitempty"`
	UserSigningKeys map[string]*CrossSigningKey       `json:"user_signing_keys,omitempty"`
}

// handleFederationQueryKeys 处理联邦设备密钥查询
// POST /_matrix/federation/v1/user/keys/query
func handleFederationQueryKeys(c *gin.Context) {
	hs := GetHomeServer()

	// 解析请求
	var req FederationQueryKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_BAD_JSON",
			Error:   "Invalid JSON: " + err.Error(),
		})
		return
	}

	// 验证并过滤只查询本服务器的用户
	filteredReq := make(map[string][]string)
	for userID, deviceIDs := range req.DeviceKeys {
		// 解析用户 ID 获取服务器名
		parts := strings.Split(userID, ":")
		if len(parts) != 2 {
			continue
		}
		serverName := parts[1]

		// 只处理本服务器的用户
		if serverName == hs.Config.ServerName {
			filteredReq[userID] = deviceIDs
		}
	}

	// 查询设备密钥
	queryReq := &QueryKeysRequest{
		DeviceKeys: filteredReq,
	}
	queryResp, err := hs.KeyStore.QueryDeviceKeys(queryReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, MatrixError{
			ErrCode: "M_INTERNAL_ERROR",
			Error:   "Failed to query keys: " + err.Error(),
		})
		return
	}

	// 构建响应
	resp := FederationQueryKeysResponse{
		DeviceKeys:      queryResp.DeviceKeys,
		MasterKeys:      make(map[string]*CrossSigningKey),
		SelfSigningKeys: make(map[string]*CrossSigningKey),
		UserSigningKeys: make(map[string]*CrossSigningKey),
	}

	// 查询交叉签名密钥
	for userID := range filteredReq {
		var crossSigningKeys []MatrixCrossSigningKey
		db.GetDB().Where("user_id = ?", userID).Find(&crossSigningKeys)

		for _, key := range crossSigningKeys {
			csk := &CrossSigningKey{
				UserID:     key.UserID,
				Usage:      []string{key.KeyType},
				Keys:       make(map[string]string),
				Signatures: make(map[string]map[string]string),
			}

			// 解析密钥数据
			var keyData map[string]interface{}
			if err := json.Unmarshal([]byte(key.KeyData), &keyData); err == nil {
				if keys, ok := keyData["keys"].(map[string]interface{}); ok {
					for k, v := range keys {
						if str, ok := v.(string); ok {
							csk.Keys[k] = str
						}
					}
				}
				if sigs, ok := keyData["signatures"].(map[string]interface{}); ok {
					for domain, domainSigs := range sigs {
						if domainSigMap, ok := domainSigs.(map[string]interface{}); ok {
							csk.Signatures[domain] = make(map[string]string)
							for k, v := range domainSigMap {
								if str, ok := v.(string); ok {
									csk.Signatures[domain][k] = str
								}
							}
						}
					}
				}
			}

			switch key.KeyType {
			case "master":
				resp.MasterKeys[userID] = csk
			case "self_signing":
				resp.SelfSigningKeys[userID] = csk
			case "user_signing":
				resp.UserSigningKeys[userID] = csk
			}
		}
	}

	c.JSON(http.StatusOK, resp)
}

// ===== 一次性密钥认领 =====

// FederationClaimKeysRequest 联邦密钥认领请求
type FederationClaimKeysRequest struct {
	OneTimeKeys map[string]map[string]string `json:"one_time_keys"` // user_id -> device_id -> algorithm
}

// FederationClaimKeysResponse 联邦密钥认领响应
type FederationClaimKeysResponse struct {
	OneTimeKeys map[string]map[string]map[string]string `json:"one_time_keys"`
}

// handleFederationClaimKeys 处理联邦一次性密钥认领
// POST /_matrix/federation/v1/user/keys/claim
func handleFederationClaimKeys(c *gin.Context) {
	hs := GetHomeServer()

	// 解析请求
	var req FederationClaimKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_BAD_JSON",
			Error:   "Invalid JSON: " + err.Error(),
		})
		return
	}

	// 验证并过滤只认领本服务器的用户
	filteredReq := make(map[string]map[string]string)
	for userID, devices := range req.OneTimeKeys {
		// 解析用户 ID 获取服务器名
		parts := strings.Split(userID, ":")
		if len(parts) != 2 {
			continue
		}
		serverName := parts[1]

		// 只处理本服务器的用户
		if serverName == hs.Config.ServerName {
			filteredReq[userID] = devices
		}
	}

	// 认领密钥
	claimReq := &ClaimKeysRequest{
		OneTimeKeys: filteredReq,
	}
	claimResp, err := hs.KeyStore.ClaimOneTimeKeys(claimReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, MatrixError{
			ErrCode: "M_INTERNAL_ERROR",
			Error:   "Failed to claim keys: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, FederationClaimKeysResponse{
		OneTimeKeys: claimResp.OneTimeKeys,
	})
}

// ===== To-Device 消息联邦传输 =====

// FederationSendToDeviceRequest 联邦 To-Device 消息请求
type FederationSendToDeviceRequest struct {
	Messages map[string]map[string]map[string]interface{} `json:"messages"` // user_id -> device_id -> content
	Sender   string                                       `json:"sender"`
}

// handleFederationSendToDevice 处理联邦 To-Device 消息发送
// PUT /_matrix/federation/v1/send_to_device/:eventType/:txnId
func handleFederationSendToDevice(c *gin.Context) {
	hs := GetHomeServer()
	eventType := c.Param("eventType")
	txnID := c.Param("txnId")

	// 检查事务 ID 是否已处理（去重）
	var existingTxn MatrixFederationTxn
	if err := db.GetDB().Where("txn_id = ? AND origin = ?", txnID, c.GetString("origin")).First(&existingTxn).Error; err == nil {
		// 事务已处理，返回成功（幂等）
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// 解析请求
	var req FederationSendToDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, MatrixError{
			ErrCode: "M_BAD_JSON",
			Error:   "Invalid JSON: " + err.Error(),
		})
		return
	}

	// 处理消息
	processedCount := 0
	for userID, devices := range req.Messages {
		// 验证用户是否属于本服务器
		parts := strings.Split(userID, ":")
		if len(parts) != 2 || parts[1] != hs.Config.ServerName {
			continue
		}

		for deviceID, content := range devices {
			// 存储 To-Device 消息
			msg := &MatrixToDeviceMessage{
				Sender:       req.Sender,
				SenderDevice: "", // 联邦发送者设备 ID 通常在 content 中
				TargetUser:   userID,
				TargetDevice: deviceID,
				EventType:    eventType,
				Content:      content,
			}

			if err := db.GetDB().Create(msg).Error; err != nil {
				continue
			}
			processedCount++

			// 触发同步通知
			hs.NotifyToDeviceUpdate(userID, deviceID)
		}
	}

	// 记录事务（幂等）
	txn := &MatrixFederationTxn{
		TxnID:     txnID,
		Origin:    c.GetString("origin"),
		PDUs:      json.RawMessage("[]"),
		EDUs:      json.RawMessage("[]"),
		Processed: true,
	}
	db.GetDB().Create(txn)

	c.JSON(http.StatusOK, gin.H{
		"processed_count": processedCount,
	})
}
