package homeserver

import (
	"crypto/ed25519"
	"fmt"
	"sync"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/systemsettings"
)

// HomeServer Matrix Homeserver
type HomeServer struct {
	Config     *HSConfig
	Auth       *AuthService
	Room       *RoomService
	Event      *EventService
	Sync       *SyncService
	privateKey ed25519.PrivateKey // 联邦签名密钥
	publicKey  ed25519.PublicKey  // 联邦验证密钥
	keyID      string             // 密钥标识
}

// HSConfig Homeserver 配置
type HSConfig struct {
	Enabled      bool
	ServerName   string
	BaseURL      string
	AdminToken   string
	RoomVersion  string
	EncryptedKey string // 用于解析社区 Token
}

// GlobalHS 全局 Homeserver 实例
var GlobalHS *HomeServer

// initOnce 确保只初始化一次
var initOnce sync.Once

// InitHomeServer 初始化 Homeserver
func InitHomeServer() error {
	cfg := loadHSConfig()

	if !cfg.Enabled {
		return nil
	}

	// 加载或生成联邦密钥
	keyStorage := NewKeyStorage()
	privateKey, publicKey, keyID, err := keyStorage.LoadOrGenerateKey()
	if err != nil {
		return fmt.Errorf("failed to load or generate federation key: %w", err)
	}

	hs := &HomeServer{
		Config:     cfg,
		privateKey: privateKey,
		publicKey:  publicKey,
		keyID:      keyID,
	}

	// 初始化服务
	hs.Auth = NewAuthService(hs)
	hs.Room = NewRoomService(hs)
	hs.Event = NewEventService(hs)
	hs.Sync = NewSyncService(hs)

	GlobalHS = hs

	// 自动迁移数据库
	if err := autoMigrate(); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	return nil
}

// loadHSConfig 加载配置
func loadHSConfig() *HSConfig {
	cfg := &HSConfig{
		Enabled:      true, // 默认启用
		ServerName:   systemsettings.GetDomain(),
		RoomVersion:  "10",
		EncryptedKey: config.C.EncryptedKey,
	}
	
	// 如果 ServerName 为空，使用默认值
	if cfg.ServerName == "" {
		cfg.ServerName = "localhost"
	}
	
	cfg.BaseURL = fmt.Sprintf("https://%s", cfg.ServerName)
	
	return cfg
}

// autoMigrate 自动迁移数据库
func autoMigrate() error {
	return db.GetDB().AutoMigrate(
		&MatrixEvent{},
		&MatrixRoom{},
		&MatrixRoomState{},
		&MatrixRoomMember{},
		&MatrixUserSession{},
		&MatrixToDeviceMessage{},
		&MatrixReceipt{},
		&MatrixAccountData{},
		&MatrixTyping{},
		&MatrixFilter{},
		&MatrixPresence{},
		&MatrixFederationKey{},
		&MatrixServerKey{},
	)
}

// GetHomeServer 获取 Homeserver 实例
func GetHomeServer() *HomeServer {
	initOnce.Do(func() {
		if GlobalHS == nil {
			InitHomeServer()
		}
	})
	return GlobalHS
}

// IsEnabled 检查是否启用
func IsEnabled() bool {
	return GlobalHS != nil && GlobalHS.Config.Enabled
}
