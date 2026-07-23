package homeserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// MatrixFederationKey 联邦密钥数据库模型.
type MatrixFederationKey struct {
	ID         uint64    `gorm:"primary_key;auto_increment"`
	KeyID      string    `gorm:"not null;uniqueIndex;size:64"`
	KeyType    string    `gorm:"type:varchar(32);index"` // "federation", "master", etc.
	PrivateKey string    `gorm:"not null;type:text"`
	PublicKey  string    `gorm:"not null;type:text"`
	IsActive   bool      `gorm:"default:true;index"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`
}

func (MatrixFederationKey) TableName() string {
	return "matrix_federation_keys"
}

// KeyStorage 密钥存储管理器.
type KeyStorage struct{}

// NewKeyStorage 创建密钥存储管理器.
func NewKeyStorage() *KeyStorage {
	return &KeyStorage{}
}

// LoadOrGenerateKey 加载或生成联邦密钥.
func (ks *KeyStorage) LoadOrGenerateKey() (ed25519.PrivateKey, ed25519.PublicKey, string, error) {
	// 首先尝试加载现有的活动密钥
	var existingKey MatrixFederationKey
	if err := db.GetDB().Where("is_active = ?", true).First(&existingKey).Error; err == nil {
		// 尝试解密私钥
		privateKeyBytes, err := ks.decryptKey(existingKey.PrivateKey)
		if err != nil {
			// 回退：尝试作为未加密的 base64 解码（兼容旧数据）
			privateKeyBytes, err = base64.RawStdEncoding.DecodeString(existingKey.PrivateKey)
			if err != nil {
				log.Error("Failed to decode stored private key: %v", err)
			} else {
				// 迁移：将旧的未加密密钥加密后重新保存
				if encrypted, encErr := ks.encryptKey(privateKeyBytes); encErr == nil {
					db.GetDB().Model(&existingKey).Update("private_key", encrypted)
					log.Info("Migrated federation key %s to encrypted storage", existingKey.KeyID)
				}
			}
		}

		if privateKeyBytes != nil {
			publicKeyBytes, err := base64.RawStdEncoding.DecodeString(existingKey.PublicKey)
			if err != nil {
				log.Error("Failed to decode stored public key: %v", err)
			} else {
				privateKey := ed25519.PrivateKey(privateKeyBytes)
				publicKey := ed25519.PublicKey(publicKeyBytes)

				log.Info("Loaded existing federation key: %s", existingKey.KeyID)

				return privateKey, publicKey, existingKey.KeyID, nil
			}
		}
	}

	// 生成新密钥
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to generate federation key: %w", err)
	}

	keyID := fmt.Sprintf("ed25519:%d", time.Now().Unix())

	// 加密私钥后保存到数据库
	encryptedPrivateKey, err := ks.encryptKey([]byte(privateKey))
	if err != nil {
		// 如果加密失败，回退到 base64 编码
		log.Error("Failed to encrypt private key, falling back to base64: %v", err)

		encryptedPrivateKey = base64.RawStdEncoding.EncodeToString(privateKey)
	}

	record := MatrixFederationKey{
		KeyID:      keyID,
		PrivateKey: encryptedPrivateKey,
		PublicKey:  base64.RawStdEncoding.EncodeToString(publicKey),
		IsActive:   true,
	}

	if err := db.GetDB().Create(&record).Error; err != nil {
		log.Error("Failed to save federation key to database: %v", err)

		return privateKey, publicKey, keyID, nil
	}

	log.Info("Generated and saved new federation key: %s", keyID)

	return privateKey, publicKey, keyID, nil
}

// deriveEncryptionKey 从配置密钥派生 AES-256 密钥.
func deriveEncryptionKey(secret string) []byte {
	hash := sha256.Sum256([]byte(secret))

	return hash[:]
}

// encryptKey 使用 AES-GCM 加密密钥.
func (ks *KeyStorage) encryptKey(plaintext []byte) (string, error) {
	secret := config.C.EncryptedKey
	if secret == "" {
		return base64.RawStdEncoding.EncodeToString(plaintext), nil
	}

	key := deriveEncryptionKey(secret)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	return "enc:" + base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

// decryptKey 使用 AES-GCM 解密密钥.
func (ks *KeyStorage) decryptKey(encoded string) ([]byte, error) {
	// 检查是否是加密格式
	if len(encoded) < 4 || encoded[:4] != "enc:" {
		return nil, errors.New("not an encrypted key")
	}

	secret := config.C.EncryptedKey
	if secret == "" {
		return nil, errors.New("encryption key not configured")
	}

	data, err := base64.RawStdEncoding.DecodeString(encoded[4:])
	if err != nil {
		return nil, fmt.Errorf("failed to decode: %w", err)
	}

	key := deriveEncryptionKey(secret)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// GetPublicKey 获取指定密钥 ID 的公钥.
func (ks *KeyStorage) GetPublicKey(keyID string) (ed25519.PublicKey, error) {
	var record MatrixFederationKey
	if err := db.GetDB().Where("key_id = ?", keyID).First(&record).Error; err != nil {
		return nil, fmt.Errorf("key not found: %w", err)
	}

	publicKeyBytes, err := base64.RawStdEncoding.DecodeString(record.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key: %w", err)
	}

	return ed25519.PublicKey(publicKeyBytes), nil
}

// GetActiveKey 获取当前活动的密钥.
func (ks *KeyStorage) GetActiveKey() (*MatrixFederationKey, error) {
	var record MatrixFederationKey
	err := db.GetDB().Where("is_active = ?", true).First(&record).Error
	if err != nil {
		return nil, err
	}

	return &record, nil
}

// RotateKey 轮换密钥（生成新密钥并停用旧密钥）.
func (ks *KeyStorage) RotateKey() (ed25519.PrivateKey, ed25519.PublicKey, string, error) {
	// 停用所有现有密钥
	err := db.GetDB().Model(&MatrixFederationKey{}).Where("is_active = ?", true).Update("is_active", false).Error
	if err != nil {
		log.Error("Failed to deactivate old keys: %v", err)
	}

	// 生成新密钥
	return ks.LoadOrGenerateKey()
}

// ListKeys 列出所有密钥.
func (ks *KeyStorage) ListKeys() ([]MatrixFederationKey, error) {
	var keys []MatrixFederationKey
	err := db.GetDB().Order("created_at DESC").Find(&keys).Error
	if err != nil {
		return nil, err
	}

	return keys, nil
}

// DeleteKey 删除指定密钥.
func (ks *KeyStorage) DeleteKey(keyID string) error {
	return db.GetDB().Where("key_id = ?", keyID).Delete(&MatrixFederationKey{}).Error
}
