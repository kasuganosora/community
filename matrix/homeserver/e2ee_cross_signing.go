package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/awnumar/memguard"
	"github.com/kasuganosora/community/db"
)

// ===== 跨签名（Cross-signing）实现 =====
// 跨签名允许用户验证其他用户/设备，建立信任网络

// CrossSigningKeyType 跨签名密钥类型
type CrossSigningKeyType string

const (
	CrossSigningKeyMaster      CrossSigningKeyType = "master"
	CrossSigningKeySelfSigning CrossSigningKeyType = "self_signing"
	CrossSigningKeyUserSigning CrossSigningKeyType = "user_signing"
)

// CrossSigningManager 跨签名管理器
type CrossSigningManager struct {
	KeyStore *KeyStore
}

// NewCrossSigningManager 创建跨签名管理器
func NewCrossSigningManager(ks *KeyStore) *CrossSigningManager {
	return &CrossSigningManager{KeyStore: ks}
}

// GenerateCrossSigningKeys 为用户的所有设备生成交叉签名密钥
func (csm *CrossSigningManager) GenerateCrossSigningKeys(userID string) (*CrossSigningKey, *CrossSigningKey, *CrossSigningKey, error) {
	// 1. 生成主密钥
	masterKey, err := csm.generateKey(userID, CrossSigningKeyMaster)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate master key: %w", err)
	}

	// 2. 生成自签名密钥
	selfSigningKey, err := csm.generateKey(userID, CrossSigningKeySelfSigning)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate self-signing key: %w", err)
	}

	// 3. 生成用户签名密钥
	userSigningKey, err := csm.generateKey(userID, CrossSigningKeyUserSigning)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate user-signing key: %w", err)
	}

	// 4. 主密钥签名自签名密钥
	if err := csm.signKey(masterKey, selfSigningKey); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to sign self-signing key: %w", err)
	}

	// 5. 主密钥签名用户签名密钥
	if err := csm.signKey(masterKey, userSigningKey); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to sign user-signing key: %w", err)
	}

	// 6. 保存到数据库
	if err := csm.saveCrossSigningKey(userID, masterKey); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to save master key: %w", err)
	}
	if err := csm.saveCrossSigningKey(userID, selfSigningKey); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to save self-signing key: %w", err)
	}
	if err := csm.saveCrossSigningKey(userID, userSigningKey); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to save user-signing key: %w", err)
	}

	return masterKey, selfSigningKey, userSigningKey, nil
}

// generateKey 生成单个跨签名密钥
func (csm *CrossSigningManager) generateKey(userID string, keyType CrossSigningKeyType) (*CrossSigningKey, error) {
	// 生成 Ed25519 密钥对
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	publicKey := privateKey[32:]
	encodedKey := base64.RawStdEncoding.EncodeToString(publicKey)
	if len(encodedKey) > 16 {
		encodedKey = encodedKey[:16]
	}
	keyID := fmt.Sprintf("ed25519:%s", encodedKey)

	csk := &CrossSigningKey{
		UserID: userID,
		Usage:  []string{string(keyType)},
		Keys: map[string]string{
			keyID: base64.RawStdEncoding.EncodeToString(publicKey),
		},
		Signatures: make(map[string]map[string]string),
		PrivateKey: memguard.NewEnclave(privateKey),
	}

	// 自签名
	if err := csm.selfSignKey(csk); err != nil {
		return nil, err
	}

	return csk, nil
}

// selfSignKey 密钥自签名
func (csm *CrossSigningManager) selfSignKey(key *CrossSigningKey) error {
	privateKeyBuf, err := key.PrivateKey.Open()
	if err != nil {
		return fmt.Errorf("failed to open private key: %w", err)
	}
	defer privateKeyBuf.Destroy()
	privateKey := privateKeyBuf.Bytes()

	// 创建签名内容（不含签名部分）
	keyCopy := *key
	keyCopy.Signatures = nil
	keyCopy.PrivateKey = nil

	data, err := json.Marshal(keyCopy)
	if err != nil {
		return fmt.Errorf("failed to marshal key: %w", err)
	}

	// 签名
	signature := ed25519.Sign(privateKey, data)

	// 获取公钥用于 key ID
	for keyID := range key.Keys {
		if key.Signatures[key.UserID] == nil {
			key.Signatures[key.UserID] = make(map[string]string)
		}
		key.Signatures[key.UserID][keyID] = base64.RawStdEncoding.EncodeToString(signature)
		break
	}

	return nil
}

