package homeserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// MatrixFederationKey 联邦密钥数据库模型
type MatrixFederationKey struct {
	ID         uint64    `gorm:"primary_key;auto_increment"`
	KeyID      string    `gorm:"not null;uniqueIndex;size:64"`
	PrivateKey string    `gorm:"not null;type:text"`
	PublicKey  string    `gorm:"not null;type:text"`
	IsActive   bool      `gorm:"default:true;index"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`
}

func (MatrixFederationKey) TableName() string {
	return "matrix_federation_keys"
}

// KeyStorage 密钥存储管理器
type KeyStorage struct{}

// NewKeyStorage 创建密钥存储管理器
func NewKeyStorage() *KeyStorage {
	return &KeyStorage{}
}

// LoadOrGenerateKey 加载或生成联邦密钥
func (ks *KeyStorage) LoadOrGenerateKey() (ed25519.PrivateKey, ed25519.PublicKey, string, error) {
	// 首先尝试加载现有的活动密钥
	var existingKey MatrixFederationKey
	if err := db.GetDB().Where("is_active = ?", true).First(&existingKey).Error; err == nil {
		// 找到现有密钥，解码并返回
		privateKeyBytes, err := base64.RawStdEncoding.DecodeString(existingKey.PrivateKey)
		if err != nil {
			log.Error("Failed to decode stored private key: %v", err)
			// 如果解码失败，继续生成新密钥
		} else {
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

	// 保存到数据库
	record := MatrixFederationKey{
		KeyID:      keyID,
		PrivateKey: base64.RawStdEncoding.EncodeToString(privateKey),
		PublicKey:  base64.RawStdEncoding.EncodeToString(publicKey),
		IsActive:   true,
	}

	if err := db.GetDB().Create(&record).Error; err != nil {
		log.Error("Failed to save federation key to database: %v", err)
		// 即使保存失败，也返回生成的密钥
		return privateKey, publicKey, keyID, nil
	}

	log.Info("Generated and saved new federation key: %s", keyID)
	return privateKey, publicKey, keyID, nil
}

// GetPublicKey 获取指定密钥 ID 的公钥
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

// GetActiveKey 获取当前活动的密钥
func (ks *KeyStorage) GetActiveKey() (*MatrixFederationKey, error) {
	var record MatrixFederationKey
	if err := db.GetDB().Where("is_active = ?", true).First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// RotateKey 轮换密钥（生成新密钥并停用旧密钥）
func (ks *KeyStorage) RotateKey() (ed25519.PrivateKey, ed25519.PublicKey, string, error) {
	// 停用所有现有密钥
	if err := db.GetDB().Model(&MatrixFederationKey{}).Where("is_active = ?", true).Update("is_active", false).Error; err != nil {
		log.Error("Failed to deactivate old keys: %v", err)
	}

	// 生成新密钥
	return ks.LoadOrGenerateKey()
}

// ListKeys 列出所有密钥
func (ks *KeyStorage) ListKeys() ([]MatrixFederationKey, error) {
	var keys []MatrixFederationKey
	if err := db.GetDB().Order("created_at DESC").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// DeleteKey 删除指定密钥
func (ks *KeyStorage) DeleteKey(keyID string) error {
	return db.GetDB().Where("key_id = ?", keyID).Delete(&MatrixFederationKey{}).Error
}
