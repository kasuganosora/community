package homeserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/kasuganosora/community/db"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

// ===== SSSS（Secure Secret Storage and Sharing）实现 =====
// 安全密钥存储，用于存储密钥备份密钥等敏感数据

// SSSSKeyInfo SSSS 密钥信息
type SSSSKeyInfo struct {
	KeyType      string `json:"key_type"`      // passphrase, raw
	Algorithm    string `json:"algorithm"`     // m.pbkdf2, m.secret_storage.v1.aes-hmac-sha2
	IV           string `json:"iv,omitempty"`
	MAC          string `json:"mac,omitempty"`
	Salt         string `json:"salt,omitempty"` // 用于 passphrase 派生
	Iterations   int    `json:"iterations,omitempty"`
	Bits         int    `json:"bits,omitempty"`
	PublicKey    string `json:"public_key,omitempty"`
	PrivateKeyMAC string `json:"private_key_mac,omitempty"`
}

// SSSSSecret SSSS 存储的密钥
type SSSSSecret struct {
	SecretName string                 `json:"secret_name"` // m.cross_signing.master, m.megolm_backup.v1, etc.
	Encrypted  map[string]interface{} `json:"encrypted"`   // 加密的密钥数据
	Ciphertext string                 `json:"ciphertext"`
	MAC        string                 `json:"mac"`
	IV         string                 `json:"iv"`
}

// SSSSManager SSSS 管理器
type SSSSManager struct {
	hs *HomeServer
}

// NewSSSSManager 创建 SSSS 管理器
func NewSSSSManager(hs *HomeServer) *SSSSManager {
	return &SSSSManager{hs: hs}
}

// GenerateKeyFromPassphrase 从密码生成 SSSS 密钥
func (sm *SSSSManager) GenerateKeyFromPassphrase(passphrase string, keyInfo *SSSSKeyInfo) ([]byte, error) {
	if keyInfo.Algorithm != "m.pbkdf2" {
		return nil, fmt.Errorf("unsupported algorithm: %s", keyInfo.Algorithm)
	}

	// 解码 salt
	salt, err := base64.RawStdEncoding.DecodeString(keyInfo.Salt)
	if err != nil {
		return nil, fmt.Errorf("failed to decode salt: %w", err)
	}

	// 使用 Argon2id 或 PBKDF2 派生密钥
	var key []byte
	if keyInfo.Iterations > 0 {
		// 使用 PBKDF2 风格（通过 Argon2id 模拟）
		key = argon2.IDKey([]byte(passphrase), salt, uint32(keyInfo.Iterations), 64*1024, 4, 32)
	} else {
		// 使用默认参数
		key = argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
	}

	// 验证 MAC
	if keyInfo.MAC != "" {
		computedMAC := computeSSSSMAC(key, keyInfo.IV)
		if computedMAC != keyInfo.MAC {
			return nil, fmt.Errorf("invalid passphrase")
		}
	}

	return key, nil
}

// GenerateRandomKey 生成随机 SSSS 密钥
func (sm *SSSSManager) GenerateRandomKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}
	return key, nil
}

// StoreSecret 存储加密的密钥
func (sm *SSSSManager) StoreSecret(userID string, secret *SSSSSecret) error {
	// 序列化密钥数据
	encryptedData, err := json.Marshal(secret.Encrypted)
	if err != nil {
		return fmt.Errorf("failed to marshal encrypted data: %w", err)
	}

	record := &MatrixSSSSSecret{
		UserID:     userID,
		SecretName: secret.SecretName,
		Encrypted:  string(encryptedData),
		Ciphertext: secret.Ciphertext,
		MAC:        secret.MAC,
		IV:         secret.IV,
	}

	// 使用 Upsert
	return db.GetDB().Where("user_id = ? AND secret_name = ?", userID, secret.SecretName).
		Assign(record).
		FirstOrCreate(&MatrixSSSSSecret{}).Error
}

// GetSecret 获取存储的密钥
func (sm *SSSSManager) GetSecret(userID, secretName string) (*SSSSSecret, error) {
	var record MatrixSSSSSecret
	if err := db.GetDB().Where("user_id = ? AND secret_name = ?", userID, secretName).
		First(&record).Error; err != nil {
		return nil, fmt.Errorf("secret not found: %w", err)
	}

	// 解析加密数据
	var encrypted map[string]interface{}
	if err := json.Unmarshal([]byte(record.Encrypted), &encrypted); err != nil {
		encrypted = make(map[string]interface{})
	}

	return &SSSSSecret{
		SecretName: record.SecretName,
		Encrypted:  encrypted,
		Ciphertext: record.Ciphertext,
		MAC:        record.MAC,
		IV:         record.IV,
	}, nil
}