// signKey 使用源密钥签名目标密钥
func (csm *CrossSigningManager) signKey(source, target *CrossSigningKey) error {
	// 获取源私钥
	sourcePrivateBuf, err := source.PrivateKey.Open()
	if err != nil {
		return fmt.Errorf("failed to open source private key: %w", err)
	}
	defer sourcePrivateBuf.Destroy()
	sourcePrivateKey := sourcePrivateBuf.Bytes()

	// 创建签名内容
	targetCopy := *target
	targetCopy.Signatures = nil
	targetCopy.PrivateKey = nil

	data, err := json.Marshal(targetCopy)
	if err != nil {
		return fmt.Errorf("failed to marshal target key: %w", err)
	}

	// 签名
	signature := ed25519.Sign(sourcePrivateKey, data)

	// 获取源密钥 ID
	var sourceKeyID string
	for kid := range source.Keys {
		sourceKeyID = kid
		break
	}

	// 添加签名
	if target.Signatures[source.UserID] == nil {
		target.Signatures[source.UserID] = make(map[string]string)
	}
	target.Signatures[source.UserID][sourceKeyID] = base64.RawStdEncoding.EncodeToString(signature)

	return nil
}

// SignDevice 使用自签名密钥签名设备
func (csm *CrossSigningManager) SignDevice(userID string, deviceKeys *DeviceKeys) error {
	// 获取用户的自签名密钥
	selfSigningKey, err := csm.getCrossSigningKey(userID, CrossSigningKeySelfSigning)
	if err != nil {
		return fmt.Errorf("self-signing key not found: %w", err)
	}

	// 获取私钥
	privateKeyBuf, err := selfSigningKey.PrivateKey.Open()
	if err != nil {
		return fmt.Errorf("failed to open private key: %w", err)
	}
	defer privateKeyBuf.Destroy()
	privateKey := privateKeyBuf.Bytes()

	// 创建设备签名内容
	signContent := map[string]interface{}{
		"algorithms": deviceKeys.Algorithms,
		"device_id":  deviceKeys.DeviceID,
		"keys":       deviceKeys.Keys,
		"user_id":    deviceKeys.UserID,
	}

	data, err := json.Marshal(signContent)
	if err != nil {
		return fmt.Errorf("failed to marshal device keys: %w", err)
	}

	// 签名
	signature := ed25519.Sign(privateKey, data)

	// 获取自签名密钥 ID
	var keyID string
	for kid := range selfSigningKey.Keys {
		keyID = kid
		break
	}

	// 添加签名
	if deviceKeys.Signatures == nil {
		deviceKeys.Signatures = make(map[string]map[string]string)
	}
	if deviceKeys.Signatures[userID] == nil {
		deviceKeys.Signatures[userID] = make(map[string]string)
	}
	deviceKeys.Signatures[userID][keyID] = base64.RawStdEncoding.EncodeToString(signature)

	return nil
}

// SignUser 使用用户签名密钥签名其他用户
func (csm *CrossSigningManager) SignUser(userID, targetUserID string, targetMasterKey *CrossSigningKey) error {
	// 获取用户的用户签名密钥
	userSigningKey, err := csm.getCrossSigningKey(userID, CrossSigningKeyUserSigning)
	if err != nil {
		return fmt.Errorf("user-signing key not found: %w", err)
	}

	// 获取私钥
	privateKeyBuf, err := userSigningKey.PrivateKey.Open()
	if err != nil {
		return fmt.Errorf("failed to open private key: %w", err)
	}
	defer privateKeyBuf.Destroy()
	privateKey := privateKeyBuf.Bytes()

	// 创建签名内容
	keyCopy := *targetMasterKey
	keyCopy.Signatures = nil
	keyCopy.PrivateKey = nil

	data, err := json.Marshal(keyCopy)
	if err != nil {
		return fmt.Errorf("failed to marshal target key: %w", err)
	}

	// 签名
	signature := ed25519.Sign(privateKey, data)

	// 获取用户签名密钥 ID
	var keyID string
	for kid := range userSigningKey.Keys {
		keyID = kid
		break
	}

	// 添加签名
	if targetMasterKey.Signatures == nil {
		targetMasterKey.Signatures = make(map[string]map[string]string)
	}
	if targetMasterKey.Signatures[userID] == nil {
		targetMasterKey.Signatures[userID] = make(map[string]string)
	}
	targetMasterKey.Signatures[userID][keyID] = base64.RawStdEncoding.EncodeToString(signature)

	return nil
}

