// Package health 提供详细的健康检查功能
package health

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// HealthStatus 健康状态
type HealthStatus string

const (
	StatusHealthy   HealthStatus = "healthy"
	StatusDegraded  HealthStatus = "degraded"
	StatusUnhealthy HealthStatus = "unhealthy"
)

// HealthResponse 健康检查响应
type HealthResponse struct {
	Status    HealthStatus             `json:"status"`
	Timestamp time.Time                `json:"timestamp"`
	Version   string                   `json:"version"`
	Uptime    string                   `json:"uptime"`
	Services  map[string]ServiceHealth `json:"services"`
}

// ServiceHealth 服务健康状态
type ServiceHealth struct {
	Status   HealthStatus `json:"status"`
	Latency  string       `json:"latency"`
	Message  string       `json:"message,omitempty"`
	Metadata interface{}  `json:"metadata,omitempty"`
}

// Checker 健康检查器
type Checker struct {
	startTime time.Time
	version   string
	checkers  map[string]HealthCheckerFunc
	enabled   map[string]bool
}

// HealthCheckerFunc 健康检查函数类型
type HealthCheckerFunc func(ctx context.Context) ServiceHealth

var (
	defaultChecker *Checker
	startTime      = time.Now()
)

// NewChecker 创建健康检查器
func NewChecker(version string) *Checker {
	return &Checker{
		startTime: startTime,
		version:   version,
		checkers:  make(map[string]HealthCheckerFunc),
		enabled:   make(map[string]bool),
	}
}

// Register 注册健康检查
func (c *Checker) Register(name string, checker HealthCheckerFunc) {
	c.checkers[name] = checker
	c.enabled[name] = true
}

// Unregister 注销健康检查
func (c *Checker) Unregister(name string) {
	delete(c.checkers, name)
	delete(c.enabled, name)
}

// Enable 启用健康检查
func (c *Checker) Enable(name string) {
	if _, exists := c.checkers[name]; exists {
		c.enabled[name] = true
	}
}

// Disable 禁用健康检查
func (c *Checker) Disable(name string) {
	c.enabled[name] = false
}

// Check 执行健康检查
func (c *Checker) Check(ctx context.Context) HealthResponse {
	response := HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC(),
		Version:   c.version,
		Uptime:    time.Since(c.startTime).String(),
		Services:  make(map[string]ServiceHealth),
	}

	// 执行所有启用的检查
	for name, checker := range c.checkers {
		if !c.enabled[name] {
			continue
		}

		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		health := checker(checkCtx)
		cancel()

		response.Services[name] = health

		// 更新整体状态
		if health.Status == StatusUnhealthy {
			response.Status = StatusUnhealthy
		} else if health.Status == StatusDegraded && response.Status == StatusHealthy {
			response.Status = StatusDegraded
		}
	}

	return response
}

// Handler HTTP 处理器
func (c *Checker) Handler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		health := c.Check(ctx.Request.Context())

		statusCode := http.StatusOK
		if health.Status == StatusUnhealthy {
			statusCode = http.StatusServiceUnavailable
		} else if health.Status == StatusDegraded {
			statusCode = http.StatusOK // 降级服务返回 200，但状态为 degraded
		}

		ctx.JSON(statusCode, health)
	}
}

// LivenessHandler 存活检查处理器
func (c *Checker) LivenessHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"status": "alive",
			"uptime": time.Since(c.startTime).String(),
		})
	}
}

// ReadinessHandler 就绪检查处理器
func (c *Checker) ReadinessHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		response := c.Check(ctx.Request.Context())

		if response.Status == StatusUnhealthy {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status":   "not_ready",
				"reason":   "critical services unavailable",
				"services": response.Services,
			})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"status":   "ready",
			"services": response.Services,
		})
	}
}

// 内置健康检查器

