package homeserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/awnumar/memguard"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// KeyStore 设备密钥存储服务
type KeyStore struct {
	hs          *HomeServer
	masterKey   *memguard.Enclave // 主密钥（加密存储）
	deviceKeys  sync.Map          // 设备公钥缓存
	otkCache    sync.Map          // 一次性密钥缓存
}

// NewKeyStore 创建密钥存储服务
func NewKeyStore(hs *HomeServer) *KeyStore {
	ks := &KeyStore{
		hs: hs,
	}
	
	// 加载或生成主密钥
	if err := ks.loadOrGenerateMasterKey(); err != nil {
		log.Error("Failed to load master key: %v", err)
	}
	
	return ks
}

// DeviceKeys 设备密钥信息
type DeviceKeys struct {
	UserID      string            `json:"user_id"`
	DeviceID    string            `json:"device_id"`
	Algorithms  []string          `json:"algorithms"`
	Keys        map[string]string `json:"keys"`
	Signatures  map[string]map[string]string `json:"signatures"`
	Unsigned    map[string]interface{} `json:"unsigned,omitempty"`
}

// OneTimeKeys 一次性密钥
type OneTimeKeys struct {
	KeyID       string `json:"key_id"`
	Algorithm   string `json:"algorithm"`
	PublicKey   string `json:"public_key"`
	PrivateKey  *memguard.Enclave `json:"-"` // 加密存储私钥
	CreatedAt   time.Time `json:"created_at"`
	Used        bool      `json:"used"`
}

// UploadKeysRequest 上传密钥请求
type UploadKeysRequest struct {
	DeviceKeys     *DeviceKeys                  `json:"device_keys,omitempty"`
	OneTimeKeys    map[string]json.RawMessage   `json:"one_time_keys,omitempty"`
	OTKCount       map[string]int               `json:"one_time_key_counts,omitempty"`
	FallbackKeys   map[string]json.RawMessage   `json:"fallback_keys,omitempty"`
}

// UploadKeysResponse 上传密钥响应
type UploadKeysResponse struct {
	OneTimeKeyCounts map[string]int `json:"one_time_key_counts"`
}

// QueryKeysRequest 查询密钥请求
type QueryKeysRequest struct {
	DeviceKeys map[string][]string `json:"device_keys"` // user_id -> device_ids
	Timeout    int64               `json:"timeout,omitempty"`
	Token      string              `json:"token,omitempty"`
}

// QueryKeysResponse 查询密钥响应
type QueryKeysResponse struct {
	DeviceKeys  map[string]map[string]*DeviceKeys `json:"device_keys"`
	Failures    map[string]interface{}          `json:"failures,omitempty"`
	MasterKeys  map[string]*CrossSigningKey     `json:"master_keys,omitempty"`
	SelfSigningKeys map[string]*CrossSigningKey `json:"self_signing_keys,omitempty"`
	UserSigningKeys map[string]*CrossSigningKey `json:"user_signing_keys,omitempty"`
}

// ClaimKeysRequest 认领密钥请求
type ClaimKeysRequest struct {
	OneTimeKeys map[string]map[string]string `json:"one_time_keys"` // user_id -> device_id -> algorithm
	Timeout     int64                        `json:"timeout,omitempty"`
}

// ClaimKeysResponse 认领密钥响应
type ClaimKeysResponse struct {
	OneTimeKeys map[string]map[string]map[string]json.RawMessage `json:"one_time_keys"`
	Failures    map[string]interface{}                            `json:"failures,omitempty"`
}

// CrossSigningKey 交叉签名密钥
type CrossSigningKey struct {
	UserID      string            `json:"user_id"`
	Usage       []string          `json:"usage"` // master, self_signing, user_signing
	Keys        map[string]string `json:"keys"`
	Signatures  map[string]map[string]string `json:"signatures"`
	PrivateKey  *memguard.Enclave `json:"-"` // 仅在内存中，不序列化
}

