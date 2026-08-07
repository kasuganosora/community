// Package server 提供 HTTP 服务器管理和优雅关闭功能。
//
// 该模块封装了 HTTP 服务器的生命周期：启动、信号监听（SIGINT/SIGTERM）、
// 优雅关闭（带超时）以及关闭钩子（关闭 HTTP -> 关闭 DB -> 关闭 Redis 等
// 依赖资源，严格按依赖顺序释放）。
package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/log"
)

// ErrAlreadyShutdown 表示对一个已经关闭（或正在关闭）的服务器重复调用 Shutdown。
var ErrAlreadyShutdown = errors.New("server already shutdown")

// Server HTTP 服务器.
type Server struct {
	mu         sync.Mutex   // 保护 httpServer / shutdownCh / closed 的并发访问
	httpServer *http.Server // 受 mu 保护
	router     *gin.Engine
	options    Options

	// shutdownCh 用于接收信号 / 显式触发关闭。带缓冲避免发送方阻塞。
	shutdownCh chan os.Signal

	// closed 记录服务器是否已进入关闭流程，保证 Shutdown 幂等。
	closed atomic.Bool
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
		router:     router,
		options:    opts,
		shutdownCh: make(chan os.Signal, 1),
	}
}

// NewWithDefault 使用默认配置创建服务器.
func NewWithDefault(router *gin.Engine) *Server {
	return New(router, DefaultOptions())
}

// RegisterPreShutdown 注册关闭前执行的函数.
func (s *Server) RegisterPreShutdown(fn func() error) {
	if fn == nil {
		return
	}

	s.options.PreShutdown = append(s.options.PreShutdown, fn)
}

// RegisterPostShutdown 注册关闭后执行的函数.
func (s *Server) RegisterPostShutdown(fn func() error) {
	if fn == nil {
		return
	}

	s.options.PostShutdown = append(s.options.PostShutdown, fn)
}

// Start 启动服务器，阻塞直到收到关闭信号或启动失败。
//
// 信号监听在函数返回时通过 signal.Stop 解除注册，避免通道泄漏。
func (s *Server) Start() error {
	s.mu.Lock()
	httpServer := &http.Server{
		Addr:         s.options.ListenAddr,
		Handler:      s.router,
		ReadTimeout:  s.options.ReadTimeout,
		WriteTimeout: s.options.WriteTimeout,
		IdleTimeout:  s.options.IdleTimeout,
	}
	s.httpServer = httpServer
	s.mu.Unlock()

	// 设置信号监听；Start 返回时通过 signal.Stop 解除对 s.shutdownCh 的绑定，
	// 避免每次 Start 都累积一个泄漏的通道注册（包级 runtime 信号状态会永久持有它）。
	signal.Notify(s.shutdownCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(s.shutdownCh)

	// errChan 缓冲为 1，确保 goroutine 在无人接收时也能写入并退出，不会泄漏。
	errChan := make(chan error, 1)

	go func() {
		log.Info("Server starting on %s", s.options.ListenAddr)

		// 使用闭包捕获的局部副本，避免与 Shutdown 中的读操作构成数据竞争。
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// errChan 已缓冲，写入后 goroutine 立即退出，即使主流程
			// 走了信号关闭路径而不再读取 errChan，也不会永久阻塞。
			errChan <- err
		}
	}()

	// 等待信号或启动错误
	select {
	case err := <-errChan:
		// 启动失败：尽力执行清理，避免泄漏已分配资源。
		_ = s.Shutdown()

		return err
	case sig := <-s.shutdownCh:
		log.Info("Received signal: %v", sig)

		return s.Shutdown()
	}
}

// Shutdown 优雅关闭服务器。
//
// 关闭顺序（严格遵循组件依赖，先停流量入口再释放底层资源）：
//  1. PreShutdown 钩子（HTTP 仍在运行前的准备工作）；
//  2. http.Server.Shutdown(ctx)（带超时，停止接收新连接、等待在途请求完成）；
//  3. PostShutdown 钩子（HTTP 已停止后才关闭 DB / Redis 等依赖资源）。
//
// 该方法是幂等的：多次调用只会执行一次实际的关闭流程。
func (s *Server) Shutdown() error {
	// 原子地标记关闭状态，保证幂等。
	if !s.closed.CompareAndSwap(false, true) {
		return ErrAlreadyShutdown
	}

	log.Info("Shutting down server...")

	// 执行关闭前钩子（容错：记录错误但继续关闭流程）。
	preErr := s.runHooks("Pre-shutdown", s.options.PreShutdown)

	// 创建带超时的上下文。对 0 / 负值做兜底，避免 context 立即取消导致
	// 关闭流程瞬间失败（典型场景：调用方未显式配置 ShutdownTimeout）。
	timeout := s.options.ShutdownTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 关闭 HTTP 服务器（停止接收新请求，等待在途请求完成）。
	s.mu.Lock()
	httpServer := s.httpServer
	s.mu.Unlock()

	var httpErr error

	if httpServer != nil {
		httpErr = httpServer.Shutdown(ctx)
		if httpErr != nil {
			log.Error("Server forced to shutdown: %v", httpErr)
		}
	} else {
		log.Warn("Shutdown called but httpServer is nil (Start not called?)")
	}

	log.Info("Server shutdown completed")

	// 执行关闭后钩子（HTTP 已停止后释放 DB / Redis 等依赖资源）。
	postErr := s.runHooks("Post-shutdown", s.options.PostShutdown)

	// 汇总所有错误返回；优先返回最关键的 HTTP 关闭错误。
	return errors.Join(preErr, httpErr, postErr)
}

