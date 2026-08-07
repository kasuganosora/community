// Package health 提供详细的健康检查功能
package health

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// 默认单次检查超时时间.
const defaultCheckTimeout = 5 * time.Second

// defaultOverallTimeout 是单次 Check 调用的整体超时上限。
// 即使存在多个检查项，整体响应也不会超过该时间，避免健康检查接口被拖垮。
const defaultOverallTimeout = 10 * time.Second

// HealthStatus 健康状态.
type HealthStatus string

const (
	StatusHealthy   HealthStatus = "healthy"
	StatusDegraded  HealthStatus = "degraded"
	StatusUnhealthy HealthStatus = "unhealthy"
)

// HealthResponse 健康检查响应.
type HealthResponse struct {
	Status    HealthStatus             `json:"status"`
	Timestamp time.Time                `json:"timestamp"`
	Version   string                   `json:"version"`
	Uptime    string                   `json:"uptime"`
	Services  map[string]ServiceHealth `json:"services"`
}

// ServiceHealth 服务健康状态.
type ServiceHealth struct {
	Status   HealthStatus `json:"status"`
	Latency  string       `json:"latency"`
	Message  string       `json:"message,omitempty"`
	Metadata any          `json:"metadata,omitempty"`
}

// Checker 健康检查器.
//
// Checker 内部使用读写锁保护 checkers/enabled 映射，允许并发注册/注销/启停检查项，
// 同时安全地并发执行 Check。所有公开方法都是 goroutine 安全的。
type Checker struct {
	startTime time.Time
	version   string
	mu        sync.RWMutex
	checkers  map[string]HealthCheckerFunc
	enabled   map[string]bool
}

// HealthCheckerFunc 健康检查函数类型.
type HealthCheckerFunc func(ctx context.Context) ServiceHealth

var (
	defaultChecker   *Checker
	defaultCheckerMu sync.Mutex
	startTime        = time.Now()
)

// NewChecker 创建健康检查器.
func NewChecker(version string) *Checker {
	return &Checker{
		startTime: startTime,
		version:   version,
		checkers:  make(map[string]HealthCheckerFunc),
		enabled:   make(map[string]bool),
	}
}

// Register 注册健康检查.
func (c *Checker) Register(name string, checker HealthCheckerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkers[name] = checker
	c.enabled[name] = true
}

// Unregister 注销健康检查.
func (c *Checker) Unregister(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.checkers, name)
	delete(c.enabled, name)
}

// Enable 启用健康检查.
func (c *Checker) Enable(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.checkers[name]; exists {
		c.enabled[name] = true
	}
}

// Disable 禁用健康检查.
func (c *Checker) Disable(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled[name] = false
}

// snapshotCheckers 在读锁保护下返回当前已启用的检查项快照。
// 返回快照后，Check 的后续执行不再持有锁，避免长时间持锁阻塞注册操作。
func (c *Checker) snapshotCheckers() map[string]HealthCheckerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := make(map[string]HealthCheckerFunc, len(c.checkers))
	for name, checker := range c.checkers {
		if c.enabled[name] {
			snapshot[name] = checker
		}
	}

	return snapshot
}

// runChecker 执行单个检查函数，并恢复可能发生的 panic，避免单个检查项崩溃导致整个
// 健康检查接口返回 500。panic 会被转换为 StatusUnhealthy 结果。
func runChecker(ctx context.Context, checker HealthCheckerFunc) (health ServiceHealth) {
	defer func() {
		if r := recover(); r != nil {
			health = ServiceHealth{
				Status:  StatusUnhealthy,
				Latency: "",
				Message: fmt.Sprintf("health check panicked: %v", r),
			}
		}
	}()

	return checker(ctx)
}