// loadOrGenerateMasterKey 加载或生成主密钥
func (ks *KeyStore) loadOrGenerateMasterKey() error {
	// 从数据库加载
	var keyRecord MatrixFederationKey
	result := db.GetDB().Where("key_type = ?", "master").First(&keyRecord)
	
	if result.Error == nil {
		// 解密并加载主密钥
		privateKeyData, err := base64.RawStdEncoding.DecodeString(keyRecord.PrivateKey)
		if err != nil {
			return fmt.Errorf("failed to decode master key: %w", err)
		}
		
		// 创建安全存储
		ks.masterKey = memguard.NewEnclave(privateKeyData)
		memguard.WipeBytes(privateKeyData)
		
		return nil
	}
	
	// 生成新的主密钥
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate master key: %w", err)
	}
	
	// 安全存储
	ks.masterKey = memguard.NewEnclave(privateKey)
	
	// 保存到数据库
	publicKey := base64.RawStdEncoding.EncodeToString(privateKey[32:])
	privateKeyEncoded := base64.RawStdEncoding.EncodeToString(privateKey)
	
	keyRecord = MatrixFederationKey{
		KeyID:      "ed25519:master:" + publicKey[:8],
		KeyType:    "master",
		PublicKey:  publicKey,
		PrivateKey: privateKeyEncoded,
		IsActive:   true,
	}
	
	if err := db.GetDB().Create(&keyRecord).Error; err != nil {
		return fmt.Errorf("failed to save master key: %w", err)
	}
	
	memguard.WipeBytes(privateKey)
	
	return nil
}

// UploadDeviceKeys 上传设备密钥
func (ks *KeyStore) UploadDeviceKeys(userID, deviceID string, req *UploadKeysRequest) (*UploadKeysResponse, error) {
	// device_keys is optional (client may upload only OTKs)
	if req.DeviceKeys != nil {
		// 保存设备密钥
		deviceKeyJSON, err := json.Marshal(req.DeviceKeys)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal device keys: %w", err)
		}

		record := &MatrixDeviceKey{
			UserID:     userID,
			DeviceID:   deviceID,
			DeviceKey:  string(deviceKeyJSON),
			Algorithms: jsonStringSlice(req.DeviceKeys.Algorithms),
			UpdatedAt:  time.Now(),
		}

		// 使用 Upsert
		if err := db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).
			Assign(record).FirstOrCreate(&MatrixDeviceKey{}).Error; err != nil {
			return nil, fmt.Errorf("failed to save device keys: %w", err)
		}

		// 更新缓存
		ks.deviceKeys.Store(userID+":"+deviceID, req.DeviceKeys)

		// Record device list change so it appears in device_lists.changed in sync
		if ks.hs.DeviceList != nil {
			ks.hs.DeviceList.RecordDeviceChange(userID, deviceID, "changed")
		}
	}
	
	// 处理一次性密钥
	counts := make(map[string]int)
	if len(req.OneTimeKeys) > 0 {
		for keyID, rawKey := range req.OneTimeKeys {
			algorithm, keyIDClean := parseKeyID(keyID)

			// 提取公钥字符串：curve25519 是字符串，signed_curve25519 是对象 {"key":"...","signatures":{...}}
			var publicKeyStr string
			var keyJSON string = string(rawKey)
			if len(rawKey) > 0 && rawKey[0] == '"' {
				// String value — curve25519
				json.Unmarshal(rawKey, &publicKeyStr)
			} else {
				// Object value — signed_curve25519: extract "key" field
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(rawKey, &obj); err == nil {
					if keyVal, ok := obj["key"]; ok {
						json.Unmarshal(keyVal, &publicKeyStr)
					}
				}
				keyJSON = string(rawKey) // store full JSON for signed keys
			}
			if publicKeyStr == "" {
				continue
			}

			// Check if this OTK already exists (idempotent upload)
			var existing MatrixOneTimeKey
			if err := db.GetDB().Where("user_id = ? AND device_id = ? AND algorithm = ? AND key_id = ?",
				userID, deviceID, algorithm, keyIDClean).First(&existing).Error; err == nil {
				// Already exists — skip to avoid duplicates
				continue
			}

			// 保存到数据库
			otkRecord := &MatrixOneTimeKey{
				UserID:    userID,
				DeviceID:  deviceID,
				KeyID:     keyIDClean,
				Algorithm: algorithm,
				PublicKey: keyJSON, // store original (string or object) for claim response
				CreatedAt: time.Now(),
			}

			db.GetDB().Create(otkRecord)
			counts[algorithm]++
		}
	}
	
	// 查询当前存储的 OTK 总数（按算法分类）
	var otkRecords []MatrixOneTimeKey
	db.GetDB().Where("user_id = ? AND device_id = ? AND used = ?", userID, deviceID, false).Find(&otkRecords)
	totalCounts := make(map[string]int)
	for _, otk := range otkRecords {
		totalCounts[otk.Algorithm]++
	}
	// 如果没有查到任何记录，使用上传时的 counts
	if len(otkRecords) == 0 && len(counts) > 0 {
		totalCounts = counts
	}

	return &UploadKeysResponse{
		OneTimeKeyCounts: totalCounts,
	}, nil
}