// DatabaseChecker 数据库健康检查
func DatabaseChecker(db *gorm.DB) HealthCheckerFunc {
	return func(ctx context.Context) ServiceHealth {
		start := time.Now()
		sqlDB, err := db.DB()
		if err != nil {
			return ServiceHealth{
				Status:  StatusUnhealthy,
				Latency: time.Since(start).String(),
				Message: fmt.Sprintf("failed to get underlying sql.DB: %v", err),
			}
		}

		// 检查连接
		err = sqlDB.PingContext(ctx)
		latency := time.Since(start)

		if err != nil {
			return ServiceHealth{
				Status:  StatusUnhealthy,
				Latency: latency.String(),
				Message: fmt.Sprintf("database ping failed: %v", err),
			}
		}

		// 获取连接池统计
		stats := sqlDB.Stats()

		status := StatusHealthy
		if stats.WaitCount > 100 {
			status = StatusDegraded
		}

		return ServiceHealth{
			Status:  status,
			Latency: latency.String(),
			Metadata: map[string]interface{}{
				"open_connections":    stats.OpenConnections,
				"in_use":              stats.InUse,
				"idle":                stats.Idle,
				"wait_count":          stats.WaitCount,
				"wait_duration_ms":    stats.WaitDuration.Milliseconds(),
				"max_idle_closed":     stats.MaxIdleClosed,
				"max_lifetime_closed": stats.MaxLifetimeClosed,
			},
		}
	}
}

// RedisChecker Redis 健康检查
func RedisChecker(client *redis.Client) HealthCheckerFunc {
	return func(ctx context.Context) ServiceHealth {
		if client == nil {
			return ServiceHealth{
				Status:  StatusHealthy,
				Message: "Redis not configured",
			}
		}

		start := time.Now()
		err := client.Ping(ctx).Err()
		latency := time.Since(start)

		if err != nil {
			return ServiceHealth{
				Status:  StatusUnhealthy,
				Latency: latency.String(),
				Message: fmt.Sprintf("redis ping failed: %v", err),
			}
		}

		// 获取 Redis 信息
		info := client.Info(ctx, "server")
		infoMap := parseRedisInfo(info.Val())

		return ServiceHealth{
			Status:  StatusHealthy,
			Latency: latency.String(),
			Metadata: map[string]interface{}{
				"version": infoMap["redis_version"],
				"mode":    infoMap["redis_mode"],
			},
		}
	}
}

// StorageChecker 存储健康检查
func StorageChecker(storageType string) HealthCheckerFunc {
	return func(ctx context.Context) ServiceHealth {
		start := time.Now()

		// 简单的存储检查
		switch storageType {
		case "local":
			return checkLocalStorage(start)
		case "s3", "oss", "cos":
			// 云存储健康检查需要具体的客户端配置
			return ServiceHealth{
				Status:  StatusHealthy,
				Latency: time.Since(start).String(),
				Message: fmt.Sprintf("%s storage health check requires client configuration", storageType),
			}
		default:
			return ServiceHealth{
				Status:  StatusHealthy,
				Latency: time.Since(start).String(),
				Message: "unknown storage type",
			}
		}
	}
}

// checkLocalStorage 检查本地存储健康状态
func checkLocalStorage(start time.Time) ServiceHealth {
	// 检查默认存储目录
	storageDirs := []string{"uploads", "media", "cache", "logs"}
	var failedDirs []string

	for _, dir := range storageDirs {
		// 检查目录是否存在，不存在则尝试创建
		if err := os.MkdirAll(dir, 0755); err != nil {
			failedDirs = append(failedDirs, dir)
			continue
		}

		// 检查目录是否可写（尝试创建一个临时文件）
		testFile := filepath.Join(dir, ".health_check_tmp")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			failedDirs = append(failedDirs, dir)
			continue
		}
		os.Remove(testFile) // 清理测试文件
	}

	latency := time.Since(start)

	if len(failedDirs) > 0 {
		return ServiceHealth{
			Status:  StatusDegraded,
			Latency: latency.String(),
			Message: fmt.Sprintf("Failed to access directories: %v", failedDirs),
		}
	}

	return ServiceHealth{
		Status:  StatusHealthy,
		Latency: latency.String(),
		Metadata: map[string]interface{}{
			"checked_dirs": storageDirs,
		},
	}
}