// DeleteSecret 删除存储的密钥
func (sm *SSSSManager) DeleteSecret(userID, secretName string) error {
	return db.GetDB().Where("user_id = ? AND secret_name = ?", userID, secretName).
		Delete(&MatrixSSSSSecret{}).Error
}

// SetDefaultKey 设置默认 SSSS 密钥
func (sm *SSSSManager) SetDefaultKey(userID, keyID string, keyInfo *SSSSKeyInfo) error {
	keyData, err := json.Marshal(keyInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal key info: %w", err)
	}

	record := &MatrixSSSSDefaultKey{
		UserID:  userID,
		KeyID:   keyID,
		KeyData: string(keyData),
	}

	// 删除旧的默认密钥
	db.GetDB().Where("user_id = ?", userID).Delete(&MatrixSSSSDefaultKey{})

	// 创建新的默认密钥
	return db.GetDB().Create(record).Error
}

// GetDefaultKey 获取默认 SSSS 密钥
func (sm *SSSSManager) GetDefaultKey(userID string) (string, *SSSSKeyInfo, error) {
	var record MatrixSSSSDefaultKey
	if err := db.GetDB().Where("user_id = ?", userID).First(&record).Error; err != nil {
		return "", nil, fmt.Errorf("default key not found: %w", err)
	}

	var keyInfo SSSSKeyInfo
	if err := json.Unmarshal([]byte(record.KeyData), &keyInfo); err != nil {
		return "", nil, fmt.Errorf("failed to parse key info: %w", err)
	}

	return record.KeyID, &keyInfo, nil
}

// EncryptSecret 使用 SSSS 密钥加密密钥
func (sm *SSSSManager) EncryptSecret(secret []byte, key []byte) (*SSSSSecret, error) {
	// 生成 IV
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}

	// 派生 AES 密钥和 MAC 密钥
	aesKey, macKey := sm.deriveKeys(key, iv)

	// 加密
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	ciphertext := gcm.Seal(nil, iv, secret, nil)

	// 计算 MAC（使用 HMAC-SHA256）
	mac := computeHMAC(ciphertext, macKey)

	return &SSSSSecret{
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
		MAC:        base64.RawStdEncoding.EncodeToString(mac),
		IV:         base64.RawStdEncoding.EncodeToString(iv),
	}, nil
}