// VerifyCrossSigningKey 验证跨签名密钥的签名
func (csm *CrossSigningManager) VerifyCrossSigningKey(key *CrossSigningKey, signerUserID string) (bool, error) {
	// 查找签名者的公钥
	signatures, ok := key.Signatures[signerUserID]
	if !ok || len(signatures) == 0 {
		return false, fmt.Errorf("no signatures from %s", signerUserID)
	}

	// 对于自签名，使用自己的公钥验证
	var publicKey []byte
	if signerUserID == key.UserID {
		// 自签名，从 Keys 中获取公钥
		for _, keyData := range key.Keys {
			var err error
			publicKey, err = base64.RawStdEncoding.DecodeString(keyData)
			if err != nil {
				return false, fmt.Errorf("failed to decode public key: %w", err)
			}
			break
		}
	} else {
		// 获取签名者的主密钥
		signerMasterKey, err := csm.getCrossSigningKey(signerUserID, CrossSigningKeyMaster)
		if err != nil {
			return false, fmt.Errorf("signer master key not found: %w", err)
		}

		for _, keyData := range signerMasterKey.Keys {
			var err error
			publicKey, err = base64.RawStdEncoding.DecodeString(keyData)
			if err != nil {
				return false, fmt.Errorf("failed to decode signer public key: %w", err)
			}
			break
		}
	}

	if publicKey == nil {
		return false, fmt.Errorf("public key not found")
	}

	// 验证每个签名
	for keyID, signatureB64 := range signatures {
		signature, err := base64.RawStdEncoding.DecodeString(signatureB64)
		if err != nil {
			continue
		}

		// 创建签名内容
		keyCopy := *key
		keyCopy.Signatures = nil
		keyCopy.PrivateKey = nil

		data, err := json.Marshal(keyCopy)
		if err != nil {
			continue
		}

		// 验证签名
		if ed25519.Verify(publicKey, data, signature) {
			return true, nil
		}
		_ = keyID // keyID 可能用于调试
	}

	return false, fmt.Errorf("signature verification failed")
}

// GetCrossSigningKeys 获取用户的所有跨签名密钥
func (csm *CrossSigningManager) GetCrossSigningKeys(userID string) (*CrossSigningKey, *CrossSigningKey, *CrossSigningKey, error) {
	masterKey, err := csm.getCrossSigningKey(userID, CrossSigningKeyMaster)
	if err != nil {
		return nil, nil, nil, err
	}

	selfSigningKey, err := csm.getCrossSigningKey(userID, CrossSigningKeySelfSigning)
	if err != nil {
		return nil, nil, nil, err
	}

	userSigningKey, err := csm.getCrossSigningKey(userID, CrossSigningKeyUserSigning)
	if err != nil {
		return nil, nil, nil, err
	}

	return masterKey, selfSigningKey, userSigningKey, nil
}

// getCrossSigningKey 从数据库获取跨签名密钥
func (csm *CrossSigningManager) getCrossSigningKey(userID string, keyType CrossSigningKeyType) (*CrossSigningKey, error) {
	var record MatrixCrossSigningKey
	if err := db.GetDB().Where("user_id = ? AND key_type = ? AND is_active = ?", userID, string(keyType), true).
		Order("created_at DESC").
		First(&record).Error; err != nil {
		return nil, fmt.Errorf("key not found: %w", err)
	}

	// 解析密钥数据
	var keyData map[string]interface{}
	if err := json.Unmarshal([]byte(record.KeyData), &keyData); err != nil {
		return nil, fmt.Errorf("failed to parse key data: %w", err)
	}

	csk := &CrossSigningKey{
		UserID:     record.UserID,
		Signatures: make(map[string]map[string]string),
		Keys:       make(map[string]string),
	}

	// 解析 Usage
	if usage, ok := keyData["usage"].([]interface{}); ok {
		for _, u := range usage {
			if s, ok := u.(string); ok {
				csk.Usage = append(csk.Usage, s)
			}
		}
	}

	// 解析 Keys
	if keys, ok := keyData["keys"].(map[string]interface{}); ok {
		for k, v := range keys {
			if s, ok := v.(string); ok {
				csk.Keys[k] = s
			}
		}
	}

	// 解析 Signatures
	if sigs, ok := keyData["signatures"].(map[string]interface{}); ok {
		for domain, domainSigs := range sigs {
			if domainSigMap, ok := domainSigs.(map[string]interface{}); ok {
				csk.Signatures[domain] = make(map[string]string)
				for k, v := range domainSigMap {
					if s, ok := v.(string); ok {
						csk.Signatures[domain][k] = s
					}
				}
			}
		}
	}

	return csk, nil
}

// saveCrossSigningKey 保存跨签名密钥到数据库
func (csm *CrossSigningManager) saveCrossSigningKey(userID string, key *CrossSigningKey) error {
	// 序列化密钥数据
	keyData := map[string]interface{}{
		"usage":      key.Usage,
		"keys":       key.Keys,
		"signatures": key.Signatures,
	}

	jsonData, err := json.Marshal(keyData)
	if err != nil {
		return fmt.Errorf("failed to marshal key data: %w", err)
	}

	// 确定密钥类型
	var keyType string
	if len(key.Usage) > 0 {
		keyType = key.Usage[0]
	}

	record := MatrixCrossSigningKey{
		UserID:   userID,
		KeyType:  keyType,
		KeyData:  string(jsonData),
		IsActive: true,
	}

	// 保存到数据库
	if err := db.GetDB().Create(&record).Error; err != nil {
		return fmt.Errorf("failed to save to database: %w", err)
	}

	return nil
}

