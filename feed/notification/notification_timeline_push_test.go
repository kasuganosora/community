package notification

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-redis/redismock/v9"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 测试环境（SQLite 内存库 + redismock 注入的全局 redis）
// ---------------------------------------------------------------------------

// integrationModels 是 notification 时间线相关数据模型，用于自动迁移。
var integrationModels = []any{
	&models.Notification{},
	&models.Account{},
	&models.AccountExtra{},
}

// notifTestEnv 持有测试环境所需的全部句柄.
type notifTestEnv struct {
	gormDB    *gorm.DB
	client    *redis.Client
	mock      redismock.ClientMock
	mockRedis *feed.MockRedisClient // feed 包的 mock redis，用于控制 PushUpdateRequired/PushToClient
}

// setupNotifEnv 准备全局 DB（SQLite 内存库）与注入的 mock redis 客户端.
// notification_timeline 中的 Push/push/trim/Rebuild 通过 db.GetDB() 与 db.GetRedis()
// 访问全局句柄，因此必须注入可用的全局 DB 和 redis.
func setupNotifEnv(t *testing.T) *notifTestEnv {
	t.Helper()

	gormDB, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// SQLite :memory: 需要单连接以确保所有查询共享同一个内存数据库.
	sqlDB, err := gormDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, gormDB.AutoMigrate(integrationModels...))

	// 注入全局 DB，使 db.GetDB() 返回测试 DB（通过 database 包设置）.
	database.SetTestDB(gormDB)
	t.Cleanup(func() {
		database.ResetTestDB()
	})

	// 注入 mock redis（通过 redis 包的 SetRedisesForTest）.
	client, mock := redismock.NewClientMock()
	// 关闭严格顺序：不同分支下 redis 调用数量/顺序可能不同.
	mock.MatchExpectationsInOrder(false)

	saved := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})

	// 同时设置 feed 包的默认 service 为基于 feed.MockRedisClient 的实例，
	// 以便控制 PushUpdateRequired / PushToClient 的行为.
	mockRedis := feed.NewMockRedisClient()
	feed.SetDefaultService(feed.NewFeedService(mockRedis))
	feed.ResetSubscriptionCache()

	t.Cleanup(func() {
		// 等待 init() 注册的异步 goroutine（如有）完成.
		time.Sleep(50 * time.Millisecond)
		cRedis.SetRedisesForTest(saved)
		_ = sqlDB.Close()
	})

	return &notifTestEnv{gormDB: gormDB, client: client, mock: mock, mockRedis: mockRedis}
}

// makeNotification 构造一个带 account 的通知.
func makeNotification(id, accountId uint64, notifType uint8) *models.Notification {
	return &models.Notification{
		Id:        id,
		AccountId: accountId,
		Type:      notifType,
		Account:   &models.Account{Id: accountId},
	}
}

// --- GetKeyFormat / GetTimelineId -----------------------------------------

func TestGetKeyFormat_Constant(t *testing.T) {
	tl := &NotificationTimeline{}
	assert.Equal(t, "feed:notification:%s", tl.GetKeyFormat())
}

func TestGetTimelineId_VariousAccounts(t *testing.T) {
	cases := []struct {
		id  uint64
		exp string
	}{
		{1, "1"},
		{0, "0"},
		{42, "42"},
		{1<<63 - 1, fmt.Sprintf("%d", uint64(1<<63-1))},
	}
	for _, c := range cases {
		tl := &NotificationTimeline{Account: &models.Account{Id: c.id}}
		assert.Equal(t, c.exp, tl.GetTimelineId())
	}
}

func TestNotificationTimeline_SatisfiesFeedTimelineInterface(t *testing.T) {
	// 编译期保证 NotificationTimeline 实现了 feed.Timeline 接口.
	var _ feed.Timeline = (*NotificationTimeline)(nil)
	var tl feed.Timeline = &NotificationTimeline{Account: &models.Account{Id: 7}}
	assert.Equal(t, "feed:notification:%s", tl.GetKeyFormat())
	assert.Equal(t, "7", tl.GetTimelineId())
}