// QueryDeviceKeys 查询设备密钥
func (ks *KeyStore) QueryDeviceKeys(req *QueryKeysRequest) (*QueryKeysResponse, error) {
	resp := &QueryKeysResponse{
		DeviceKeys: make(map[string]map[string]*DeviceKeys),
		Failures:   make(map[string]interface{}),
	}
	
	for userID, deviceIDs := range req.DeviceKeys {
		deviceKeys := make(map[string]*DeviceKeys)
		
		// 如果 deviceIDs 为空，查询所有设备
		if len(deviceIDs) == 0 {
			var records []MatrixDeviceKey
			db.GetDB().Where("user_id = ?", userID).Find(&records)
			
			for _, record := range records {
				var dk DeviceKeys
				if err := json.Unmarshal([]byte(record.DeviceKey), &dk); err == nil {
					// 添加服务器签名
					if dk.Signatures == nil {
						dk.Signatures = make(map[string]map[string]string)
					}
					
					deviceKeys[record.DeviceID] = &dk
				}
			}
		} else {
			// 查询指定设备
			for _, deviceID := range deviceIDs {
				// 先查缓存
				if cached, ok := ks.deviceKeys.Load(userID + ":" + deviceID); ok {
					deviceKeys[deviceID] = cached.(*DeviceKeys)
					continue
				}
				
				// 查数据库
				var record MatrixDeviceKey
				if err := db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).First(&record).Error; err == nil {
					var dk DeviceKeys
					if err := json.Unmarshal([]byte(record.DeviceKey), &dk); err == nil {
						deviceKeys[deviceID] = &dk
						// 更新缓存
						ks.deviceKeys.Store(userID+":"+deviceID, &dk)
					}
				}
			}
		}
		
		// Always include the user in the response, even with no keys (spec requirement)
		resp.DeviceKeys[userID] = deviceKeys
	}
	
	return resp, nil
}

