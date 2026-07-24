package matrix

import (
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
)

// Config Matrix Bridge 配置
// 用户只需配置 3 个核心变量：MATRIX_HOMESERVER_URL, MATRIX_AS_TOKEN, MATRIX_HS_TOKEN
// ServerName 默认使用系统设置的站点域名，Prefix 使用简短的 "_c_".
type Config struct {
	// Enabled 是否启用 Matrix Bridge
	Enabled bool `default:"false" env:"MATRIX_ENABLED"`

	// HomeserverURL Matrix Homeserver 地址 (如 https://matrix.example.com)
	HomeserverURL string `default:"" env:"MATRIX_HOMESERVER_URL"`

	// ServerName Matrix 服务器名称 (默认使用系统设置的站点域名)
	ServerName string `default:"" env:"MATRIX_SERVER_NAME"`

	// ASToken Application Service Token，用于向 HS 认证
	ASToken string `default:"" env:"MATRIX_AS_TOKEN"`

	// HSToken Homeserver Token，用于验证来自 HS 的请求
	HSToken string `default:"" env:"MATRIX_HS_TOKEN"`

	// BridgeUsername Bridge 机器人的本地用户名部分 (默认 "_bridge")
	BridgeUsername string `default:"_bridge" env:"MATRIX_BRIDGE_USERNAME"`

	// UserNamespacePrefix 用户命名空间前缀 (默认 "_c_")
	UserNamespacePrefix string `default:"_c_" env:"MATRIX_USER_NAMESPACE"`

	// RoomNamespacePrefix 房间别名命名空间前缀 (默认 "_c_")
	RoomNamespacePrefix string `default:"_c_" env:"MATRIX_ROOM_NAMESPACE"`
}

// GlobalConfig 全局 Matrix 配置实例.
var (
	GlobalConfig Config
	configOnce   sync.Once
)

// LoadConfig 从环境变量加载配置，自动推导未设置的值
// 使用 sync.Once 保证线程安全且只执行一次.
func LoadConfig() {
	configOnce.Do(func() {
		GlobalConfig = Config{
			Enabled:             getEnvBool("MATRIX_ENABLED", false),
			HomeserverURL:       getEnvString("MATRIX_HOMESERVER_URL", ""),
			ServerName:          getEnvString("MATRIX_SERVER_NAME", ""),
			ASToken:             getEnvString("MATRIX_AS_TOKEN", ""),
			HSToken:             getEnvString("MATRIX_HS_TOKEN", ""),
			BridgeUsername:      getEnvString("MATRIX_BRIDGE_USERNAME", "_bridge"),
			UserNamespacePrefix: getEnvString("MATRIX_USER_NAMESPACE", "_c_"),
			RoomNamespacePrefix: getEnvString("MATRIX_ROOM_NAMESPACE", "_c_"),
		}

		// ServerName 默认使用系统设置的站点域名
		if GlobalConfig.ServerName == "" {
			GlobalConfig.ServerName = systemsettings.GetDomain()
		}
	})
}

// IsEnabled 检查 Matrix Bridge 是否启用且配置完整.
func (c *Config) IsEnabled() bool {
	return c.Enabled && c.HomeserverURL != "" && c.ServerName != "" && c.ASToken != "" && c.HSToken != ""
}

// GetUserNamespaceRegex 获取用户命名空间的正则表达式.
func (c *Config) GetUserNamespaceRegex() *regexp.Regexp {
	pattern := "^@" + regexp.QuoteMeta(c.UserNamespacePrefix) + "(.+):" + regexp.QuoteMeta(c.ServerName) + "$"

	return regexp.MustCompile(pattern)
}

// GetBridgeUserID 获取 Bridge 机器人的完整 Matrix ID.
func (c *Config) GetBridgeUserID() string {
	return "@" + c.BridgeUsername + ":" + c.ServerName
}

// FormatMatrixUserID 格式化 Matrix 用户 ID.
func (c *Config) FormatMatrixUserID(localpart string) string {
	return utils.FormatMatrixUserID(localpart, c.ServerName)
}

// ParseMatrixUserID 解析 Matrix 用户 ID，返回 localpart 和 server.
func ParseMatrixUserID(userID string) (localpart, server string, ok bool) {
	return utils.ParseMatrixUserID(userID)
}

// ParseMatrixRoomID 解析 Matrix 房间 ID.
func ParseMatrixRoomID(roomID string) (localpart, server string, ok bool) {
	return utils.ParseMatrixRoomID(roomID)
}

// Helper functions

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	return strings.ToLower(value) == "true" || value == "1"
}
