package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/kasuganosora/community/clientip"
	"github.com/kasuganosora/community/controllers/api/streaming"
	"github.com/kasuganosora/community/db/writebuffer"
	"github.com/kasuganosora/community/secretredact"
	"github.com/kasuganosora/community/serializer"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community"
	"github.com/kasuganosora/community/accountlifecycle"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/controllers/api/admin"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
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
	"github.com/kasuganosora/community/moderation"
	_ "github.com/kasuganosora/community/notifications"
	"github.com/kasuganosora/community/profileverify"
	"github.com/kasuganosora/community/runtimeconfig"
	"github.com/kasuganosora/community/search/indexsync"
	"github.com/kasuganosora/community/server"
	"github.com/kasuganosora/community/setup"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/userwebhook"
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

	// Boot decision: setup wizard (no database configured and never
	// installed), maintenance (installed but configuration or database
	// missing) or normal operation. See package setup.
	if flag.Arg(0) != "--rollback" {
		switch mode, reason := setup.Decide(); mode {
		case setup.ModeSetup:
			log.FatalIf(setup.Run(config.C.Listen, utils.ResolveAppPath("frontend/dist")))
			return
		case setup.ModeMaintenance:
			log.Error("Installed marker found in %s but no database is configured in %s; refusing to start setup", setup.ConfigDir(), config.EnvFile())
			log.FatalIf(setup.RunMaintenance(config.C.Listen, reason, nil, 0))
			return
		}
		if database.InitError() != nil || db.GetDB() == nil {
			log.FatalIf(setup.RunMaintenance(config.C.Listen, "database_unavailable", func() bool {
				config.Reload()
				database.InitGorm()
				return database.InitError() == nil && db.GetDB() != nil
			}, 15*time.Second))
			return
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
	// Deployments configured through .env before the wizard existed get the
	// installed marker here, so they can never enter setup mode later.
	if wrote, markerErr := setup.EnsureInstalledMarker(); markerErr != nil {
		log.Warn("Could not write the installed marker in %s: %v", setup.ConfigDir(), markerErr)
	} else if wrote {
		log.Info("Installed marker written to %s", setup.ConfigDir())
	}
	runtimeconfig.ApplyStored()
	if names := admin.ShadowedByProcessEnv(os.LookupEnv, admin.StoredSettingKey); len(names) > 0 {
		log.Warn("These settings are stored in the database but still set in the process environment, which overrides them: %s. Remove them from BOTH the host compose env_file (.env) and /app/config/.env, then recreate the container.", strings.Join(names, ", "))
	}
	role, feedMode, feedOptions, err := feedRuntimeOptions()
	log.FatalIf(err)
	serviceContext, stopServices := context.WithCancel(context.Background())
	defer stopServices()
	feedWorker := startFeedRuntime(serviceContext, role, feedMode, feedOptions)
	if role == "feed-worker" {
		metricsServer, metricsErr := startWorkerMetrics(runtimeValue("FEED_METRICS_LISTEN", ""))
		log.FatalIf(metricsErr)
		defer stopWorkerMetrics(metricsServer)
		log.Info("Feed worker started (scan=%d, delivery=%d)", feedOptions.ScanConcurrency, feedOptions.DeliveryConcurrency)
		waitFeedWorker(stopServices, feedWorker)
		flushCtx, cancelFlush := context.WithTimeout(context.Background(), 10*time.Second)
		if err := indexsync.Shutdown(flushCtx); err != nil {
			log.Warn("search index queue not fully flushed: %v", err)
		}
		cancelFlush()
		return
	}
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
	// The instance actor (<base>/actor) subscribes to relays and receives
	// their Accepts; create it the same way.
	if _, _, err := activitypub.EnsureInstanceActor(ensureCtx); err != nil {
		log.Warn("Failed to ensure instance actor: %v", err)
	}
	cancelEnsure()
	transferContext, stopTransfers := context.WithCancel(serviceContext)
	defer stopTransfers()
	datatransfer.ResumeJobs(transferContext, db.GetDB())
	adminwebhook.Start(transferContext, db.GetDB())
	userwebhook.Start(transferContext, db.GetDB())
	accountlifecycle.Start(transferContext, db.GetDB())
	profileverify.Start(transferContext, db.GetDB())
	moderation.StartInactivityChecker(serviceContext)

	event.GetEventManager("status")

	// 初始化缓存
	cache.Init()
	startDiscoveryRuntime(serviceContext)

	// 初始化 Matrix Bridge（如果已配置启用）
	if err := matrix.Init(); err != nil {
		log.Warn("Matrix Bridge initialization failed: %v", err)
	}

	// 初始化 Matrix Homeserver（完整 Matrix 服务器实现）
	if err := homeserver.InitHomeServer(); err != nil {
		log.Warn("Matrix Homeserver initialization failed: %v", err)
	}
	if homeserver.GlobalHS != nil {
		homeserver.GlobalHS.StartNativeFederation(serviceContext)
		homeserver.GlobalHS.StartDirectMessageBridge(serviceContext)
	}

	// 创建 Gin 引擎
	app := setupServer()

	// 启动服务器（带优雅关闭）
	startServer(app, func() {
		stopTransfers()
		stopServices()
		streaming.CloseFeedTopics()
		if feedWorker != nil {
			feedWorker.Wait()
		}
	})
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

	// Believe X-Real-IP / X-Forwarded-For only from trusted reverse proxies,
	// so clients cannot forge the address used by rate limits and logs.
	if err := clientip.Configure(app); err != nil {
		log.Fatal("%v", err)
	}

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

	// Compress API/federation JSON (brotli or gzip); outside the timeout
	// wrapper so the encoder is closed after the handler finished.
	app.Use(middleware.APICompression())
	// Inside compression so it sees plain bytes: error bodies never echo the
	// request's own token, whatever error text produced them.
	app.Use(secretredact.Middleware())

	// 超时控制中间件
	app.Use(middleware.TimeoutMiddleware(30 * time.Second))
	app.Use(middleware.FeedAdmissionMiddleware())

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

	// The setup wizard exists only before installation.
	setupGone := func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "Record not found"}) }
	app.Any("/api/setup/*any", setupGone)
	app.GET("/setup", setupGone)
	app.GET("/setup/*any", setupGone)
	app.GET("/setup.html", setupGone)

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
	registerBrandingRoutes(app)
	frontendDir := utils.ResolveAppPath("frontend/dist")
	if info, err := os.Stat(frontendDir); err == nil && info.IsDir() {
		files := frontendFileHandler(frontendDir, setupAssetArchive(frontendDir))
		app.GET("/assets/*filepath", files)
		app.HEAD("/assets/*filepath", files)
		app.GET("/logo.svg", files)
		app.HEAD("/logo.svg", files)

		// The build id in version.json versions non-hashed files (?v=).
		if raw, err := os.ReadFile(filepath.Join(frontendDir, "version.json")); err == nil { //nolint:gosec // G304: our dist directory
			var v struct {
				Build string `json:"build"`
			}
			if json.Unmarshal(raw, &v) == nil {
				systemsettings.SetStaticVersion(v.Build)
			}
		}

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
	instanceMeta := activitypub.StartInstanceMetadataWorker()
	srv.RegisterPostShutdown(func() error { instanceMeta.Stop(); return nil })
	outbound, initErr := activitypub.StartOutboundWorker()
	log.FatalIf(initErr)
	srv.RegisterPostShutdown(func() error { outbound.Stop(); return nil })
	serializer.PollViewed = activitypub.MaybeRepairRemotePoll
	inboxQueue := activitypub.StartInboxQueue()
	srv.RegisterPostShutdown(func() error { inboxQueue.Stop(); return nil })
	announceRetry := activitypub.StartAnnounceRetryWorker()
	srv.RegisterPostShutdown(func() error { announceRetry.Stop(); return nil })
	pollClose := activitypub.StartPollCloseWorker()
	srv.RegisterPostShutdown(func() error { pollClose.Stop(); return nil })
	// Federation-tier row updates (delivery status, blurhash) are batched.
	fedWrites := writebuffer.Start(writebuffer.New(db.GetDB, writebuffer.DefaultInterval))
	srv.RegisterPostShutdown(func() error { fedWrites.Stop(); return nil })
	// Search-index writes are batched in memory for a few seconds; send the
	// last batch before exiting.
	srv.RegisterPostShutdown(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := indexsync.Shutdown(ctx); err != nil {
			log.Warn("search index queue not fully flushed: %v", err)
		}
		return nil
	})

	log.Info("Server is starting on %s", config.C.Listen)

	err := srv.Start()

	if err != nil {
		log.Fatal("Server failed: %v", err)
	}
}
