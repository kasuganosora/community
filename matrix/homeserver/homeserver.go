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
	Config         *HSConfig
	Auth           *AuthService
	Room           *RoomService
	Event          *EventService
	Sync           *SyncService
	Account        *AccountService        // 账户服务
	KeyStore       *KeyStore              // E2EE 密钥存储
	KeyBackup      *KeyBackupManager      // 密钥备份服务
	Relations      *RelationsService      // 事件关系服务
	Push           *PushService           // 推送通知服务
	Spaces         *SpacesService         // Matrix Spaces 服务
	AppService     *AppServiceService     // 应用服务
	RoomUpgrade    *RoomUpgradeService    // 房间升级服务
	KeySharing     *KeySharingService     // 密钥共享服务
	DeviceList     *DeviceListService     // 设备列表服务
	BatchSend      *BatchSendService      // 批量发送服务
	AsyncMedia     *AsyncMediaService     // 异步媒体服务
	OpenID         *OpenIDService         // OpenID 服务
	ThreePID       *ThreePIDService       // 3PID 服务
	Admin          *AdminService          // 管理员服务
	UserSearch     *UserSearchService     // 用户搜索服务
	WellKnown      *WellKnownService      // Well-Known 服务
	SyncNotifier   *SyncNotifier          // 同步通知器
	privateKey     ed25519.PrivateKey     // 联邦签名密钥
	publicKey      ed25519.PublicKey      // 联邦验证密钥
	keyID          string                 // 密钥标识
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
	hs.Account = NewAccountService(hs)
	hs.KeyStore = NewKeyStore(hs)
	hs.KeyBackup = NewKeyBackupManager(hs)
	hs.Relations = NewRelationsService(hs)
	hs.Push = NewPushService(hs)
	hs.Spaces = NewSpacesService(hs)
	hs.AppService = NewAppServiceService(hs)
	hs.RoomUpgrade = NewRoomUpgradeService(hs)
	hs.KeySharing = NewKeySharingService(hs)
	hs.DeviceList = NewDeviceListService(hs)
	hs.BatchSend = NewBatchSendService(hs)
	hs.AsyncMedia = NewAsyncMediaService(hs)
	hs.SyncNotifier = NewSyncNotifier()
	hs.OpenID = NewOpenIDService(hs)
	hs.ThreePID = NewThreePIDService(hs)
	hs.Admin = NewAdminService(hs)
	hs.UserSearch = NewUserSearchService(hs)
	hs.WellKnown = NewWellKnownService(hs)

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
		&MatrixDeviceKey{},
		&MatrixOneTimeKey{},
		&MatrixCrossSigningKey{},
		&MatrixFederationTxn{},
		&MatrixKeyBackup{},
		&MatrixKeyBackupSession{},
		&MatrixKeyBackupRecovery{},
		&MatrixSSSSSecret{},
		&MatrixSSSSDefaultKey{},
		&EventRelation{},
		&Pusher{},
		&PushRule{},
		&AppService{},
		&MatrixRoomUpgrade{},
		&RoomKeyRequest{},
		&DeviceListChange{},
		&DeviceListStream{},
		&AsyncMediaInfo{},
		&PasswordResetToken{},
		&OpenIDToken{},
		&ThreePID{},
		&ThreePIDToken{},
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

// NotifyToDeviceUpdate 通知用户有新的 To-Device 消息
func (hs *HomeServer) NotifyToDeviceUpdate(userID, deviceID string) {
	if hs.SyncNotifier != nil {
		hs.SyncNotifier.Notify(userID, deviceID)
	}
}

// SyncNotifier 同步通知器
type SyncNotifier struct {
	waiters map[string][]chan struct{}
	mu      sync.RWMutex
}

// NewSyncNotifier 创建新的同步通知器
func NewSyncNotifier() *SyncNotifier {
	return &SyncNotifier{
		waiters: make(map[string][]chan struct{}),
	}
}

// Notify 通知特定用户的设备有新数据
func (n *SyncNotifier) Notify(userID, deviceID string) {
	key := userID + ":" + deviceID
	n.mu.Lock()
	waiters := n.waiters[key]
	delete(n.waiters, key)
	n.mu.Unlock()

	for _, ch := range waiters {
		close(ch)
	}
}

// Wait 等待通知
func (n *SyncNotifier) Wait(userID, deviceID string) <-chan struct{} {
	key := userID + ":" + deviceID
	ch := make(chan struct{})
	
	n.mu.Lock()
	n.waiters[key] = append(n.waiters[key], ch)
	n.mu.Unlock()
	
	return ch
}
