package homeserver

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// HomeServer Matrix Homeserver.
type HomeServer struct {
	federationWorkerOnce  sync.Once
	federationPeerMu      sync.Mutex
	federationPeerCursor  string
	mediaFederationOnce   sync.Once
	mediaFederationClient *FederationClient
	Config                *HSConfig
	Auth                  *AuthService
	Room                  *RoomService
	Event                 *EventService
	Sync                  *SyncService
	Account               *AccountService     // 账户服务
	KeyStore              *KeyStore           // E2EE 密钥存储
	KeyBackup             *KeyBackupManager   // 密钥备份服务
	Relations             *RelationsService   // 事件关系服务
	Push                  *PushService        // 推送通知服务
	Spaces                *SpacesService      // Matrix Spaces 服务
	AppService            *AppServiceService  // 应用服务
	RoomUpgrade           *RoomUpgradeService // 房间升级服务
	KeySharing            *KeySharingService  // 密钥共享服务
	DeviceList            *DeviceListService  // 设备列表服务
	BatchSend             *BatchSendService   // 批量发送服务
	AsyncMedia            *AsyncMediaService  // 异步媒体服务
	OpenID                *OpenIDService      // OpenID 服务
	ThreePID              *ThreePIDService    // 3PID 服务
	Admin                 *AdminService       // 管理员服务
	UserSearch            *UserSearchService  // 用户搜索服务
	WellKnown             *WellKnownService   // Well-Known 服务
	SyncNotifier          *SyncNotifier       // 同步通知器
	privateKey            ed25519.PrivateKey  // 联邦签名密钥
	publicKey             ed25519.PublicKey   // 联邦验证密钥
	keyID                 string              // 密钥标识
}

// HSConfig Homeserver 配置.
type HSConfig struct {
	Enabled        bool
	ServerName     string
	BaseURL        string
	AdminToken     string
	RoomVersion    string
	EncryptedKey   string // 用于解析社区 Token
	FederationPort int    // 联邦端口，默认 8448
}

// GlobalHS 全局 Homeserver 实例.
var GlobalHS *HomeServer

// initOnce 确保只初始化一次.
var (
	initOnce sync.Once
	errInit  error
)

// InitHomeServer 初始化 Homeserver.
func InitHomeServer() error {
	cfg := loadHSConfig()

	if !cfg.Enabled {
		return nil
	}

	// 自动迁移数据库（必须在服务初始化之前，确保表存在）
	if err := autoMigrate(); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
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

	// Finish any room join whose state was still being stored at last shutdown.
	go hs.Room.ResumePartialStateJoins()

	return nil
}

// federationPortFromEnv returns the port advertised in
// /.well-known/matrix/server. The application serves federation on its normal
// HTTPS port (behind the reverse proxy), so the default is 443. Set
// MATRIX_FEDERATION_PORT when a dedicated federation listener exists; the
// Complement harness (SERVER_NAME set) listens on 8448.
func federationPortFromEnv() int {
	if v := os.Getenv("MATRIX_FEDERATION_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	if os.Getenv("SERVER_NAME") != "" {
		return 8448
	}
	return 443
}

// loadHSConfig 加载配置.
func loadHSConfig() *HSConfig {
	cfg := &HSConfig{
		Enabled:      true, // 默认启用
		RoomVersion:  "10",
		EncryptedKey: config.C.EncryptedKey,
	}

	// SERVER_NAME env var takes highest priority (used by Complement test suite)
	if envName := os.Getenv("SERVER_NAME"); envName != "" {
		cfg.ServerName = envName
	} else {
		cfg.ServerName = systemsettings.GetDomain()
	}

	// 如果 ServerName 为空，使用默认值
	if cfg.ServerName == "" {
		cfg.ServerName = "localhost"
	}

	if config.AppBaseURLConfigured() {
		cfg.BaseURL = systemsettings.GetBaseURL()
	} else if os.Getenv("SERVER_NAME") != "" {
		cfg.BaseURL = "https://" + cfg.ServerName
	} else {
		cfg.BaseURL = systemsettings.GetBaseURL()
	}
	cfg.FederationPort = federationPortFromEnv()

	return cfg
}