// runHooks 顺序执行一组关闭钩子，收集所有错误。
// 单个钩子失败不会中断后续钩子，错误会被记录并汇总返回。
func (s *Server) runHooks(name string, hooks []func() error) error {
	var errs []error

	for _, fn := range hooks {
		if fn == nil {
			continue
		}

		if err := fn(); err != nil {
			log.Error("%s hook failed: %v", name, err)
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// GracefulStart 带优雅关闭的服务器启动。
//
// 注册默认的资源清理钩子（关闭 DB / Redis），按正确的依赖顺序执行：
// HTTP 关闭之后才关闭 DB / Redis（PostShutdown），避免在途请求访问已关闭的连接。
func GracefulStart(router *gin.Engine, opts Options) error {
	server := New(router, opts)

	// 关闭数据库连接（HTTP 停止后再关，保证在途请求仍可访问 DB）
	server.RegisterPostShutdown(func() error {
		log.Info("Closing database connections...")
		// database.CloseDB 内部已处理 DB == nil 的情况，安全调用。
		return database.CloseDB()
	})

	// 关闭 Redis 连接（HTTP 停止后再关）
	server.RegisterPostShutdown(func() error {
		log.Info("Closing Redis connections...")
		// 遍历所有已配置的 Redis 客户端逐个关闭。
		// 使用 cRedis.GetRedises() 而非 db.GetRedis()，因为后者在未配置 Redis
		// 时会 panic（redises 为空），而 GetRedises() 仅返回空切片。
		clients := cRedis.GetRedises()
		if len(clients) == 0 {
			return nil
		}

		var errs []error
		for i, c := range clients {
			if c == nil {
				continue
			}

			if err := c.Close(); err != nil {
				log.Error("Redis client[%d] close failed: %v", i, err)
				errs = append(errs, err)
			}
		}

		return errors.Join(errs...)
	})

	return server.Start()
}

// SetupGracefulShutdown 为给定的 *http.Server 注册信号监听并执行优雅关闭。
//
// 关闭顺序：先 http.Server.Shutdown(ctx)（停止接收新请求、等待在途请求），
// 再执行 cleanupFuncs（释放 DB / Redis 等底层资源），严格遵循依赖顺序。
//
// 注意：Shutdown 失败时仅记录错误而非 log.Fatal，避免跳过后续 cleanupFuncs。
func SetupGracefulShutdown(server *http.Server, shutdownTimeout time.Duration, cleanupFuncs ...func()) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	// Start 的调用方通常以 ListenAndServe 阻塞，无法在返回后调用 signal.Stop，
	// 但 quit 为局部通道且带缓冲，进程退出时随之一并回收，不存在长期泄漏。

	go func() {
		sig := <-quit
		log.Info("Received signal: %v", sig)

		// 对 0 / 负值做兜底，避免 context 立即取消。
		if shutdownTimeout <= 0 {
			shutdownTimeout = 30 * time.Second
		}

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)

		// 先关闭 HTTP 服务器（停止接收新请求），再释放底层资源，
		// 保证在途请求能够正常完成（它们可能仍需访问 DB / Redis）。
		err := server.Shutdown(ctx)
		cancel()

		if err != nil {
			// 不使用 log.Fatal（会 os.Exit 跳过后续清理），改为记录错误后继续执行 cleanupFuncs。
			log.Error("Server forced to shutdown: %v", err)
		} else {
			log.Info("Server exited gracefully")
		}

		// 执行清理函数（HTTP 已停止后才释放 DB / Redis 等资源）
		for _, fn := range cleanupFuncs {
			if fn == nil {
				continue
			}

			fn()
		}
	}()
}

// RunServer 运行服务器（简化版）.
func RunServer(router *gin.Engine, addr string, cleanupFuncs ...func()) {
	srv := &http.Server{ //nolint:gosec // G112: reviewed and safe
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  10 * time.Second, // 防止 Slowloris 等慢速攻击耗尽连接
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	SetupGracefulShutdown(srv, 30*time.Second, cleanupFuncs...)

	log.Info("Server starting on %s", addr)

	err := srv.ListenAndServe()

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Server start failed: %v", err)
	}
}
