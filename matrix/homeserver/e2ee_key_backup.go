package homeserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// ===== 密钥备份（Server-Side Key Backup）实现 =====
// 允许用户将 Megolm 会话密钥备份到服务器

// KeyBackupVersion 密钥备份版本
type KeyBackupVersion struct {
	Version   string                 `json:"version"`
	Algorithm string                 `json:"algorithm"` // m.megolm_backup.v1.curve25519-aes-sha2
	AuthData  map[string]interface{} `json:"auth_data"` // 包含备份的公钥和签名
	Count     int                    `json:"count"`     // 备份的会话数量
	ETag      string                 `json:"etag"`      // 版本标识
}

// KeyBackupSession 备份的会话密钥
type KeyBackupSession struct {
	RoomID            string `json:"room_id"`
	SessionID         string `json:"session_id"`
	FirstMessageIndex int    `json:"first_message_index"`
	ForwardedCount    int    `json:"forwarded_count"`
	IsVerified        bool   `json:"is_verified"`
	SessionData       string `json:"session_data"` // 加密的会话数据
}

// KeyBackupData 密钥备份数据（存储在数据库中）
type KeyBackupData struct {
	Algorithm  string `json:"algorithm"`
	Ciphertext string `json:"ciphertext"` // 加密的备份密钥
	MAC        string `json:"mac"`
	IV         string `json:"iv"`
}

// KeyBackupManager 密钥备份管理器
type KeyBackupManager struct {
	hs *HomeServer
}

// NewKeyBackupManager 创建密钥备份管理器
func NewKeyBackupManager(hs *HomeServer) *KeyBackupManager {
	return &KeyBackupManager{hs: hs}
}

// CreateBackupVersion 创建新的密钥备份版本
func (kbm *KeyBackupManager) CreateBackupVersion(userID string, authData map[string]interface{}) (*KeyBackupVersion, error) {
	// 生成版本 ID
	version := generateBackupVersion()

	// 序列化 auth_data
	authDataJSON, err := json.Marshal(authData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth_data: %w", err)
	}

	// 保存到数据库
	record := &MatrixKeyBackup{
		UserID:    userID,
		Version:   version,
		Algorithm: "m.megolm_backup.v1.curve25519-aes-sha2",
		AuthData:  string(authDataJSON),
		Count:     0,
		ETag:      generateETag(),
	}

	if err := db.GetDB().Create(record).Error; err != nil {
		return nil, fmt.Errorf("failed to create backup: %w", err)
	}

	return &KeyBackupVersion{
		Version:   version,
		Algorithm: record.Algorithm,
		AuthData:  authData,
		Count:     0,
		ETag:      record.ETag,
	}, nil
}

// GetBackupVersion 获取密钥备份版本
func (kbm *KeyBackupManager) GetBackupVersion(userID, version string) (*KeyBackupVersion, error) {
	var record MatrixKeyBackup
	query := db.GetDB().Where("user_id = ?", userID)
	
	if version != "" {
		query = query.Where("version = ?", version)
	} else {
		// 获取最新版本
		query = query.Order("created_at DESC")
	}

	if err := query.First(&record).Error; err != nil {
		return nil, fmt.Errorf("backup not found: %w", err)
	}

	// 解析 auth_data
	var authData map[string]interface{}
	if err := json.Unmarshal([]byte(record.AuthData), &authData); err != nil {
		authData = make(map[string]interface{})
	}

	return &KeyBackupVersion{
		Version:   record.Version,
		Algorithm: record.Algorithm,
		AuthData:  authData,
		Count:     record.Count,
		ETag:      record.ETag,
	}, nil
}

