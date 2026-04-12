package matrix

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/log"
)

// Bridge Matrix Bridge 核心服务
// 协调各组件，管理 Bridge 的生命周期
type Bridge struct {
	config     *Config
	client     *ASClient
	userMapper *UserMapper
	roomMapper *RoomMapper
	msgBridge  *MessageBridge
	started    atomic.Bool
	stopChan   chan struct{}
	stopOnce   sync.Once
}

// NewBridge 创建 Bridge 实例
func NewBridge() *Bridge {
	LoadConfig()
	return &Bridge{
		config:   &GlobalConfig,
		client:   NewASClient(),
		stopChan: make(chan struct{}),
	}
}

// NewBridgeWithConfig 使用指定配置创建 Bridge
func NewBridgeWithConfig(cfg *Config) *Bridge {
	return &Bridge{
		config:   cfg,
		client:   NewASClientWithConfig(cfg),
		stopChan: make(chan struct{}),
	}
}

// Start 启动 Bridge
func (b *Bridge) Start() error {
	if !b.config.IsEnabled() {
		log.Info("Matrix Bridge is disabled, skipping start")
		return nil
	}

	// 测试与 Homeserver 的连接
	if err := b.client.PingHS(); err != nil {
		return err
	}

	// 初始化各组件
	b.userMapper = NewUserMapper(b.client)
	b.roomMapper = NewRoomMapper(b.client, b.userMapper)
	b.msgBridge = NewMessageBridge(b.client, b.userMapper, b.roomMapper)

	// 确保 Bridge 用户存在
	if err := b.client.EnsureVirtualUser(b.config.BridgeUsername); err != nil {
		return err
	}

	b.started.Store(true)
	log.Info("Matrix Bridge started successfully")
	return nil
}

// Stop 停止 Bridge
func (b *Bridge) Stop() {
	if !b.started.Load() {
		return
	}

	b.stopOnce.Do(func() {
		close(b.stopChan)
	})
	b.started.Store(false)
	log.Info("Matrix Bridge stopped")
}

// IsStarted 检查 Bridge 是否已启动
func (b *Bridge) IsStarted() bool {
	return b.started.Load()
}

// HandleTransaction 处理来自 Homeserver 的 Transaction
func (b *Bridge) HandleTransaction(txnID string, events []Event) error {
	if !b.started.Load() {
		return nil
	}

	log.Info("Processing Matrix transaction %s with %d events", txnID, len(events))

	for _, event := range events {
		if err := b.handleEvent(event); err != nil {
			log.Error("Failed to handle Matrix event %s: %v", event.EventID, err)
			// 继续处理其他事件
		}
	}

	return nil
}

// handleEvent 处理单个 Matrix 事件
func (b *Bridge) handleEvent(event Event) error {
	switch event.Type {
	case EventTypeRoomMessage:
		return b.msgBridge.HandleMatrixMessage(&event)

	case EventTypeRoomMember:
		return b.handleMemberEvent(&event)

	case EventTypeRoomCreate:
		// 房间创建事件，通常不需要特殊处理
		return nil

	default:
		// 忽略其他事件类型
		return nil
	}
}

// handleMemberEvent 处理成员事件
func (b *Bridge) handleMemberEvent(event *Event) error {
	content := event.ToRoomMemberContent()
	if content == nil {
		return nil
	}

	// 根据成员状态更新用户信息
	switch content.Membership {
	case MembershipJoin:
		// 用户加入房间，更新档案信息
		if content.DisplayName != "" || content.AvatarURL != "" {
			return b.userMapper.UpdateUserProfile(
				event.Sender,
				content.DisplayName,
				content.AvatarURL,
			)
		}
	case MembershipLeave, MembershipBan:
		// 用户离开或被封禁，可以在这里清理资源
	}

	return nil
}

// SendToMatrix 将本地消息发送到 Matrix
func (b *Bridge) SendToMatrix(convID uint64, senderAccountID uint64, content string) (string, error) {
	if !b.started.Load() {
		return "", nil
	}

	eventID, err := b.msgBridge.SendLocalMessageToMatrix(convID, senderAccountID, content)
	if err != nil {
		return "", err
	}

	return eventID, nil
}

// GetUserMapper 获取用户映射服务
func (b *Bridge) GetUserMapper() *UserMapper {
	return b.userMapper
}

// GetRoomMapper 获取房间映射服务
func (b *Bridge) GetRoomMapper() *RoomMapper {
	return b.roomMapper
}

// GetMessageBridge 获取消息桥接服务
func (b *Bridge) GetMessageBridge() *MessageBridge {
	return b.msgBridge
}

// GetConfig 获取配置
func (b *Bridge) GetConfig() *Config {
	return b.config
}

// GetClient 获取 AS 客户端
func (b *Bridge) GetClient() *ASClient {
	return b.client
}

// timeNow 辅助函数
var timeNow = func() time.Time {
	return time.Now()
}