// --- Push（顶层导出函数） -------------------------------------------------

// Push 在 notification.Account == nil 且全局 DB 为 nil 时应返回 error.
func TestPush_NilAccountAndNilDB_ReturnsError(t *testing.T) {
	// 确保全局 DB 为 nil（不调用 setupNotifEnv）.
	n := &models.Notification{Id: 1, AccountId: 1} // Account == nil

	err := Push(n, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database connection is nil")
}

// Push 在 notification.Account != nil 时会直接调用 timeline.push.
func TestPush_WithAccount_ZAddSucceeds(t *testing.T) {
	env := setupNotifEnv(t)

	n := makeNotification(100, 1, models.NotificationTypeMention)

	env.mock.ExpectZAdd("feed:notification:1", redis.Z{
		Score: float64(100), Member: uint64(100),
	}).SetVal(1)

	err := Push(n, false, false)
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// Push 配合 trim=true 时，成功 ZAdd 后应再调用 trim -> ZRemRangeByRank.
func TestPush_WithAccount_AndTrim(t *testing.T) {
	env := setupNotifEnv(t)

	n := makeNotification(200, 5, models.NotificationTypeFavourite)

	env.mock.ExpectZAdd("feed:notification:5", redis.Z{
		Score: float64(200), Member: uint64(200),
	}).SetVal(1)
	env.mock.ExpectZRemRangeByRank("feed:notification:5", int64(0), int64(-feed.MAX_ITEMS)).SetVal(0)

	err := Push(n, false, true)
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// Push 在 ZAdd 失败时应直接返回 error，不再调用 trim.
func TestPush_WithAccount_ZAddFails(t *testing.T) {
	env := setupNotifEnv(t)

	n := makeNotification(300, 9, models.NotificationTypeReblog)

	env.mock.ExpectZAdd("feed:notification:9", redis.Z{
		Score: float64(300), Member: uint64(300),
	}).SetErr(errors.New("redis boom"))

	err := Push(n, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis boom")
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// --- push（私有方法） ------------------------------------------------------

func TestPush_Private_ZAddOnly(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 3}}
	n := makeNotification(11, 3, models.NotificationTypeFollow)

	env.mock.ExpectZAdd("feed:notification:3", redis.Z{
		Score: float64(11), Member: uint64(11),
	}).SetVal(1)

	err := tl.push(n, false, false)
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// push 在 trim=true 时会触发 trim().
func TestPush_Private_WithTrim(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 8}}
	n := makeNotification(77, 8, models.NotificationTypePoll)

	env.mock.ExpectZAdd("feed:notification:8", redis.Z{
		Score: float64(77), Member: uint64(77),
	}).SetVal(1)
	env.mock.ExpectZRemRangeByRank("feed:notification:8", int64(0), int64(-feed.MAX_ITEMS)).SetVal(0)

	err := tl.push(n, false, true)
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// push 在 trim=false 时不会调用 trim.
func TestPush_Private_WithoutTrim_NoTrimCall(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 2}}
	n := makeNotification(55, 2, models.NotificationTypeFollowRequest)

	env.mock.ExpectZAdd("feed:notification:2", redis.Z{
		Score: float64(55), Member: uint64(55),
	}).SetVal(1)

	err := tl.push(n, false, false)
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// --- trim ----------------------------------------------------------------

func TestTrim_CallsZRemRangeByRank(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 4}}

	env.mock.ExpectZRemRangeByRank("feed:notification:4", int64(0), int64(-feed.MAX_ITEMS)).SetVal(0)

	// trim 内部忽略错误（_ = ...），不应 panic.
	assert.NotPanics(t, func() {
		tl.trim()
	})
	require.NoError(t, env.mock.ExpectationsWereMet())
}

func TestTrim_IgnoresRedisError(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 6}}

	env.mock.ExpectZRemRangeByRank("feed:notification:6", int64(0), int64(-feed.MAX_ITEMS)).
		SetErr(errors.New("network error"))

	// 即使 redis 返回错误，trim 也只是忽略.
	assert.NotPanics(t, func() {
		tl.trim()
	})
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// --- Rebuild -------------------------------------------------------------