// DeleteBackupVersion 删除密钥备份版本
func (kbm *KeyBackupManager) DeleteBackupVersion(userID, version string) error {
	// 删除备份记录
	result := db.GetDB().Where("user_id = ? AND version = ?", userID, version).Delete(&MatrixKeyBackup{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete backup: %w", result.Error)
	}

	// 删除所有相关的会话密钥
	db.GetDB().Where("user_id = ? AND version = ?", userID, version).Delete(&MatrixKeyBackupSession{})

	return nil
}

// StoreBackupKeys 存储备份的会话密钥
func (kbm *KeyBackupManager) StoreBackupKeys(userID, version string, rooms map[string]map[string]KeyBackupSession) error {
	// 检查备份版本是否存在
	var backup MatrixKeyBackup
	if err := db.GetDB().Where("user_id = ? AND version = ?", userID, version).First(&backup).Error; err != nil {
		return fmt.Errorf("backup version not found: %w", err)
	}

	// 统计新增数量
	newCount := 0

	// 存储每个房间的会话密钥
	for roomID, sessions := range rooms {
		for sessionID, session := range sessions {
			// 序列化会话数据
			sessionData, err := json.Marshal(session)
			if err != nil {
				log.Error("Failed to marshal session %s: %v", sessionID, err)
				continue
			}

			record := &MatrixKeyBackupSession{
				UserID:      userID,
				Version:     version,
				RoomID:      roomID,
				SessionID:   sessionID,
				SessionData: string(sessionData),
			}

			// 使用 Upsert
			if err := db.GetDB().Where("user_id = ? AND version = ? AND room_id = ? AND session_id = ?",
				userID, version, roomID, sessionID).
				Assign(record).
				FirstOrCreate(&MatrixKeyBackupSession{}).Error; err != nil {
				log.Error("Failed to store session %s: %v", sessionID, err)
				continue
			}

			newCount++
		}
	}

	// 更新备份计数
	if newCount > 0 {
		backup.Count += newCount
		backup.ETag = generateETag()
		db.GetDB().Save(&backup)
	}

	return nil
}

// GetBackupKeys 获取备份的会话密钥
func (kbm *KeyBackupManager) GetBackupKeys(userID, version, roomID, sessionID string) (*KeyBackupSession, error) {
	var record MatrixKeyBackupSession
	query := db.GetDB().Where("user_id = ? AND version = ?", userID, version)

	if roomID != "" {
		query = query.Where("room_id = ?", roomID)
	}
	if sessionID != "" {
		query = query.Where("session_id = ?", sessionID)
	}

	if err := query.First(&record).Error; err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	// 解析会话数据
	var session KeyBackupSession
	if err := json.Unmarshal([]byte(record.SessionData), &session); err != nil {
		return nil, fmt.Errorf("failed to parse session data: %w", err)
	}

	return &session, nil
}

// GetBackupKeysForRoom 获取房间的所有备份密钥
func (kbm *KeyBackupManager) GetBackupKeysForRoom(userID, version, roomID string) (map[string]KeyBackupSession, error) {
	var records []MatrixKeyBackupSession
	if err := db.GetDB().Where("user_id = ? AND version = ? AND room_id = ?", userID, version, roomID).
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch sessions: %w", err)
	}

	sessions := make(map[string]KeyBackupSession)
	for _, record := range records {
		var session KeyBackupSession
		if err := json.Unmarshal([]byte(record.SessionData), &session); err != nil {
			continue
		}
		sessions[record.SessionID] = session
	}

	return sessions, nil
}

// DeleteBackupKey 删除单个备份密钥
func (kbm *KeyBackupManager) DeleteBackupKey(userID, version, roomID, sessionID string) error {
	result := db.GetDB().Where("user_id = ? AND version = ? AND room_id = ? AND session_id = ?",
		userID, version, roomID, sessionID).Delete(&MatrixKeyBackupSession{})
	
	if result.Error != nil {
		return fmt.Errorf("failed to delete session: %w", result.Error)
	}

	// 更新备份计数
	if result.RowsAffected > 0 {
		db.GetDB().Model(&MatrixKeyBackup{}).
			Where("user_id = ? AND version = ?", userID, version).
			UpdateColumn("count", db.GetDB().Raw("count - ?", result.RowsAffected))
	}

	return nil
}

// ListBackupVersions 列出用户的所有备份版本
func (kbm *KeyBackupManager) ListBackupVersions(userID string) ([]*KeyBackupVersion, error) {
	var records []MatrixKeyBackup
	if err := db.GetDB().Where("user_id = ?", userID).Order("created_at DESC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch backups: %w", err)
	}

	versions := make([]*KeyBackupVersion, len(records))
	for i, record := range records {
		var authData map[string]interface{}
		json.Unmarshal([]byte(record.AuthData), &authData)

		versions[i] = &KeyBackupVersion{
			Version:   record.Version,
			Algorithm: record.Algorithm,
			AuthData:  authData,
			Count:     record.Count,
			ETag:      record.ETag,
		}
	}

	return versions, nil
}

// ===== 密钥恢复令牌 =====