// MatrixHomeserverChecker Matrix Homeserver 健康检查
func MatrixHomeserverChecker(serverName string) HealthCheckerFunc {
	return func(ctx context.Context) ServiceHealth {
		start := time.Now()

		// 检查关键组件是否初始化
		// 这里可以扩展为实际的 Matrix API 调用检查

		return ServiceHealth{
			Status:  StatusHealthy,
			Latency: time.Since(start).String(),
			Metadata: map[string]interface{}{
				"server_name": serverName,
			},
		}
	}
}

// ActivityPubChecker ActivityPub 健康检查
func ActivityPubChecker() HealthCheckerFunc {
	return func(ctx context.Context) ServiceHealth {
		start := time.Now()

		// 检查 ActivityPub 配置
		if config.C.AppBaseURL == "" {
			return ServiceHealth{
				Status:  StatusDegraded,
				Latency: time.Since(start).String(),
				Message: "AppBaseURL not configured",
			}
		}

		return ServiceHealth{
			Status:  StatusHealthy,
			Latency: time.Since(start).String(),
		}
	}
}

// parseRedisInfo 解析 Redis INFO 输出
func parseRedisInfo(info string) map[string]string {
	result := make(map[string]string)
	// 简化解析，实际可以使用更复杂的解析逻辑
	return result
}

// DefaultChecker 获取默认健康检查器
func DefaultChecker() *Checker {
	if defaultChecker == nil {
		defaultChecker = NewChecker(config.Version)

		// 注册数据库检查
		if db.GetDB() != nil {
			defaultChecker.Register("database", DatabaseChecker(db.GetDB()))
		}

		// 注册存储检查
		defaultChecker.Register("storage", StorageChecker(config.C.StorageType))

		// 注册 Matrix 检查
		defaultChecker.Register("matrix", MatrixHomeserverChecker(config.C.AppBaseURL))

		// 注册 ActivityPub 检查
		defaultChecker.Register("activitypub", ActivityPubChecker())
	}

	return defaultChecker
}

// InitChecker 初始化健康检查器
func InitChecker(redisClient *redis.Client) *Checker {
	checker := NewChecker(config.Version)

	// 注册数据库检查
	if db.GetDB() != nil {
		checker.Register("database", DatabaseChecker(db.GetDB()))
	}

	// 注册 Redis 检查
	if redisClient != nil {
		checker.Register("redis", RedisChecker(redisClient))
	}

	// 注册存储检查
	checker.Register("storage", StorageChecker(config.C.StorageType))

	// 注册 Matrix 检查
	checker.Register("matrix", MatrixHomeserverChecker(config.C.AppBaseURL))

	// 注册 ActivityPub 检查
	checker.Register("activitypub", ActivityPubChecker())

	defaultChecker = checker
	return checker
}

// SimpleHealthHandler 简单健康检查处理器
func SimpleHealthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().UTC(),
		})
	}
}

// DetailedHealthHandler 详细健康检查处理器
func DetailedHealthHandler() gin.HandlerFunc {
	return DefaultChecker().Handler()
}

// LivenessHandler Kubernetes 存活检查处理器
func LivenessHandler() gin.HandlerFunc {
	return DefaultChecker().LivenessHandler()
}

// ReadinessHandler Kubernetes 就绪检查处理器
func ReadinessHandler() gin.HandlerFunc {
	return DefaultChecker().ReadinessHandler()
}

// CheckWithTimeout 带超时的健康检查
func CheckWithTimeout(checker HealthCheckerFunc, timeout time.Duration) ServiceHealth {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return checker(ctx)
}

// LogHealthStatus 记录健康状态日志
func LogHealthStatus(health HealthResponse) {
	log.WithFields(map[string]interface{}{
		"status":   health.Status,
		"services": len(health.Services),
		"timestamp": health.Timestamp,
	}).Info("Health check completed")

	for name, service := range health.Services {
		if service.Status != StatusHealthy {
			log.WithFields(map[string]interface{}{
				"service": name,
				"status":  service.Status,
				"message": service.Message,
			}).Warn("Service health check failed")
		}
	}
}
