package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/errors"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/health"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/matrix"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/metrics"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/migrate"
	"github.com/kasuganosora/community/models"
	_ "github.com/kasuganosora/community/notifications"
	"github.com/kasuganosora/community/runtimeconfig"
	"github.com/kasuganosora/community/search/indexsync"
	"github.com/kasuganosora/community/server"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
)

func main() {
	flag.Parse()

	var err error

	// 初始化日志
	initLogger()

	log.Info("Starting Community Server v%s", config.Version)

	// Health checks stat these paths. uploads and logs are runtime dirs;
	// media and cache are source packages and already exist in a checkout.
	for _, dir := range []string{"uploads", "logs"} {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			log.Warn("Failed to create %s: %v", dir, err)
		}
	}

	// 如果有 --rollback 参数，则回滚
	if flag.Arg(0) == "--rollback" {
		err = migrate.Down(db.GetDB())
		log.FatalIf(err)

		return
	}

	// 迁移数据库
	err = migrate.Up(db.GetDB())
	log.FatalIf(err)
	runtimeconfig.ApplyStored()
	if err = models.ReconcileFollowCounts(db.GetDB()); err != nil {
		log.Warn("Failed to reconcile follow counts: %v", err)
	}
	indexsync.Start()

	// Federation identities (actor/status URIs, acct domain) are derived from
	// APP_BASE_URL; refuse to start a production instance that would publish
	// placeholder URLs.
	if warning, err := systemsettings.CheckBaseURL(config.C.DevMode != "true"); err != nil {
		log.Fatal("Invalid instance URL configuration: %v", err)
	} else if warning != "" {
		log.Warn("%s", warning)
	}

	log.Info("Instance base URL: %s (domain %s)", systemsettings.GetBaseURL(), systemsettings.GetDomain())

	// The system.proxy service actor signs instance-level fetches; create it
	// (and its keypair) up front so https://<domain>/users/system.proxy
	// resolves for the very first signed request. Idempotent across
	// restarts and replicas; failures are retried lazily on first use.
	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 30*time.Second)
	if _, _, err := activitypub.EnsureProxyAccount(ensureCtx); err != nil {
		log.Warn("Failed to ensure system proxy actor: %v", err)
	}
	cancelEnsure()
	transferContext, stopTransfers := context.WithCancel(context.Background())
	defer stopTransfers()
	datatransfer.ResumeJobs(transferContext, db.GetDB())
	adminwebhook.Start(transferContext, db.GetDB())

	event.GetEventManager("status")

	// 初始化缓存
	cache.Init()

	// 初始化 Matrix Bridge（如果已配置启用）
	if err := matrix.Init(); err != nil {
		log.Warn("Matrix Bridge initialization failed: %v", err)
	}

	// 初始化 Matrix Homeserver（完整 Matrix 服务器实现）
	if err := homeserver.InitHomeServer(); err != nil {
		log.Warn("Matrix Homeserver initialization failed: %v", err)
	}

	// 创建 Gin 引擎
	app := setupServer()

	// 启动服务器（带优雅关闭）
	startServer(app, stopTransfers)
}

// initLogger 初始化日志配置.
func initLogger() {
	level := "info"
	if config.C.DevMode == "true" {
		level = "debug"
	}

	// 使用安全的默认配置
	logConfig := log.DefaultLogConfig()
	logConfig.Level = level

	// 生产环境启用文件日志
	if config.C.DevMode != "true" {
		logConfig.Output = "both" // 同时输出到 stdout 和文件
	}

	log.InitLogger(logConfig)

	// 记录日志配置
	log.Info("Logger initialized with rotation: max_size=%dMB, max_backups=%d, max_age=%ddays, compress=%v",
		logConfig.MaxSize, logConfig.MaxBackups, logConfig.MaxAge, logConfig.Compress)
}

