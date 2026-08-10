package server

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// freeAddr 返回一个当前可用的监听地址（"host:port" 形式）。
// 通过让内核分配端口（":0"）保证测试之间、以及并发运行时不会端口冲突。
func freeAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	return addr
}

// waitForServerReady 轮询直到服务器开始接受连接，或超时失败。
// 用于在调用 Start() 后可靠地等待 http.Server 完成绑定。
// 它对任意 HTTP 响应（包括 404）都视为就绪——只要 TCP 连接成功即可。
func waitForServerReady(t *testing.T, addr string) {
	t.Helper()

	client := &http.Client{Timeout: 200 * time.Millisecond}
	url := "http://" + addr + "/__probe__"

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx // 测试探测请求，短连接无需 context
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("server at %s did not become ready in time", addr)
}

// newTestRouter 构造一个用于测试的最小 gin 引擎，带一个探测路由。
func newTestRouter() *gin.Engine {
	r := gin.New()
	r.GET("/__probe__", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	return r
}

// resetSignals 清除当前进程上对指定信号的所有 signal.Notify 注册。
// 用于在依赖真实进程信号的测试之前清理之前测试残留的泄漏注册
// （SetupGracefulShutdown / RunServer 内部不调用 signal.Stop）。
func resetSignals(t *testing.T) {
	t.Helper()
	signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
}

// =============================================================================
// DefaultOptions / New / NewWithDefault
// =============================================================================

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	assert.Equal(t, ":8080", opts.ListenAddr)
	assert.Equal(t, 10*time.Second, opts.ReadTimeout)
	assert.Equal(t, 30*time.Second, opts.WriteTimeout)
	assert.Equal(t, 120*time.Second, opts.IdleTimeout)
	assert.Equal(t, 30*time.Second, opts.ShutdownTimeout)
	assert.NotNil(t, opts.PreShutdown)
	assert.NotNil(t, opts.PostShutdown)
	assert.Empty(t, opts.PreShutdown)
	assert.Empty(t, opts.PostShutdown)
}

func TestNew(t *testing.T) {
	r := newTestRouter()
	opts := DefaultOptions()
	opts.ListenAddr = ":9999"

	s := New(r, opts)

	require.NotNil(t, s)
	assert.Equal(t, r, s.router)
	assert.Equal(t, ":9999", s.options.ListenAddr)
	assert.NotNil(t, s.shutdownCh)
	// Start 之前 httpServer 应为 nil
	assert.Nil(t, s.httpServer)
	// closed 标志初始为 false
	assert.False(t, s.closed.Load())
}

func TestNewWithDefault(t *testing.T) {
	r := newTestRouter()

	s := NewWithDefault(r)

	require.NotNil(t, s)
	assert.Equal(t, ":8080", s.options.ListenAddr)
	assert.Equal(t, 30*time.Second, s.options.ShutdownTimeout)
	assert.Equal(t, r, s.router)
}

// =============================================================================
// RegisterPreShutdown / RegisterPostShutdown
// =============================================================================