// ClaimOneTimeKeys 认领一次性密钥
func (ks *KeyStore) ClaimOneTimeKeys(req *ClaimKeysRequest) (*ClaimKeysResponse, error) {
	resp := &ClaimKeysResponse{
		OneTimeKeys: make(map[string]map[string]map[string]json.RawMessage),
		Failures:    make(map[string]interface{}),
	}

	for userID, devices := range req.OneTimeKeys {
		deviceKeys := make(map[string]map[string]json.RawMessage)

		for deviceID, algorithm := range devices {
			// 查找未使用的一次性密钥
			var otkRecord MatrixOneTimeKey
			result := db.GetDB().Where("user_id = ? AND device_id = ? AND algorithm = ? AND used = ?",
				userID, deviceID, algorithm, false).
				Order("created_at ASC").
				First(&otkRecord)

			if result.Error != nil {
				continue // 没有可用密钥
			}

			// 标记为已使用
			db.GetDB().Model(&otkRecord).Update("used", true)

			// 返回公钥 — PublicKey stores either a JSON string "base64..." or a JSON object {...}
			if deviceKeys[deviceID] == nil {
				deviceKeys[deviceID] = make(map[string]json.RawMessage)
			}
			deviceKeys[deviceID][algorithm+":"+otkRecord.KeyID] = json.RawMessage(otkRecord.PublicKey)
		}

		if len(deviceKeys) > 0 {
			resp.OneTimeKeys[userID] = deviceKeys
		}
	}
	
	return resp, nil
}

// GetOneTimeKeysCount 获取一次性密钥数量
func (ks *KeyStore) GetOneTimeKeysCount(userID, deviceID string) map[string]int {
	counts := make(map[string]int)
	
	var results []struct {
		Algorithm string
		Count     int64
	}
	
	db.GetDB().Model(&MatrixOneTimeKey{}).
		Select("algorithm, COUNT(*) as count").
		Where("user_id = ? AND device_id = ? AND used = ?", userID, deviceID, false).
		Group("algorithm").
		Scan(&results)
	
	for _, r := range results {
		counts[r.Algorithm] = int(r.Count)
	}
	
	// 确保返回所有算法的计数
	if _, ok := counts["signed_curve25519"]; !ok {
		counts["signed_curve25519"] = 0
	}
	if _, ok := counts["curve25519"]; !ok {
		counts["curve25519"] = 0
	}
	
	return counts
}