// Check 执行健康检查。
//
// 所有启用的检查项会并发执行（每个检查项独立超时），整个调用受整体超时保护。
// 单个检查项发生 panic 不会影响整体结果，会被转换为该检查项的 StatusUnhealthy。
func (c *Checker) Check(ctx context.Context) HealthResponse {
	response := HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC(),
		Version:   c.version,
		Uptime:    time.Since(c.startTime).String(),
		Services:  make(map[string]ServiceHealth),
	}

	// 整体超时控制：即使存在大量检查项或外部 ctx 缺少 deadline，
	// 健康检查接口也不会无限期阻塞。
	overallCtx, cancelOverall := context.WithTimeout(ctx, defaultOverallTimeout)
	defer cancelOverall()

	enabled := c.snapshotCheckers()
	if len(enabled) == 0 {
		return response
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for name, checker := range enabled {
		wg.Add(1)

		go func(name string, checker HealthCheckerFunc) {
			defer wg.Done()

			checkCtx, cancel := context.WithTimeout(overallCtx, defaultCheckTimeout)
			defer cancel()

			health := runChecker(checkCtx, checker)

			mu.Lock()
			response.Services[name] = health
			// 更新整体状态：unhealthy 优先级最高，其次是 degraded。
			if health.Status == StatusUnhealthy {
				response.Status = StatusUnhealthy
			} else if health.Status == StatusDegraded && response.Status == StatusHealthy {
				response.Status = StatusDegraded
			}
			mu.Unlock()
		}(name, checker)
	}

	wg.Wait()

	return response
}

// Handler HTTP 处理器.
func (c *Checker) Handler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		health := c.Check(ctx.Request.Context())

		statusCode := http.StatusOK

		switch health.Status {
		case StatusUnhealthy:
			statusCode = http.StatusServiceUnavailable
		case StatusDegraded:
			statusCode = http.StatusOK // 降级服务返回 200，但状态为 degraded
		}

		ctx.JSON(statusCode, health)
	}
}

// LivenessHandler 存活检查处理器.
func (c *Checker) LivenessHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"status": "alive", //nolint:goconst
			"uptime": time.Since(c.startTime).String(),
		})
	}
}

// ReadinessHandler 就绪检查处理器.
func (c *Checker) ReadinessHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		response := c.Check(ctx.Request.Context())

		if response.Status == StatusUnhealthy {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status":   "not_ready",
				"reason":   "critical services unavailable",
				"services": response.Services, //nolint:goconst
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

// DatabaseChecker 数据库健康检查.
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
			Metadata: map[string]any{
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

// RedisChecker Redis 健康检查.
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

		// 获取 Redis 信息（best-effort）。
		// PING 成功已足以判定 Redis 健康；INFO 仅用于补充元数据。
		// 部分 Redis 兼容实现（如 miniredis）不支持带 section 的 INFO，
		// 因此先尝试带 section 请求，失败时回退到无 section 的全量 INFO；
		// 若全量 INFO 仍失败，仍返回 healthy，仅在元数据中说明无法获取 INFO。
		info := client.Info(ctx, "server")

		if infoErr := info.Err(); infoErr != nil {
			info = client.Info(ctx)
		}

		if infoErr := info.Err(); infoErr != nil {
			return ServiceHealth{
				Status:  StatusHealthy,
				Latency: latency.String(),
				Message: fmt.Sprintf("redis ping ok, but info unavailable: %v", infoErr),
			}
		}

		infoMap := parseRedisInfo(info.Val())

		return ServiceHealth{
			Status:  StatusHealthy,
			Latency: latency.String(),
			Metadata: map[string]any{
				"version": infoMap["redis_version"],
				"mode":    infoMap["redis_mode"],
			},
		}
	}
}