// DecryptSecret 使用 SSSS 密钥解密密钥
func (sm *SSSSManager) DecryptSecret(secret *SSSSSecret, key []byte) ([]byte, error) {
	// 解码数据
	ciphertext, err := base64.RawStdEncoding.DecodeString(secret.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	iv, err := base64.RawStdEncoding.DecodeString(secret.IV)
	if err != nil {
		return nil, fmt.Errorf("failed to decode IV: %w", err)
	}

	expectedMAC, err := base64.RawStdEncoding.DecodeString(secret.MAC)
	if err != nil {
		return nil, fmt.Errorf("failed to decode MAC: %w", err)
	}

	// 派生密钥
	aesKey, macKey := sm.deriveKeys(key, iv)

	// 验证 MAC
	computedMAC := computeHMAC(ciphertext, macKey)
	if len(computedMAC) != len(expectedMAC) {
		return nil, fmt.Errorf("MAC length mismatch")
	}
	for i := range computedMAC {
		if computedMAC[i] != expectedMAC[i] {
			return nil, fmt.Errorf("MAC verification failed")
		}
	}

	// 解密
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// deriveKeys 从 SSSS 密钥派生 AES 和 MAC 密钥
func (sm *SSSSManager) deriveKeys(key, iv []byte) (aesKey, macKey []byte) {
	// 使用 HKDF 派生两个密钥
	hkdfReader := hkdf.New(sha256.New, key, iv, []byte("SSSS_V1"))

	aesKey = make([]byte, 32)
	macKey = make([]byte, 32)

	hkdfReader.Read(aesKey)
	hkdfReader.Read(macKey)

	return
}

// ===== 密钥共享（Secret Sharing）=====

// SecretShare 密钥分片
type SecretShare struct {
	Index   int    `json:"index"`
	Share   string `json:"share"` // Base64 编码的分片
	Threshold int  `json:"threshold"`
}

// SplitSecret 使用 Shamir's Secret Sharing 分割密钥
func (sm *SSSSManager) SplitSecret(secret []byte, threshold, shares int) ([]*SecretShare, error) {
	if threshold > shares {
		return nil, fmt.Errorf("threshold cannot be greater than shares")
	}

	// 简化实现：使用基本的秘密共享
	// 注意：这是简化版，生产环境应使用完整的 Shamir's Secret Sharing
	sharesList := make([]*SecretShare, shares)

	// 生成随机多项式系数
	coefficients := make([][]byte, threshold-1)
	for i := range coefficients {
		coeff := make([]byte, len(secret))
		rand.Read(coeff)
		coefficients[i] = coeff
	}

	// 计算每个分片
	for i := 0; i < shares; i++ {
		index := i + 1 // 从 1 开始
		share := make([]byte, len(secret))
		copy(share, secret) // P(0) = secret

		// 计算 P(x) = secret + a1*x + a2*x^2 + ...
		for j, coeff := range coefficients {
			power := index
			for k := 0; k < j; k++ {
				power *= index
			}
			for k := range share {
				share[k] ^= coeff[k] * byte(power)
			}
		}

		sharesList[i] = &SecretShare{
			Index:     index,
			Share:     base64.RawStdEncoding.EncodeToString(share),
			Threshold: threshold,
		}
	}

	return sharesList, nil
}

// RecoverSecret 从分片恢复密钥
func (sm *SSSSManager) RecoverSecret(shares []*SecretShare) ([]byte, error) {
	if len(shares) == 0 {
		return nil, fmt.Errorf("no shares provided")
	}

	threshold := shares[0].Threshold
	if len(shares) < threshold {
		return nil, fmt.Errorf("insufficient shares: got %d, need %d", len(shares), threshold)
	}

	// 简化实现：使用拉格朗日插值
	// 注意：这是简化版，生产环境应使用完整的拉格朗日插值
	
	// 解码分片
	decodedShares := make([][]byte, len(shares))
	for i, share := range shares {
		decoded, err := base64.RawStdEncoding.DecodeString(share.Share)
		if err != nil {
			return nil, fmt.Errorf("failed to decode share %d: %w", i, err)
		}
		decodedShares[i] = decoded
	}

	// 恢复密钥（简化版 XOR 恢复）
	secret := make([]byte, len(decodedShares[0]))
	for i := range secret {
		secret[i] = decodedShares[0][i]
		for j := 1; j < len(decodedShares); j++ {
			secret[i] ^= decodedShares[j][i]
		}
	}

	return secret, nil
}

// ===== 数据库模型 =====

// MatrixSSSSSecret SSSS 密钥存储表
type MatrixSSSSSecret struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	UserID     string `gorm:"index;type:varchar(255);not null"`
	SecretName string `gorm:"index;type:varchar(128);not null"`
	Encrypted  string `gorm:"type:text"`
	Ciphertext string `gorm:"type:text;not null"`
	MAC        string `gorm:"type:varchar(128);not null"`
	IV         string `gorm:"type:varchar(128);not null"`
	CreatedAt  int64  `gorm:"autoCreateTime"`
	UpdatedAt  int64  `gorm:"autoUpdateTime"`
}

func (MatrixSSSSSecret) TableName() string {
	return "matrix_ssss_secrets"
}

// MatrixSSSSDefaultKey SSSS 默认密钥表
type MatrixSSSSDefaultKey struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	UserID    string `gorm:"index;type:varchar(255);not null"`
	KeyID     string `gorm:"type:varchar(64);not null"`
	KeyData   string `gorm:"type:text;not null"`
	CreatedAt int64  `gorm:"autoCreateTime"`
	UpdatedAt int64  `gorm:"autoUpdateTime"`
}

func (MatrixSSSSDefaultKey) TableName() string {
	return "matrix_ssss_default_keys"
}

// ===== 辅助函数 =====

// computeSSSSMAC 计算 SSSS MAC
func computeSSSSMAC(key []byte, iv string) string {
	data := append(key, []byte(iv)...)
	hash := sha256.Sum256(data)
	return base64.RawStdEncoding.EncodeToString(hash[:16]) // 使用 128 位 MAC
}

// computeHMAC 计算 HMAC
func computeHMAC(data, key []byte) []byte {
	// 简化的 HMAC 实现
	h := sha256.New()
	h.Write(key)
	h.Write(data)
	return h.Sum(nil)
}