func TestRegisterPreShutdown(t *testing.T) {
	s := NewWithDefault(newTestRouter())
	called := false

	s.RegisterPreShutdown(func() error {
		called = true
		return nil
	})

	require.Len(t, s.options.PreShutdown, 1)

	err := s.options.PreShutdown[0]()
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRegisterPostShutdown(t *testing.T) {
	s := NewWithDefault(newTestRouter())
	called := false

	s.RegisterPostShutdown(func() error {
		called = true
		return nil
	})

	require.Len(t, s.options.PostShutdown, 1)

	err := s.options.PostShutdown[0]()
	require.NoError(t, err)
	assert.True(t, called)
}

// 注册 nil 应被忽略，不追加到钩子切片。
func TestRegisterPreShutdown_NilIgnored(t *testing.T) {
	s := NewWithDefault(newTestRouter())
	s.RegisterPreShutdown(nil)
	assert.Empty(t, s.options.PreShutdown)
}

func TestRegisterPostShutdown_NilIgnored(t *testing.T) {
	s := NewWithDefault(newTestRouter())
	s.RegisterPostShutdown(nil)
	assert.Empty(t, s.options.PostShutdown)
}

// =============================================================================
// Shutdown: 幂等性
// =============================================================================

func TestShutdown_Idempotent(t *testing.T) {
	s := NewWithDefault(newTestRouter())

	err1 := s.Shutdown()
	require.NoError(t, err1, "first shutdown should succeed")

	// 第二次调用应返回 ErrAlreadyShutdown
	err2 := s.Shutdown()
	assert.ErrorIs(t, err2, ErrAlreadyShutdown)
}

// 未调用 Start 时 Shutdown 不 panic，且 httpServer 为 nil 时仍能正常完成。
func TestShutdown_NoHttpServer(t *testing.T) {
	s := NewWithDefault(newTestRouter())

	err := s.Shutdown()
	// 没有 httpServer，没有钩子错误，应返回 nil。
	assert.NoError(t, err)
}

// =============================================================================
// Shutdown: 钩子执行顺序 (Pre -> HTTP -> Post)
// =============================================================================

func TestShutdown_HookOrder(t *testing.T) {
	var seq atomic.Int32 // 记录执行顺序的递增计数器
	var preOrder, postOrder int32

	s := NewWithDefault(newTestRouter())
	s.RegisterPreShutdown(func() error {
		preOrder = seq.Add(1)
		return nil
	})
	s.RegisterPostShutdown(func() error {
		postOrder = seq.Add(1)
		return nil
	})

	err := s.Shutdown()
	require.NoError(t, err)

	// Pre 钩子必须在 Post 钩子之前执行。
	assert.Equal(t, int32(1), preOrder, "Pre-shutdown hook must run first")
	assert.Equal(t, int32(2), postOrder, "Post-shutdown hook must run after pre")
}

// 多个钩子都应被依次执行（容错：单个失败不中断后续）。
func TestShutdown_MultipleHooksAllExecuted(t *testing.T) {
	var calls atomic.Int32

	s := NewWithDefault(newTestRouter())
	for i := 0; i < 3; i++ {
		s.RegisterPreShutdown(func() error {
			calls.Add(1)
			return nil
		})
		s.RegisterPostShutdown(func() error {
			calls.Add(1)
			return nil
		})
	}

	err := s.Shutdown()
	require.NoError(t, err)
	assert.Equal(t, int32(6), calls.Load(), "all 3 pre + 3 post hooks must run")
}

// =============================================================================
// Shutdown: 钩子错误聚合
// =============================================================================

func TestShutdown_HookErrorAggregation(t *testing.T) {
	preErr := errors.New("pre failed")
	postErr := errors.New("post failed")

	s := NewWithDefault(newTestRouter())
	s.RegisterPreShutdown(func() error { return preErr })
	s.RegisterPostShutdown(func() error { return postErr })

	err := s.Shutdown()
	require.Error(t, err)

	// errors.Join 应同时包含两个钩子的错误。
	assert.ErrorIs(t, err, preErr)
	assert.ErrorIs(t, err, postErr)
}

// 一个钩子失败不应阻止后续钩子执行。
func TestShutdown_HookFailureContinuesOthers(t *testing.T) {
	var secondCalled atomic.Bool

	s := NewWithDefault(newTestRouter())
	s.RegisterPreShutdown(func() error { return errors.New("first failed") })
	s.RegisterPreShutdown(func() error {
		secondCalled.Store(true)
		return nil
	})

	err := s.Shutdown()
	require.Error(t, err)
	assert.True(t, secondCalled.Load(), "second hook should still run after first failed")
}

// =============================================================================
// Shutdown: 超时配置兜底 (0/负值回退到 30s)
// =============================================================================

func TestShutdown_NegativeTimeoutFallback(t *testing.T) {
	// ShutdownTimeout 为负值时应回退到 30s 默认值。
	// 通过带真实 httpServer 的关闭来间接验证：负超时不会导致
	// context 立即取消从而令 Shutdown 瞬间失败。
	addr := freeAddr(t)
	opts := DefaultOptions()
	opts.ListenAddr = addr
	opts.ShutdownTimeout = -1 * time.Second // 非法值

	s := New(newTestRouter(), opts)

	// 手动构造 httpServer 并绑定，模拟 Start 的副作用但不进入 Start 的阻塞循环。
	// 这让我们直接测试 Shutdown 在负超时下的行为。
	hs := &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	go func() {
		_ = hs.ListenAndServe()
	}()

	waitForServerReady(t, addr)

	s.mu.Lock()
	s.httpServer = hs
	s.mu.Unlock()

	err := s.Shutdown()
	// 负超时回退到 30s，正常关闭应成功。
	assert.NoError(t, err, "negative timeout should fall back to 30s default")
}

func TestShutdown_ZeroTimeoutFallback(t *testing.T) {
	addr := freeAddr(t)
	opts := DefaultOptions()
	opts.ListenAddr = addr
	opts.ShutdownTimeout = 0 // 零值，非法

	s := New(newTestRouter(), opts)
	hs := &http.Server{Addr: addr, Handler: s.router}

	go func() {
		_ = hs.ListenAndServe()
	}()

	waitForServerReady(t, addr)

	s.mu.Lock()
	s.httpServer = hs
	s.mu.Unlock()

	err := s.Shutdown()
	assert.NoError(t, err, "zero timeout should fall back to 30s default")
}

// =============================================================================
// Start: 通过 shutdownCh 触发优雅关闭（不依赖进程级信号）
// =============================================================================

func TestStart_ShutdownChannelTriggersShutdown(t *testing.T) {
	addr := freeAddr(t)
	opts := DefaultOptions()
	opts.ListenAddr = addr
	opts.ShutdownTimeout = 2 * time.Second

	s := New(newTestRouter(), opts)

	startErr := make(chan error, 1)

	go func() {
		startErr <- s.Start()
	}()

	waitForServerReady(t, addr)

	// 直接向 shutdownCh 发送信号触发关闭（等价于收到 OS 信号）
	s.shutdownCh <- syscall.SIGINT

	select {
	case err := <-startErr:
		// 信号触发关闭应返回 nil（无错误）
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after signal within 5s")
	}

	// 关闭后 closed 标志应为 true
	assert.True(t, s.closed.Load())
}

func TestStart_StartupError(t *testing.T) {
	// 绑定一个已占用的端口，使 ListenAndServe 立即失败。
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = l.Close() }()

	occupiedAddr := l.Addr().String()

	opts := DefaultOptions()
	opts.ListenAddr = occupiedAddr
	opts.ShutdownTimeout = time.Second

	s := New(newTestRouter(), opts)

	// Start 应在 ListenAndServe 失败后返回错误并执行清理。
	done := make(chan error, 1)

	go func() {
		done <- s.Start()
	}()

	select {
	case err := <-done:
		// 启动失败应返回非 nil 错误（端口已占用）。
		// 注意：错误不应是 http.ErrServerClosed（那是正常关闭）。
		require.Error(t, err)
		assert.NotErrorIs(t, err, http.ErrServerClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return startup error within 5s")
	}

	// 启动失败后应已触发清理，Shutdown 成为幂等的二次调用。
	err2 := s.Shutdown()
	assert.ErrorIs(t, err2, ErrAlreadyShutdown)
}

// =============================================================================
// Start: httpServer 超时配置正确传递
// =============================================================================

func TestStart_TimeoutConfigPropagation(t *testing.T) {
	addr := freeAddr(t)
	opts := Options{
		ListenAddr:      addr,
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 2 * time.Second,
		PreShutdown:     make([]func() error, 0),
		PostShutdown:    make([]func() error, 0),
	}

	s := New(newTestRouter(), opts)

	startErr := make(chan error, 1)

	go func() {
		startErr <- s.Start()
	}()

	waitForServerReady(t, addr)

	// 验证 Start 创建的 httpServer 正确携带了超时配置。
	s.mu.Lock()
	httpServer := s.httpServer
	s.mu.Unlock()

	require.NotNil(t, httpServer)
	assert.Equal(t, addr, httpServer.Addr)
	assert.Equal(t, 5*time.Second, httpServer.ReadTimeout)
	assert.Equal(t, 15*time.Second, httpServer.WriteTimeout)
	assert.Equal(t, 60*time.Second, httpServer.IdleTimeout)
	assert.NotNil(t, httpServer.Handler)

	// 清理：通过 shutdownCh 触发关闭
	s.shutdownCh <- syscall.SIGTERM

	select {
	case <-startErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after signal within 5s")
	}
}

// =============================================================================
// Start: 路由注册验证（端到端 HTTP 请求）
// =============================================================================

func TestStart_RoutesAccessible(t *testing.T) {
	router := gin.New()
	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	router.POST("/echo", func(c *gin.Context) {
		c.String(http.StatusOK, "received")
	})

	addr := freeAddr(t)
	opts := DefaultOptions()
	opts.ListenAddr = addr
	opts.ShutdownTimeout = 2 * time.Second

	s := New(router, opts)

	go func() {
		_ = s.Start()
	}()

	waitForServerReady(t, addr)

	client := &http.Client{Timeout: 1 * time.Second}
	base := "http://" + addr

	// GET /ping
	resp, err := client.Get(base + "/ping") //nolint:noctx
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// POST /echo
	resp2, err := client.Post(base+"/echo", "text/plain", nil) //nolint:noctx
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	_ = resp2.Body.Close()

	// 通过 shutdownCh 关闭
	s.shutdownCh <- syscall.SIGINT
}

// =============================================================================
// Start: shutdownCh 接收不同信号（SIGINT / SIGTERM / SIGHUP）
// =============================================================================

func TestStart_ReceivesSIGHUP(t *testing.T) {
	addr := freeAddr(t)
	opts := DefaultOptions()
	opts.ListenAddr = addr
	opts.ShutdownTimeout = 2 * time.Second

	s := New(newTestRouter(), opts)

	startErr := make(chan error, 1)

	go func() {
		startErr <- s.Start()
	}()

	waitForServerReady(t, addr)

	// SIGHUP 也在监听列表中
	s.shutdownCh <- syscall.SIGHUP

	select {
	case err := <-startErr:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after SIGHUP within 5s")
	}
}

// =============================================================================
// runHooks (内部辅助函数，通过同一包直接测试)
// =============================================================================

func TestRunHooks_ContinuesOnError(t *testing.T) {
	s := NewWithDefault(newTestRouter())

	var count atomic.Int32
	hooks := []func() error{
		func() error {
			count.Add(1)
			return errors.New("e1")
		},
		func() error {
			count.Add(1)
			return nil
		},
		func() error {
			count.Add(1)
			return errors.New("e3")
		},
	}

	err := s.runHooks("test", hooks)

	require.Error(t, err)
	assert.Equal(t, int32(3), count.Load(), "all hooks must execute despite errors")
}

func TestRunHooks_NilEntriesSkipped(t *testing.T) {
	s := NewWithDefault(newTestRouter())

	var count atomic.Int32
	hooks := []func() error{
		nil, // 应被跳过
		func() error {
			count.Add(1)
			return nil
		},
		nil, // 应被跳过
	}

	err := s.runHooks("test", hooks)
	require.NoError(t, err)
	assert.Equal(t, int32(1), count.Load(), "only non-nil hooks should run")
}

func TestRunHooks_Empty(t *testing.T) {
	s := NewWithDefault(newTestRouter())

	err := s.runHooks("test", nil)
	assert.NoError(t, err)
}

// =============================================================================
// SetupGracefulShutdown: 通过真实信号触发关闭并执行 cleanup
// =============================================================================
//
// 这些测试需要向进程发送真实的 OS 信号（SetupGracefulShutdown 内部使用
// 独立的 signal.Notify 通道，无法从外部注入）。关键点：
//   - signal.Notify 注册会抑制信号的默认行为（终止进程），所以只要存在注册，
//     SIGTERM 不会杀死测试进程。
//   - 测试开始前调用 signal.Reset 清理之前测试残留的泄漏注册（源码中
//     SetupGracefulShutdown / RunServer 不调用 signal.Stop）。

func TestSetupGracefulShutdown_SignalTriggersCleanup(t *testing.T) {
	// 清理残留的信号注册，确保只有本次注册生效。
	resetSignals(t)

	addr := freeAddr(t)
	srv := &http.Server{
		Addr:         addr,
		Handler:      newTestRouter(),
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}

	var cleanup1, cleanup2 atomic.Bool

	SetupGracefulShutdown(srv, 2*time.Second,
		func() { cleanup1.Store(true) },
		nil, // nil cleanup 应被跳过而不 panic
		func() { cleanup2.Store(true) },
		nil,
	)

	go func() {
		_ = srv.ListenAndServe()
	}()

	waitForServerReady(t, addr)

	// 发送 SIGTERM 触发优雅关闭。
	require.NoError(t, sendSignal(syscall.SIGTERM))

	// 等待 cleanup 执行完毕（带超时避免死锁）
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cleanup1.Load() && cleanup2.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.True(t, cleanup1.Load(), "first cleanup function must run on shutdown")
	assert.True(t, cleanup2.Load(), "second cleanup function must run on shutdown")
}

func TestSetupGracefulShutdown_NegativeTimeoutFallback(t *testing.T) {
	resetSignals(t)

	addr := freeAddr(t)
	srv := &http.Server{
		Addr:    addr,
		Handler: newTestRouter(),
	}

	var cleanupCalled atomic.Bool

	SetupGracefulShutdown(srv, -5*time.Second, func() {
		cleanupCalled.Store(true)
	})

	go func() {
		_ = srv.ListenAndServe()
	}()

	waitForServerReady(t, addr)

	require.NoError(t, sendSignal(syscall.SIGTERM))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cleanupCalled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 负超时回退到 30s，cleanup 应正常执行（未因 context 立即取消而失败）。
	assert.True(t, cleanupCalled.Load(), "cleanup should run even with negative timeout (falls back to 30s)")
}

// =============================================================================
// GracefulStart: 钩子注册与关闭流程
// =============================================================================
//
// GracefulStart 内部创建 Server 并调用 Start()，注册 database/redis 的
// PostShutdown 钩子。在测试环境中 database.DB 为 nil（CloseDB 安全返回 nil），
// cRedis.GetRedises() 返回空切片，因此钩子不会 panic。

func TestGracefulStart_SignalTriggersShutdown(t *testing.T) {
	resetSignals(t)

	addr := freeAddr(t)
	opts := DefaultOptions()
	opts.ListenAddr = addr
	opts.ShutdownTimeout = 2 * time.Second

	router := gin.New()
	router.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	startDone := make(chan error, 1)

	go func() {
		startDone <- GracefulStart(router, opts)
	}()

	waitForServerReady(t, addr)

	// 验证路由可达
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz") //nolint:noctx
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// 发送 SIGTERM 触发 GracefulStart 的优雅关闭。
	require.NoError(t, sendSignal(syscall.SIGTERM))

	select {
	case err := <-startDone:
		// 正常关闭应返回 nil。
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("GracefulStart did not return after signal within 5s")
	}
}

// =============================================================================
// 并发安全：多 goroutine 同时调用 Shutdown
// =============================================================================

func TestShutdown_ConcurrentCalls(t *testing.T) {
	s := NewWithDefault(newTestRouter())

	const n = 10
	errs := make([]error, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = s.Shutdown()
		}(i)
	}

	wg.Wait()

	// 有且仅有一个调用返回 nil（第一个成功），其余返回 ErrAlreadyShutdown。
	successCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		} else {
			assert.ErrorIs(t, e, ErrAlreadyShutdown)
		}
	}

	assert.Equal(t, 1, successCount, "exactly one Shutdown call should succeed")
}

// =============================================================================
// 辅助函数
// =============================================================================

// sendSignal 向当前进程发送指定的信号。
func sendSignal(sig os.Signal) error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return p.Signal(sig)
}