// UpdateKeySignatures 更新密钥签名
func (csm *CrossSigningManager) UpdateKeySignatures(userID string, masterKey, selfSigningKey, userSigningKey *CrossSigningKey) error {
	// 更新数据库中的签名
	for _, key := range []*CrossSigningKey{masterKey, selfSigningKey, userSigningKey} {
		if key == nil {
			continue
		}

		keyData := map[string]interface{}{
			"usage":      key.Usage,
			"keys":       key.Keys,
			"signatures": key.Signatures,
		}

		jsonData, err := json.Marshal(keyData)
		if err != nil {
			return err
		}

		var keyType string
		if len(key.Usage) > 0 {
			keyType = key.Usage[0]
		}

		db.GetDB().Model(&MatrixCrossSigningKey{}).
			Where("user_id = ? AND key_type = ?", userID, keyType).
			Update("key_data", string(jsonData))
	}

	return nil
}

// RevokeCrossSigningKeys 撤销用户的跨签名密钥
func (csm *CrossSigningManager) RevokeCrossSigningKeys(userID string) error {
	return db.GetDB().Model(&MatrixCrossSigningKey{}).
		Where("user_id = ?", userID).
		Update("is_active", false).Error
}

// ===== 设备验证流程 =====

// DeviceVerification 设备验证状态
type DeviceVerification struct {
	TransactionID   string
	UserID          string
	DeviceID        string
	OtherUserID     string
	OtherDeviceID   string
	Method          string // sas, qr_code_scan, etc.
	Stage           string // requested, started, keys_exchanged, accepted, verified, cancelled
	StartTime       time.Time
	ExpirationTime  time.Time
	SASData         map[string]interface{}
	MACMethod       string
	KeyAgreementProtocol string
	HashAlgorithm   string
	CipherAlgorithm string
}

// DeviceVerificationManager 设备验证管理器
type DeviceVerificationManager struct {
	verifications map[string]*DeviceVerification // transaction_id -> verification
	callbacks     map[string][]func(string, bool) // 验证完成回调
}

// NewDeviceVerificationManager 创建设备验证管理器
func NewDeviceVerificationManager() *DeviceVerificationManager {
	return &DeviceVerificationManager{
		verifications: make(map[string]*DeviceVerification),
		callbacks:     make(map[string][]func(string, bool)),
	}
}

// RequestVerification 请求验证设备
func (dvm *DeviceVerificationManager) RequestVerification(userID, deviceID, otherUserID, otherDeviceID, method string) (*DeviceVerification, error) {
	transactionID := generateSessionID()

	verification := &DeviceVerification{
		TransactionID:  transactionID,
		UserID:         userID,
		DeviceID:       deviceID,
		OtherUserID:    otherUserID,
		OtherDeviceID:  otherDeviceID,
		Method:         method,
		Stage:          "requested",
		StartTime:      time.Now(),
		ExpirationTime: time.Now().Add(10 * time.Minute),
	}

	dvm.verifications[transactionID] = verification

	return verification, nil
}

// AcceptVerification 接受验证请求
func (dvm *DeviceVerificationManager) AcceptVerification(transactionID string) (*DeviceVerification, error) {
	verification, ok := dvm.verifications[transactionID]
	if !ok {
		return nil, fmt.Errorf("verification not found")
	}

	if verification.Stage != "requested" {
		return nil, fmt.Errorf("invalid verification stage: %s", verification.Stage)
	}

	verification.Stage = "accepted"
	return verification, nil
}

// CompleteVerification 完成验证
func (dvm *DeviceVerificationManager) CompleteVerification(transactionID string, verified bool) error {
	verification, ok := dvm.verifications[transactionID]
	if !ok {
		return fmt.Errorf("verification not found")
	}

	if verified {
		verification.Stage = "verified"
	} else {
		verification.Stage = "cancelled"
	}

	// 触发回调
	key := verification.UserID + ":" + verification.DeviceID + ":" + verification.OtherUserID + ":" + verification.OtherDeviceID
	if callbacks, ok := dvm.callbacks[key]; ok {
		for _, cb := range callbacks {
			go cb(transactionID, verified)
		}
	}

	return nil
}

// RegisterCallback 注册验证完成回调
func (dvm *DeviceVerificationManager) RegisterCallback(userID, deviceID, otherUserID, otherDeviceID string, callback func(string, bool)) {
	key := userID + ":" + deviceID + ":" + otherUserID + ":" + otherDeviceID
	dvm.callbacks[key] = append(dvm.callbacks[key], callback)
}