// Rebuild 空数据场景：account 无任何 notification 记录，应只 Del 旧 timeline 然后正常返回.
func TestRebuild_EmptyAccount(t *testing.T) {
	env := setupNotifEnv(t)

	now := time.Now()
	account := &models.Account{Id: 5001, Username: "rebuild_empty", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, env.gormDB.Create(account).Error)

	tl := &NotificationTimeline{Account: account}

	// Rebuild 先 Del 旧 key，由于没有 notification，Pluck 返回空，循环不执行 push.
	env.mock.ExpectDel("feed:notification:5001").SetVal(1)

	err := tl.Rebuild()
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// Rebuild 有数据场景：先 Del，再为每条 notification 调用 push -> ZAdd.
func TestRebuild_WithNotifications(t *testing.T) {
	env := setupNotifEnv(t)

	now := time.Now()
	account := &models.Account{Id: 5002, Username: "rebuild_data", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, env.gormDB.Create(account).Error)

	// 使用 SkipHooks 插入 notification，避免触发 Notification.AfterCreate 钩子
	// （该钩子会 event.Trigger -> init 注册的 handler -> go Push，与测试的 redismock 产生竞争）.
	noHooks := env.gormDB.Session(&gorm.Session{SkipHooks: true})
	// 插入 3 条 notification（push 用 trim=false, pushToClient=false）.
	for i := uint64(1); i <= 3; i++ {
		n := &models.Notification{
			Id:        i,
			AccountId: account.Id,
			Type:      models.NotificationTypeMention,
		}
		require.NoError(t, noHooks.Create(n).Error)
	}

	tl := &NotificationTimeline{Account: account}

	// Rebuild 先 Del.
	env.mock.ExpectDel("feed:notification:5002").SetVal(1)
	// 然后为每条 notification push（ZAdd）.
	for i := uint64(1); i <= 3; i++ {
		env.mock.ExpectZAdd("feed:notification:5002", redis.Z{
			Score: float64(i), Member: uint64(i),
		}).SetVal(1)
	}

	err := tl.Rebuild()
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// Rebuild 在 Del 失败时应返回 error.
func TestRebuild_DelFails(t *testing.T) {
	env := setupNotifEnv(t)

	account := &models.Account{Id: 5003}
	tl := &NotificationTimeline{Account: account}

	env.mock.ExpectDel("feed:notification:5003").SetErr(errors.New("del failed"))

	err := tl.Rebuild()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "del failed")
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// --- init 注册的 event handler --------------------------------------------

// init() 在包加载时已注册 "notification"/"after_create" handler.
// 这里通过 event.Trigger 直接调用，覆盖各分支.

// event.Data == nil -> handler 返回 false，不触发 Push.
func TestEventHandler_NilData(t *testing.T) {
	assert.NotPanics(t, func() {
		event.Trigger("notification", event.Event{Name: "after_create", Data: nil})
	})
}

// event.Data != nil 但 (*models.Notification)(nil) -> 返回 false.
func TestEventHandler_NilNotification(t *testing.T) {
	var n *models.Notification
	assert.NotPanics(t, func() {
		event.Trigger("notification", event.Event{Name: "after_create", Data: n})
	})
}

// event.Data 是有效的 notification 时，handler 启动 goroutine 调用 Push.
func TestEventHandler_ValidNotification_TriggersPush(t *testing.T) {
	env := setupNotifEnv(t)

	n := makeNotification(888, 12, models.NotificationTypeStatusUpdate)

	// handler 内部 go Push(...)，最终会执行 timeline.push -> ZAdd.
	env.mock.ExpectZAdd("feed:notification:12", redis.Z{
		Score: float64(888), Member: uint64(888),
	}).SetVal(1)

	// Trigger handler; handler 启动 goroutine 调用 Push，需要等待完成.
	event.Trigger("notification", event.Event{Name: "after_create", Data: n})

	// 等待异步 goroutine 完成（轮询 ExpectationsWereMet）.
	require.Eventually(t, func() bool {
		return env.mock.ExpectationsWereMet() == nil
	}, 2*time.Second, 10*time.Millisecond)
}

// --- 多类型通知聚合（合并/排序/去重的 feed 行为验证） -----------------------

// 验证不同类型的通知推送到同一个 timeline key 时，都被写入同一个 sorted set，
// member 是 notification id（按 score 排序天然实现时间序聚合）.
func TestNotificationAggregation_MultipleTypesSameTimeline(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 50}}

	notifications := []*models.Notification{
		{Id: 1, AccountId: 50, Type: models.NotificationTypeMention, Account: tl.Account},
		{Id: 2, AccountId: 50, Type: models.NotificationTypeFavourite, Account: tl.Account},
		{Id: 3, AccountId: 50, Type: models.NotificationTypeReblog, Account: tl.Account},
		{Id: 4, AccountId: 50, Type: models.NotificationTypeFollow, Account: tl.Account},
	}

	for _, n := range notifications {
		env.mock.ExpectZAdd("feed:notification:50", redis.Z{
			Score: float64(n.Id), Member: uint64(n.Id),
		}).SetVal(1)
	}

	for _, n := range notifications {
		err := tl.push(n, false, false)
		require.NoError(t, err)
	}

	require.NoError(t, env.mock.ExpectationsWereMet())
}

