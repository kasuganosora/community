// Package server 提供 HTTP 服务器管理和优雅关闭功能
package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/log"
)

// Server HTTP 服务器.
type Server struct {
	httpServer *http.Server
	router     *gin.Engine
	shutdown   chan os.Signal
	options    Options
}

// Options 服务器配置选项.
type Options struct {
	ListenAddr      string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	PreShutdown     []func() error
	PostShutdown    []func() error
}

// DefaultOptions 默认服务器配置.
func DefaultOptions() Options {
	return Options{
		ListenAddr:      ":8080",
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    30 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 30 * time.Second,
		PreShutdown:     make([]func() error, 0),
		PostShutdown:    make([]func() error, 0),
	}
}

// New 创建服务器.
func New(router *gin.Engine, opts Options) *Server {
	return &Server{
		router:   router,
		options:  opts,
		shutdown: make(chan os.Signal, 1),
	}
}

// NewWithDefault 使用默认配置创建服务器.
func NewWithDefault(router *gin.Engine) *Server {
	return New(router, DefaultOptions())
}

// RegisterPreShutdown 注册关闭前执行的函数.
func (s *Server) RegisterPreShutdown(fn func() error) {
	s.options.PreShutdown = append(s.options.PreShutdown, fn)
}

// RegisterPostShutdown 注册关闭后执行的函数.
func (s *Server) RegisterPostShutdown(fn func() error) {
	s.options.PostShutdown = append(s.options.PostShutdown, fn)
}

// Start 启动服务器.
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:         s.options.ListenAddr,
		Handler:      s.router,
		ReadTimeout:  s.options.ReadTimeout,
		WriteTimeout: s.options.WriteTimeout,
		IdleTimeout:  s.options.IdleTimeout,
	}

	// 设置信号监听
	signal.Notify(s.shutdown, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// 启动服务器
	errChan := make(chan error, 1)

	go func() {
		log.Info("Server starting on %s", s.options.ListenAddr)

		err := s.httpServer.ListenAndServe()

		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	// 等待信号或错误
	select {
	case err := <-errChan:
		return err
	case sig := <-s.shutdown:
		log.Info("Received signal: %v", sig)

		return s.Shutdown()
	}
}

// Shutdown 优雅关闭服务器.
func (s *Server) Shutdown() error {
	log.Info("Shutting down server...")

	// 执行关闭前钩子
	for _, fn := range s.options.PreShutdown {
		err := fn()
		if err != nil {
			log.Error("Pre-shutdown hook failed: %v", err)
		}
	}

	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), s.options.ShutdownTimeout)
	defer cancel()

	// 关闭 HTTP 服务器
	err := s.httpServer.Shutdown(ctx)
	if err != nil {
		log.Error("Server forced to shutdown: %v", err)

		return err
	}

	log.Info("Server shutdown completed")

	// 执行关闭后钩子
	for _, fn := range s.options.PostShutdown {
		err := fn()
		if err != nil {
			log.Error("Post-shutdown hook failed: %v", err)
		}
	}

	return nil
}

// GracefulStart 带优雅关闭的服务器启动.
func GracefulStart(router *gin.Engine, opts Options) error {
	server := New(router, opts)

	// 注册默认的关闭钩子
	server.RegisterPreShutdown(func() error {
		log.Info("Closing database connections...")

		return database.CloseDB()
	})

	// 关闭 Redis 连接
	server.RegisterPreShutdown(func() error {
		log.Info("Closing Redis connections...")

		cRedis := db.GetRedis()
		if cRedis != nil {
			return cRedis.Close()
		}

		return nil
	})

	return server.Start()
}

// SetupGracefulShutdown 设置优雅关闭.
func SetupGracefulShutdown(server *http.Server, shutdownTimeout time.Duration, cleanupFuncs ...func()) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-quit
		log.Info("Received signal: %v", sig)

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// 执行清理函数
		for _, fn := range cleanupFuncs {
			fn()
		}

		err := server.Shutdown(ctx)
		if err != nil {
			log.Fatal("Server forced to shutdown: %v", err)
		}

		log.Info("Server exited")
	}()
}

// RunServer 运行服务器（简化版）.
func RunServer(router *gin.Engine, addr string, cleanupFuncs ...func()) {
	srv := &http.Server{ //nolint:gosec // G112: reviewed and safe
		Addr:    addr,
		Handler: router,
	}

	SetupGracefulShutdown(srv, 30*time.Second, cleanupFuncs...)

	log.Info("Server starting on %s", addr)

	err := srv.ListenAndServe()

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Server start failed: %v", err)
	}
}