// StorageChecker 存储健康检查.
func StorageChecker(storageType string) HealthCheckerFunc {
	return func(ctx context.Context) ServiceHealth {
		start := time.Now()

		// 简单的存储检查
		switch storageType {
		case "local":
			return checkLocalStorage(ctx, start)
		case "s3", "oss", "cos":
			// 云存储健康检查需要具体的客户端配置
			return ServiceHealth{
				Status:  StatusHealthy,
				Latency: time.Since(start).String(),
				Message: storageType + " storage health check requires client configuration",
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

// checkLocalStorage 检查本地存储健康状态.
func checkLocalStorage(ctx context.Context, start time.Time) ServiceHealth {
	// 检查默认存储目录
	storageDirs := []string{"uploads", "media", "cache", "logs"} //nolint:goconst

	var failedDirs []string

	for _, dir := range storageDirs {
		// 若整体/单次超时已触发，停止后续检查并直接返回已收集的结果，
		// 避免健康检查接口因大量目录检查而无限拖延。
		if err := ctx.Err(); err != nil {
			break
		}

		// 只读检查：使用 os.Stat 检查目录是否存在，不创建目录
		info, err := os.Stat(dir)
		if err != nil {
			// 任何 Stat 错误（不存在、权限不足等）都意味着目录不可访问，
			// 应记为失败，而非仅处理 os.IsNotExist 的情况。
			failedDirs = append(failedDirs, dir)
			continue
		}

		if !info.IsDir() {
			failedDirs = append(failedDirs, dir)
		}
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
		Metadata: map[string]any{
			"checked_dirs": storageDirs,
		},
	}
}

// MatrixHomeserverChecker Matrix Homeserver 健康检查.
func MatrixHomeserverChecker(serverName string) HealthCheckerFunc {
	return func(ctx context.Context) ServiceHealth {
		start := time.Now()

		// 检查关键组件是否初始化
		// 这里可以扩展为实际的 Matrix API 调用检查

		return ServiceHealth{
			Status:  StatusHealthy,
			Latency: time.Since(start).String(),
			Metadata: map[string]any{
				"server_name": serverName,
			},
		}
	}
}

// ActivityPubChecker ActivityPub 健康检查.
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

// parseRedisInfo 解析 Redis INFO 输出.
//
// 支持标准的 Redis INFO 文本格式：每行形如 "key:value"，以 "#" 开头的为注释，
// 空行被忽略。行分隔符兼容 LF 与 CRLF。缺失的字段在结果中为空字符串。
func parseRedisInfo(info string) map[string]string {
	result := make(map[string]string)

	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key != "" {
			result[key] = value
		}
	}

	return result
}

// DefaultChecker 获取默认健康检查器.
//
// 使用 defaultCheckerMu 保证 defaultChecker 的惰性初始化是 goroutine 安全的，
// 避免并发请求触发竞态条件。
func DefaultChecker() *Checker {
	defaultCheckerMu.Lock()
	defer defaultCheckerMu.Unlock()

	if defaultChecker != nil {
		return defaultChecker
	}

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

	return defaultChecker
}

// InitChecker 初始化健康检查器.
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

	defaultCheckerMu.Lock()
	defaultChecker = checker
	defaultCheckerMu.Unlock()

	return checker
}

// SimpleHealthHandler 简单健康检查处理器.
func SimpleHealthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().UTC(),
		})
	}
}

// DetailedHealthHandler 详细健康检查处理器.
func DetailedHealthHandler() gin.HandlerFunc {
	return DefaultChecker().Handler()
}

// LivenessHandler Kubernetes 存活检查处理器.
func LivenessHandler() gin.HandlerFunc {
	return DefaultChecker().LivenessHandler()
}

// ReadinessHandler Kubernetes 就绪检查处理器.
func ReadinessHandler() gin.HandlerFunc {
	return DefaultChecker().ReadinessHandler()
}

// CheckWithTimeout 带超时的健康检查.
func CheckWithTimeout(checker HealthCheckerFunc, timeout time.Duration) ServiceHealth {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return runChecker(ctx, checker)
}

// LogHealthStatus 记录健康状态日志.
func LogHealthStatus(health HealthResponse) {
	log.WithFields(map[string]any{
		"status":    health.Status,
		"services":  len(health.Services),
		"timestamp": health.Timestamp,
	}).Info("Health check completed")

	for name, service := range health.Services {
		if service.Status != StatusHealthy {
			log.WithFields(map[string]any{
				"service": name,
				"status":  service.Status,
				"message": service.Message,
			}).Warn("Service health check failed")
		}
	}
}