// setupServer 配置 HTTP 服务器.
func setupServer() *gin.Engine {
	// 创建 Gin 引擎
	app := gin.New()

	// 启用 405 Method Not Allowed 处理。
	// Gin 默认对方法不匹配的路径返回 404，这会误导 API 客户端。
	// 设置 HandleMethodNotAllowed 后，对已注册路径使用错误的 HTTP 方法时
	// 会返回 405（由 NoMethod 处理器或默认行为响应）。
	app.HandleMethodNotAllowed = true

	// 添加 Recovery 中间件
	app.Use(gin.Recovery())

	// 安全头部中间件
	app.Use(middleware.SecurityHeadersMiddleware())

	// CORS 中间件（从配置读取允许的来源，为空则允许所有来源但不允许携带凭证）
	var corsOrigins []string
	if config.C.CORSAllowedOrigins != "" {
		corsOrigins = strings.Split(config.C.CORSAllowedOrigins, ",")
	}

	app.Use(middleware.CORSMiddleware(corsOrigins))

	// 超时控制中间件
	app.Use(middleware.TimeoutMiddleware(30 * time.Second))

	// 请求日志中间件
	app.Use(middleware.RequestLogMiddleware())

	// Request body size limit: uploads may be as large as the configured
	// media limits, everything else is capped at 10 MB.
	app.Use(middleware.BodySizeLimitMiddleware(middleware.DefaultMaxRequestBodySize, func() int64 {
		return max(systemsettings.MediaImageSizeLimit(), systemsettings.MediaVideoSizeLimit())
	}))

	// Prometheus 指标中间件
	app.Use(metrics.MetricsMiddleware())

	// 统一错误处理中间件
	app.Use(errors.ErrorHandler())

	// 设置路由
	community.SetRouter(app)

	// 限制 multipart 表单的内存缓冲区大小为 8MB，超出部分自动写入磁盘临时文件。
	// 这防止攻击者通过大体积 multipart 请求耗尽服务器内存（内存 DoS）。
	app.MaxMultipartMemory = 8 << 20 // 8 MB

	// 统一的 405 Method Not Allowed 处理器。
	// 当请求路径已注册但使用了不支持的 HTTP 方法时返回 JSON 错误，
	// 并通过 Allow 头告知客户端该路径支持的方法。
	app.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	})

	// 健康检查端点
	app.GET("/health", health.SimpleHealthHandler())
	app.GET("/health/detailed", health.DetailedHealthHandler())
	app.GET("/live", health.LivenessHandler())
	app.GET("/ready", health.ReadinessHandler())

	// Prometheus 指标端点
	app.GET("/metrics", metrics.Handler())

	// 日志统计端点（需要管理员权限）
	logsAdminGroup := app.Group("/admin/logs", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(),
		middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead}))
	logsAdminGroup.GET("/stats", log.StatsHandler())
	logsAdminGroup.GET("/health", log.HealthHandler())

	// 前端静态文件服务
	setupFrontend(app)

	return app
}

// setupFrontend 配置前端静态文件服务.
func setupFrontend(app *gin.Engine) {
	frontendDir := utils.ResolveAppPath("frontend/dist")
	if info, err := os.Stat(frontendDir); err == nil && info.IsDir() {
		app.Static("/assets", filepath.Join(frontendDir, "assets"))
		app.StaticFile("/logo.svg", filepath.Join(frontendDir, "logo.svg"))

		// SPA fallback: index.html for frontend routes only (see spa.go)
		indexPath := filepath.Join(frontendDir, "index.html")

		indexHTML, err := os.ReadFile(indexPath) //nolint:gosec // G304: reviewed and safe
		if err != nil {
			log.Warn("Failed to read index.html: %v", err)

			return
		}

		app.NoRoute(spaFallback(frontendDir, indexHTML))
		log.Info("Serving frontend from %s", frontendDir)
	} else {
		log.Info("Frontend directory not found at %s, skipping static file serving", frontendDir)
	}
}

// frontendAsset returns a built file (theme-init.js, icons, …) when the
// request matches one. SPA routes fall through to index.html. The path is
// cleaned so a request cannot escape the dist directory.
func frontendAsset(frontendDir, requestPath string) (string, bool) {
	rel := strings.TrimPrefix(requestPath, "/")
	if rel == "" || strings.Contains(rel, "\x00") {
		return "", false
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "index.html" || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}

	root, err := filepath.Abs(frontendDir)
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(root, rel)
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}

	return abs, true
}

// startServer 启动服务器（带优雅关闭）.
func startServer(app *gin.Engine, stopTransfers context.CancelFunc) {
	opts := server.DefaultOptions()
	opts.ListenAddr = config.C.Listen
	// The socket deadline must outlive the longest handler budget so an idle
	// Matrix long poll (or its timeout response) can still be written.
	opts.WriteTimeout = middleware.MatrixSyncHandlerTimeout + 5*time.Second

	// 注册关闭前钩子
	srv := server.New(app, opts)
	srv.RegisterPreShutdown(func() error { stopTransfers(); return nil })
	outbound, initErr := activitypub.StartOutboundWorker()
	log.FatalIf(initErr)
	srv.RegisterPostShutdown(func() error { outbound.Stop(); return nil })

	log.Info("Server is starting on %s", config.C.Listen)

	err := srv.Start()

	if err != nil {
		log.Fatal("Server failed: %v", err)
	}
}