// RecoveryKey 恢复密钥
type RecoveryKey struct {
	UserID      string    `json:"user_id"`
	RecoveryKey string    `json:"recovery_key"` // 用户提供的恢复密钥哈希
	Version     string    `json:"version"`      // 关联的备份版本
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SetRecoveryKey 设置恢复密钥
func (kbm *KeyBackupManager) SetRecoveryKey(userID, version, recoveryKey string) error {
	// 哈希恢复密钥
	hash := sha256.Sum256([]byte(recoveryKey))
	recoveryKeyHash := base64.RawStdEncoding.EncodeToString(hash[:])

	record := &MatrixKeyBackupRecovery{
		UserID:      userID,
		Version:     version,
		RecoveryKey: recoveryKeyHash,
	}

	// 使用 Upsert
	return db.GetDB().Where("user_id = ? AND version = ?", userID, version).
		Assign(record).
		FirstOrCreate(&MatrixKeyBackupRecovery{}).Error
}

// VerifyRecoveryKey 验证恢复密钥
func (kbm *KeyBackupManager) VerifyRecoveryKey(userID, version, recoveryKey string) (bool, error) {
	// 哈希提供的恢复密钥
	hash := sha256.Sum256([]byte(recoveryKey))
	recoveryKeyHash := base64.RawStdEncoding.EncodeToString(hash[:])

	var record MatrixKeyBackupRecovery
	if err := db.GetDB().Where("user_id = ? AND version = ? AND recovery_key = ?",
		userID, version, recoveryKeyHash).First(&record).Error; err != nil {
		return false, nil
	}

	return true, nil
}

// ===== 数据库模型 =====

// MatrixKeyBackup 密钥备份版本表
type MatrixKeyBackup struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	UserID    string `gorm:"index;type:varchar(255);not null"`
	Version   string `gorm:"index;type:varchar(64);not null"`
	Algorithm string `gorm:"type:varchar(64);not null"`
	AuthData  string `gorm:"type:text;not null"`
	Count     int    `gorm:"default:0"`
	ETag      string `gorm:"type:varchar(64)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (MatrixKeyBackup) TableName() string {
	return "matrix_key_backups"
}

// MatrixKeyBackupSession 密钥备份会话表
type MatrixKeyBackupSession struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	UserID      string `gorm:"index;type:varchar(255);not null"`
	Version     string `gorm:"index;type:varchar(64);not null"`
	RoomID      string `gorm:"index;type:varchar(255);not null"`
	SessionID   string `gorm:"index;type:varchar(255);not null"`
	SessionData string `gorm:"type:text;not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (MatrixKeyBackupSession) TableName() string {
	return "matrix_key_backup_sessions"
}

// MatrixKeyBackupRecovery 密钥备份恢复表
type MatrixKeyBackupRecovery struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	UserID      string `gorm:"index;type:varchar(255);not null"`
	Version     string `gorm:"index;type:varchar(64);not null"`
	RecoveryKey string `gorm:"type:varchar(255);not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (MatrixKeyBackupRecovery) TableName() string {
	return "matrix_key_backup_recoveries"
}

// ===== 辅助函数 =====

// generateBackupVersion 生成备份版本 ID
func generateBackupVersion() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// generateETag 生成 ETag
func generateETag() string {
	b := make([]byte, 8)
	rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

// ===== 加密/解密辅助函数 =====

// encryptBackupData 加密备份数据
func encryptBackupData(plaintext, key []byte) (*KeyBackupData, error) {
	// 生成 IV
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}

	// 创建 AES-GCM 加密器
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// 加密
	ciphertext := gcm.Seal(nil, iv, plaintext, nil)

	// 分离 ciphertext 和 tag
	tag := ciphertext[len(ciphertext)-gcm.Overhead():]
	ciphertextOnly := ciphertext[:len(ciphertext)-gcm.Overhead()]

	return &KeyBackupData{
		Algorithm:  "m.megolm_backup.v1.curve25519-aes-sha2",
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertextOnly),
		MAC:        base64.RawStdEncoding.EncodeToString(tag),
		IV:         base64.RawStdEncoding.EncodeToString(iv),
	}, nil
}

// decryptBackupData 解密备份数据
func decryptBackupData(data *KeyBackupData, key []byte) ([]byte, error) {
	// 解码数据
	ciphertext, err := base64.RawStdEncoding.DecodeString(data.Ciphertext)
	if err != nil {
		return nil, err
	}

	tag, err := base64.RawStdEncoding.DecodeString(data.MAC)
	if err != nil {
		return nil, err
	}

	iv, err := base64.RawStdEncoding.DecodeString(data.IV)
	if err != nil {
		return nil, err
	}

	// 组合 ciphertext 和 tag
	ciphertextWithTag := append(ciphertext, tag...)

	// 创建 AES-GCM 解密器
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// 解密
	plaintext, err := gcm.Open(nil, iv, ciphertextWithTag, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}
