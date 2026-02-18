package matrix

import (
	"sync"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

var (
	globalBridge     *Bridge
	globalBridgeOnce sync.Once
	globalBridgeMu   sync.RWMutex
	statusHandlerId  int
)

// Init 初始化 Matrix Bridge（在应用启动时自动调用）
// 如果配置中未启用 Matrix，则跳过初始化
func Init() error {
	var initErr error
	globalBridgeOnce.Do(func() {
		LoadConfig()
		if !GlobalConfig.IsEnabled() {
			log.Info("Matrix Bridge is disabled, skipping initialization")
			return
		}

		bridge := NewBridge()
		if err := bridge.Start(); err != nil {
			log.Error("Failed to start Matrix Bridge: %v", err)
			initErr = err
			return
		}

		globalBridgeMu.Lock()
		globalBridge = bridge
		globalBridgeMu.Unlock()

		// 注册 Status 事件监听器
		registerStatusEventHandler()

		log.Info("Matrix Bridge initialized successfully")
	})

	return initErr
}

// registerStatusEventHandler 注册 Status 事件处理器
func registerStatusEventHandler() {
	statusHandlerId = event.Register("status", "after_create", &event.Handler{
		Exec: func(e event.Event) bool {
			status, ok := e.Data.(*models.Status)
			if !ok || status == nil {
				return true // 继续执行其他处理器
			}

			// 只处理 direct 消息（私信）
			if status.Visibility != models.StatusVisibilityDirect {
				return true
			}

			// 异步发送到 Matrix，不阻塞主流程
			go sendDirectMessageToMatrix(status)

			return true
		},
	})

	log.Info("Matrix Bridge: registered status event handler (id=%d)", statusHandlerId)
}

// sendDirectMessageToMatrix 发送私信到 Matrix
func sendDirectMessageToMatrix(status *models.Status) {
	bridge := GetBridge()
	if bridge == nil || !bridge.IsStarted() {
		return
	}

	// 检查是否有 ConversationId
	if status.ConversationId == nil {
		return
	}

	eventID, err := bridge.SendToMatrix(*status.ConversationId, status.AccountId, status.Content.Content)
	if err != nil {
		log.Error("Matrix Bridge: failed to send direct message: %v", err)
		return
	}

	if eventID != "" {
		log.Debug("Matrix Bridge: sent message to Matrix, event_id=%s", eventID)
	}
}

// GetBridge 获取全局 Bridge 实例
func GetBridge() *Bridge {
	globalBridgeMu.RLock()
	defer globalBridgeMu.RUnlock()
	return globalBridge
}

// IsEnabled 检查 Matrix Bridge 是否已启用
func IsEnabled() bool {
	return GlobalConfig.IsEnabled()
}

// IsRunning 检查 Matrix Bridge 是否正在运行
func IsRunning() bool {
	globalBridgeMu.RLock()
	defer globalBridgeMu.RUnlock()
	return globalBridge != nil && globalBridge.IsStarted()
}

// Shutdown 关闭 Matrix Bridge（在应用关闭时调用）
func Shutdown() {
	globalBridgeMu.Lock()
	defer globalBridgeMu.Unlock()

	// 移除事件监听器
	if statusHandlerId > 0 {
		event.Remove("status", "after_create", statusHandlerId)
		log.Info("Matrix Bridge: unregistered status event handler")
	}

	if globalBridge != nil {
		globalBridge.Stop()
		globalBridge = nil
	}
}