// verifyDeviceKeySignature 验证设备密钥签名
func (ks *KeyStore) verifyDeviceKeySignature(dk *DeviceKeys) error {
	// 提取设备密钥
	deviceKeyID := fmt.Sprintf("ed25519:%s", dk.DeviceID)
	devicePublicKey, ok := dk.Keys[deviceKeyID]
	if !ok {
		return fmt.Errorf("device key not found")
	}

	// 解码公钥
	publicKeyBytes, err := base64.RawStdEncoding.DecodeString(devicePublicKey)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	// 验证自签名
	signatures, ok := dk.Signatures[dk.UserID]
	if !ok {
		return fmt.Errorf("missing signatures")
	}

	signature, ok := signatures[deviceKeyID]
	if !ok {
		return fmt.Errorf("missing device key signature")
	}

	signatureBytes, err := base64.RawStdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	// 创建不含签名和 unsigned 的副本，转换为 map 后用 CanonicalJSON 序列化
	// Matrix spec 要求使用 canonical JSON 进行签名验证
	dkBytes, err := json.Marshal(dk)
	if err != nil {
		return fmt.Errorf("failed to marshal device keys: %w", err)
	}
	var dkMap map[string]interface{}
	if err := json.Unmarshal(dkBytes, &dkMap); err != nil {
		return fmt.Errorf("failed to unmarshal device keys: %w", err)
	}
	delete(dkMap, "signatures")
	delete(dkMap, "unsigned")

	data, err := CanonicalJSON(dkMap)
	if err != nil {
		return fmt.Errorf("failed to canonicalize device keys: %w", err)
	}

	if !ed25519.Verify(publicKeyBytes, data, signatureBytes) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// signDeviceKey 为设备密钥添加服务器签名
func (ks *KeyStore) signDeviceKey(dk *DeviceKeys) error {
	if ks.masterKey == nil {
		return fmt.Errorf("master key not available")
	}
	
	// 获取私钥
	privateKeyBuffer, err := ks.masterKey.Open()
	if err != nil {
		return fmt.Errorf("failed to unlock master key: %w", err)
	}
	defer privateKeyBuffer.Destroy()
	
	privateKey := privateKeyBuffer.Bytes()
	
	// 创建不含服务器签名的副本
	dkCopy := *dk
	if dkCopy.Signatures == nil {
		dkCopy.Signatures = make(map[string]map[string]string)
	}
	delete(dkCopy.Signatures, ks.hs.Config.ServerName)
	
	data, _ := json.Marshal(dkCopy)
	
	// 签名
	signature := ed25519.Sign(privateKey, data)
	
	// 添加签名
	keyID := "ed25519:" + base64.RawStdEncoding.EncodeToString(privateKey[32:])[:16]
	if dk.Signatures[ks.hs.Config.ServerName] == nil {
		dk.Signatures[ks.hs.Config.ServerName] = make(map[string]string)
	}
	dk.Signatures[ks.hs.Config.ServerName][keyID] = base64.RawStdEncoding.EncodeToString(signature)
	
	return nil
}

// parseKeyID 解析密钥 ID
func parseKeyID(keyID string) (algorithm, cleanID string) {
	// 格式: "algorithm:key_id" 或 "signed_curve25519:AAAAFw"
	for i, c := range keyID {
		if c == ':' {
			return keyID[:i], keyID[i+1:]
		}
	}
	return keyID, ""
}

// jsonStringSlice 将字符串切片转换为 JSON
func jsonStringSlice(s []string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// GenerateDeviceKeys 为新设备生成密钥对
func (ks *KeyStore) GenerateDeviceKeys(userID, deviceID string) (*DeviceKeys, error) {
	// 生成 Ed25519 签名密钥对
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	defer memguard.WipeBytes(privateKey)
	
	publicKey := privateKey[32:]
	keyID := fmt.Sprintf("ed25519:%s", deviceID)
	
	dk := &DeviceKeys{
		UserID:     userID,
		DeviceID:   deviceID,
		Algorithms: []string{"ed25519", "curve25519"},
		Keys: map[string]string{
			keyID: base64.RawStdEncoding.EncodeToString(publicKey),
		},
		Signatures: make(map[string]map[string]string),
	}
	
	// 自签名
	data, _ := json.Marshal(dk)
	signature := ed25519.Sign(privateKey, data)
	
	dk.Signatures[userID] = map[string]string{
		keyID: base64.RawStdEncoding.EncodeToString(signature),
	}
	
	// 添加服务器签名
	if err := ks.signDeviceKey(dk); err != nil {
		log.Warn("Failed to sign device key: %v", err)
	}
	
	return dk, nil
}

// MatrixDeviceKey 设备密钥数据库模型
type MatrixDeviceKey struct {
	ID         uint64    `gorm:"primaryKey"`
	UserID     string    `gorm:"index;type:varchar(255);not null"`
	DeviceID   string    `gorm:"index;type:varchar(255);not null"`
	DeviceKey  string    `gorm:"type:text"`
	Algorithms string    `gorm:"type:text"`
	UpdatedAt  time.Time
}

func (MatrixDeviceKey) TableName() string {
	return "matrix_device_keys"
}

// MatrixOneTimeKey 一次性密钥数据库模型
type MatrixOneTimeKey struct {
	ID         uint64    `gorm:"primaryKey"`
	UserID     string    `gorm:"index;type:varchar(255);not null"`
	DeviceID   string    `gorm:"index;type:varchar(255);not null"`
	KeyID      string    `gorm:"type:varchar(255);not null"`
	Algorithm  string    `gorm:"type:varchar(64);not null"`
	PublicKey  string    `gorm:"type:text"`
	PrivateKey string    `gorm:"type:text"` // 加密存储
	Used       bool      `gorm:"default:false"`
	CreatedAt  time.Time
}

func (MatrixOneTimeKey) TableName() string {
	return "matrix_one_time_keys"
}
