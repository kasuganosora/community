package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community"
	"github.com/kasuganosora/community/config"
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
	"github.com/kasuganosora/community/server"
	"github.com/kasuganosora/community/utils"
	_ "github.com/kasuganosora/community/notifications"
)

func main() {
	flag.Parse()
	var err error

	// 初始化日志
	initLogger()

	log.Info("Starting Community Server v%s", config.Version)

	// 如果有 --rollback 参数，则回滚
	if flag.Arg(0) == "--rollback" {
		err = migrate.Down(db.GetDB())
		log.FatalIf(err)
		return
	}

	// 迁移数据库
	err = migrate.Up(db.GetDB())
	log.FatalIf(err)

	event.GetEventManager("status")

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
	startServer(app)
}

// initLogger 初始化日志配置
func initLogger() {
	level := "info"
	if config.C.DevMode == "true" {
		level = "debug"
	}

	log.InitLogger(log.LogConfig{
		Level:      level,
		Output:     "stdout",
		FilePath:   "logs/community.log",
		MaxSize:    100,
		MaxBackups: 7,
		MaxAge:     30,
		Compress:   true,
		Format:     "text",
	})
}

// setupServer 配置 HTTP 服务器
func setupServer() *gin.Engine {
	// 创建 Gin 引擎
	app := gin.New()

	// 添加 Recovery 中间件
	app.Use(gin.Recovery())

	// 安全头部中间件
	app.Use(middleware.SecurityHeadersMiddleware())

	// CORS 中间件（允许所有来源，生产环境应配置具体来源）
	app.Use(middleware.CORSMiddleware([]string{}))

	// 超时控制中间件
	app.Use(middleware.TimeoutMiddleware(30 * time.Second))

	// 请求日志中间件
	app.Use(middleware.RequestLogMiddleware())

	// Prometheus 指标中间件
	app.Use(metrics.MetricsMiddleware())

	// 统一错误处理中间件
	app.Use(errors.ErrorHandler())

	// 设置路由
	community.SetRouter(app)

	// 健康检查端点
	app.GET("/health", health.SimpleHealthHandler())
	app.GET("/health/detailed", health.DetailedHealthHandler())
	app.GET("/live", health.LivenessHandler())
	app.GET("/ready", health.ReadinessHandler())

	// Prometheus 指标端点
	app.GET("/metrics", metrics.Handler())

	// 前端静态文件服务
	setupFrontend(app)

	return app
}

// setupFrontend 配置前端静态文件服务
func setupFrontend(app *gin.Engine) {
	frontendDir := utils.GetFullPath("frontend/dist")
	if info, err := os.Stat(frontendDir); err == nil && info.IsDir() {
		app.Static("/assets", filepath.Join(frontendDir, "assets"))
		app.StaticFile("/logo.svg", filepath.Join(frontendDir, "logo.svg"))

		// SPA fallback: serve index.html for all non-API routes
		indexPath := filepath.Join(frontendDir, "index.html")
		indexHTML, err := os.ReadFile(indexPath)
		if err != nil {
			log.Warn("Failed to read index.html: %v", err)
			return
		}

		app.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path
			// Don't serve frontend for API, OAuth, ActivityPub, Matrix routes
			if strings.HasPrefix(path, "/api/") ||
				strings.HasPrefix(path, "/oauth/") ||
				strings.HasPrefix(path, "/.well-known/") ||
				strings.HasPrefix(path, "/users/") ||
				strings.HasPrefix(path, "/inbox") ||
				strings.HasPrefix(path, "/_matrix/") ||
				strings.HasPrefix(path, "/health") ||
				strings.HasPrefix(path, "/live") ||
				strings.HasPrefix(path, "/ready") ||
				strings.HasPrefix(path, "/metrics") {
				c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
		})
		log.Info("Serving frontend from %s", frontendDir)
	} else {
		log.Info("Frontend directory not found at %s, skipping static file serving", frontendDir)
	}
}

// startServer 启动服务器（带优雅关闭）
func startServer(app *gin.Engine) {
	opts := server.DefaultOptions()
	opts.ListenAddr = config.C.Listen

	// 注册关闭前钩子
	srv := server.New(app, opts)

	log.Info("Server is starting on %s", config.C.Listen)
	if err := srv.Start(); err != nil {
		log.Fatal("Server failed: %v", err)
	}
}