// Models returns every table model owned by the Matrix homeserver. These
// tables are created by autoMigrate when the homeserver starts, not by the
// application migrations (see migrate/scripts.MigrateMatrixHSScript).
func Models() []any {
	return []any{
		&models.MatrixDMDelivery{},
		&models.MatrixDMEventJob{},
		&MatrixDMReactionReference{},
		&MatrixDMFavoriteReference{},
		&BridgeFederationDelivery{},
		&MatrixDMOutbound{},
		&BridgeConversation{},
		&BridgeConversationMember{},
		&MatrixExternalActor{},
		&MatrixDMEventReference{},
		&MatrixEvent{},
		&MatrixRoom{},
		&MatrixRoomState{},
		&MatrixRoomMember{},
		&MatrixUserSession{},
		&MatrixToDeviceMessage{},
		&MatrixSyncToDeviceBatch{},
		&MatrixToDeviceDelivery{},
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
		&MatrixRoomTag{},
		&MatrixRoomAlias{},
		&MatrixMedia{},
		&WebGroupUpload{},
		&WebGroupInvitation{},
		&MatrixThumbnail{},
		&MatrixRoomMapping{},
	}
}

// autoMigrate 自动迁移数据库.
func autoMigrate() error {
	return db.GetDB().AutoMigrate(Models()...)
}

// GetHomeServer 获取 Homeserver 实例.
func GetHomeServer() *HomeServer {
	initOnce.Do(func() {
		if GlobalHS == nil {
			errInit = InitHomeServer()
		}
	})

	return GlobalHS
}

// GetInitError 获取初始化错误（供需要检查初始化状态的调用者使用）.
func GetInitError() error {
	return errInit
}

// IsEnabled 检查是否启用.
func IsEnabled() bool {
	return GlobalHS != nil && GlobalHS.Config.Enabled
}

// NotifyToDeviceUpdate 通知用户有新的 To-Device 消息.
func (hs *HomeServer) NotifyToDeviceUpdate(userID, deviceID string) {
	if hs.SyncNotifier != nil {
		hs.SyncNotifier.Notify(userID, deviceID)
	}
}

// NotifyRoomEvent 通知房间内所有本地用户有新事件（用于联邦事件）.
func (hs *HomeServer) NotifyRoomEvent(roomID string) {
	if hs.SyncNotifier == nil {
		return
	}
	// 找到所有已 join 的本地用户
	var members []MatrixRoomMember

	db.GetDB().Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Find(&members)

	for _, m := range members {
		// 只通知本地用户（domain 匹配本服务器）
		_, domain, ok := ParseMatrixUserID(m.UserID)
		if !ok || domain != hs.Config.ServerName {
			continue
		}
		hs.SyncNotifier.Notify(m.UserID, "web_groups")
		// 通知该用户的所有设备
		var sessions []MatrixUserSession

		db.GetDB().Where("user_id = ?", m.UserID).Find(&sessions)

		for _, s := range sessions {
			hs.SyncNotifier.Notify(m.UserID, s.DeviceID)
		}
	}
}

// NotifyUser notifies all devices of a specific local user (e.g. after push rule changes).
func (hs *HomeServer) NotifyUser(userID string) {
	if hs.SyncNotifier == nil {
		return
	}

	hs.SyncNotifier.Notify(userID, "web_groups")

	var sessions []MatrixUserSession

	db.GetDB().Where("user_id = ?", userID).Find(&sessions)

	for _, s := range sessions {
		hs.SyncNotifier.Notify(userID, s.DeviceID)
	}
}

// SyncNotifier 同步通知器.
type SyncNotifier struct {
	waiters map[string][]chan struct{}
	mu      sync.RWMutex
}

// NewSyncNotifier 创建新的同步通知器.
func NewSyncNotifier() *SyncNotifier {
	return &SyncNotifier{
		waiters: make(map[string][]chan struct{}),
	}
}

// Notify 通知特定用户的设备有新数据.
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

// Wait 等待通知.
func (n *SyncNotifier) Wait(userID, deviceID string) <-chan struct{} {
	ch, _ := n.Subscribe(userID, deviceID)
	return ch
}

// Subscribe registers before a sync query and releases its waiter when that
// request finishes. A timeout, cancellation, or immediate response must not
// leave a channel retained until the next notification for an idle device.
func (n *SyncNotifier) Subscribe(userID, deviceID string) (<-chan struct{}, func()) {
	key := userID + ":" + deviceID
	ch := make(chan struct{})

	n.mu.Lock()
	n.waiters[key] = append(n.waiters[key], ch)
	n.mu.Unlock()

	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		waiters := n.waiters[key]
		for i, waiter := range waiters {
			if waiter != ch {
				continue
			}
			copy(waiters[i:], waiters[i+1:])
			waiters[len(waiters)-1] = nil
			waiters = waiters[:len(waiters)-1]
			if len(waiters) == 0 {
				delete(n.waiters, key)
			} else {
				n.waiters[key] = waiters
			}
			break
		}
		// Notify owns channel closure. Cleanup can race with Notify safely and
		// must not close another request's channel or remove a newer waiter.
	}
}