// 去重语义：同一个 notification id 再次 push，redis ZAdd 仍成功（score 更新）.
func TestNotificationAggregation_DuplicateIdIsUpsert(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 51}}
	n := makeNotification(5, 51, models.NotificationTypeMention)

	// 第一次.
	env.mock.ExpectZAdd("feed:notification:51", redis.Z{
		Score: float64(5), Member: uint64(5),
	}).SetVal(1)
	// 第二次（同样 id，ZAdd 返回 0 表示成员已存在，但操作成功）.
	env.mock.ExpectZAdd("feed:notification:51", redis.Z{
		Score: float64(5), Member: uint64(5),
	}).SetVal(0)

	require.NoError(t, tl.push(n, false, false))
	require.NoError(t, tl.push(n, false, false))
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// 排序语义：按 notification id 作为 score，天然升序；timeline 通常按 id desc 读取.
// 这里验证 score 正确传入 redis.
func TestNotificationAggregation_OrderingByScore(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 52}}

	// 乱序 push，验证每次 score 都与 id 一致.
	ids := []uint64{30, 10, 50, 20, 40}
	for _, id := range ids {
		env.mock.ExpectZAdd("feed:notification:52", redis.Z{
			Score: float64(id), Member: uint64(id),
		}).SetVal(1)
	}

	for _, id := range ids {
		n := &models.Notification{Id: id, AccountId: 52, Account: tl.Account}
		require.NoError(t, tl.push(n, false, false))
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// --- 并发安全 -------------------------------------------------------------

// 并发 push 不同 id 到同一 timeline，不应 panic 且应全部成功.
func TestPush_Concurrent(t *testing.T) {
	env := setupNotifEnv(t)

	tl := &NotificationTimeline{Account: &models.Account{Id: 60}}

	const n = 50
	for i := uint64(1); i <= n; i++ {
		env.mock.ExpectZAdd("feed:notification:60", redis.Z{
			Score: float64(i), Member: uint64(i),
		}).SetVal(1)
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := uint64(1); i <= n; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			notif := &models.Notification{Id: id, AccountId: 60, Account: tl.Account}
			errs <- tl.push(notif, false, false)
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// --- pushToClient 分支（feed.PushUpdateRequired == true 时的客户端推送） ----

// pushToClient=true 且无订阅者时，PushUpdateRequired 返回 false，
// 不会调用 serializer.ToNotification，只 ZAdd。
func TestPush_PushToClientTrue_NoSubscriber(t *testing.T) {
	env := setupNotifEnv(t)
	feed.ResetSubscriptionCache()

	n := makeNotification(700, 70, models.NotificationTypeMention)
	tl := &NotificationTimeline{Account: &models.Account{Id: 70}}

	env.mock.ExpectZAdd("feed:notification:70", redis.Z{
		Score: float64(700), Member: uint64(700),
	}).SetVal(1)

	err := tl.push(n, true, false) // pushToClient=true, trim=false
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// pushToClient=true 且有订阅者时，PushUpdateRequired 返回 true，
// 会调用 serializer.ToNotification 然后 feed.PushToClient（Publish）。
// 这里构造一个不带任何扩展字段（StatusId/ReportId/ListId/FromAccountId 均 0）的通知，
// 使 ToNotification 不触发额外的 DB 查询。
func TestPush_PushToClientTrue_WithSubscriber(t *testing.T) {
	env := setupNotifEnv(t)
	feed.ResetSubscriptionCache()

	// 让 feed.PushUpdateRequired 返回 true：在 feed mock redis 中设置对应 channel.
	env.mockRedis.SetChannel("subscribed:feed:notification:71", true)

	account := &models.Account{Id: 71}
	n := &models.Notification{
		Id:        771,
		AccountId: 71,
		Type:      models.NotificationTypeMention,
		Account:   account,
		// FromAccountId/StatusId/ReportId/ListId 均为 0，避免 ToNotification 走 DB 查询.
	}
	tl := &NotificationTimeline{Account: account}

	env.mock.ExpectZAdd("feed:notification:71", redis.Z{
		Score: float64(771), Member: uint64(771),
	}).SetVal(1)

	err := tl.push(n, true, false)
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// pushToClient=true + ToNotification 成功 + trim=true：覆盖 trim 分支组合.
func TestPush_PushToClientTrue_WithSubscriber_AndTrim(t *testing.T) {
	env := setupNotifEnv(t)
	feed.ResetSubscriptionCache()
	env.mockRedis.SetChannel("subscribed:feed:notification:72", true)

	account := &models.Account{Id: 72}
	n := &models.Notification{
		Id:        772,
		AccountId: 72,
		Type:      models.NotificationTypeFollow,
		Account:   account,
	}
	tl := &NotificationTimeline{Account: account}

	env.mock.ExpectZAdd("feed:notification:72", redis.Z{
		Score: float64(772), Member: uint64(772),
	}).SetVal(1)
	env.mock.ExpectZRemRangeByRank("feed:notification:72", int64(0), int64(-feed.MAX_ITEMS)).SetVal(0)

	err := tl.push(n, true, true)
	require.NoError(t, err)
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// --- gorm 相关补充 --------------------------------------------------------
func TestPush_DBReturnsNil_GormNotInvoked(t *testing.T) {
	n := &models.Notification{Id: 1, AccountId: 1}
	err := Push(n, false, false)
	require.Error(t, err)
	// 确保不是 gorm 相关的 panic.
	assert.NotPanics(t, func() {
		_ = errors.Is(err, gorm.ErrRecordNotFound)
	})
}

// Push 在 Account==nil 但 DB 非 nil 时会调用 LoadAllExtendFields.
// 由于 notification 不带任何 extend 字段（StatusId 等 = 0），LoadAllExtendFields 应成功，
// 但随后 timeline.push 需要 redis；这里验证整条链路能走完且最终写入.
func TestPush_NilAccount_LoadsViaDB(t *testing.T) {
	env := setupNotifEnv(t)

	// 提前在 DB 中创建 account，供 LoadAllExtendFields 加载 Account 关联.
	now := time.Now()
	account := &models.Account{Id: 700, Username: "loadvia", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, env.gormDB.Create(account).Error)

	// notification.Account == nil，Push 会调用 LoadAllExtendFields 把 Account 加载进来.
	n := &models.Notification{
		Id:        7700,
		AccountId: account.Id,
		Type:      models.NotificationTypeFollow,
		// Account 刻意留空.
	}

	env.mock.ExpectZAdd("feed:notification:700", redis.Z{
		Score: float64(7700), Member: uint64(7700),
	}).SetVal(1)

	err := Push(n, false, false)
	require.NoError(t, err)
	// 验证 LoadAllExtendFields 确实把 Account 加载进来了.
	require.NotNil(t, n.Account)
	assert.Equal(t, uint64(700), n.Account.Id)
	require.NoError(t, env.mock.ExpectationsWereMet())
}
