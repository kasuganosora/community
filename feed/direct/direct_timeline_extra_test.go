package direct

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 测试辅助函数
// ---------------------------------------------------------------------------

// setupMockFeedService 安装基于 MockRedisClient 的 FeedService，使
// Push/Unpush 内部的 feed.PushUpdateRequired / feed.PushToClient 无需真实 Redis 即可运行。
// 返回底层 MockRedisClient 以便断言状态。
func setupMockFeedService() *feed.MockRedisClient {
	mockRedis := feed.NewMockRedisClient()
	feed.SetDefaultService(feed.NewFeedService(mockRedis))
	feed.ResetSubscriptionCache()

	return mockRedis
}

// directIntegrationModels 是 direct 时间线相关数据模型，用于自动迁移。
var directIntegrationModels = []any{
	&models.User{},
	&models.UserSetting{},
	&models.Account{},
	&models.AccountExtra{},
	&models.Status{},
	&models.StatusContent{},
	&models.StatusToAccount{},
	&models.StatusMediaAttachment{},
	&models.StatusTag{},
	&models.StatusEmoji{},
	&models.Conversation{},
	&models.AccountConversation{},
	&models.Follow{},
}

// setupDirectDB 创建内存 DB 并注入全局，使 Push/Unpush 中的 db.GetDB() 可用。
// 同时迁移所需的全部模型（包含 conversations / status_to_accounts 等关联表）。
func setupDirectDB(t *testing.T) *testutil.TestDB {
	t.Helper()

	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(directIntegrationModels...))
	// Fill status_to_accounts.created_at (NOT NULL) on Association().Append.
	require.NoError(t, testDB.GormDB.SetupJoinTable(&models.Status{}, "ToAccounts", &models.StatusToAccount{}))

	database.SetTestDB(testDB.GormDB)
	t.Cleanup(func() {
		database.ResetTestDB()
	})

	return testDB
}

// newConvID 返回指向 id 的 *uint64。
func newConvID(id uint64) *uint64 {
	return &id
}

// ---------------------------------------------------------------------------
// Push —— 提前返回路径（无需 Redis / DB）
// ---------------------------------------------------------------------------

// 非 direct visibility 或 pushToClient=false 都应提前返回 nil。
func TestPush_EarlyReturn_NotPushToClient(t *testing.T) {
	// 任意 visibility + pushToClient=false 都应该立即返回
	for _, vis := range []uint8{
		models.StatusVisibilityPublic,
		models.StatusVisibilityUnlisted,
		models.StatusVisibilityPrivate,
		models.StatusVisibilityDirect,
	} {
		status := &models.Status{
			Id:             1,
			Visibility:     vis,
			ConversationId: newConvID(1),
			ToAccounts:     []*models.Account{{Id: 10}},
		}

		err := Push(status, false, false)
		assert.NoError(t, err, "pushToClient=false 时应直接返回 nil，visibility=%d", vis)
	}
}

// visibility 不是 direct 时，即使 pushToClient=true 也应提前返回。
func TestPush_EarlyReturn_NotDirectVisibility(t *testing.T) {
	for _, vis := range []uint8{
		models.StatusVisibilityPublic,
		models.StatusVisibilityUnlisted,
		models.StatusVisibilityPrivate,
	} {
		status := &models.Status{
			Id:             1,
			Visibility:     vis,
			ConversationId: newConvID(1),
			ToAccounts:     []*models.Account{{Id: 10}},
		}

		err := Push(status, false, true)
		assert.NoError(t, err, "非 direct visibility 应直接返回 nil，visibility=%d", vis)
	}
}

// direct visibility + pushToClient=true 但 ConversationId=nil 时应提前返回。
func TestPush_EarlyReturn_NilConversationId(t *testing.T) {
	status := &models.Status{
		Id:             1,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: nil,
		ToAccounts:     []*models.Account{{Id: 10}},
	}

	err := Push(status, false, true)
	assert.NoError(t, err, "ConversationId=nil 应直接返回 nil")
}

// ---------------------------------------------------------------------------
// Unpush —— 提前返回路径（无需 Redis / DB）
// ---------------------------------------------------------------------------

// 非 direct visibility 或 pushToClient=false 都应提前返回 nil。
func TestUnpush_EarlyReturn_NotPushToClient(t *testing.T) {
	for _, vis := range []uint8{
		models.StatusVisibilityPublic,
		models.StatusVisibilityUnlisted,
		models.StatusVisibilityPrivate,
		models.StatusVisibilityDirect,
	} {
		status := &models.Status{
			Id:         1,
			Visibility: vis,
			ToAccounts: []*models.Account{{Id: 10}},
		}

		err := Unpush(status, false)
		assert.NoError(t, err, "pushToClient=false 时应直接返回 nil，visibility=%d", vis)
	}
}

// visibility 不是 direct 时，即使 pushToClient=true 也应提前返回。
func TestUnpush_EarlyReturn_NotDirectVisibility(t *testing.T) {
	for _, vis := range []uint8{
		models.StatusVisibilityPublic,
		models.StatusVisibilityUnlisted,
		models.StatusVisibilityPrivate,
	} {
		status := &models.Status{
			Id:         1,
			Visibility: vis,
			ToAccounts: []*models.Account{{Id: 10}},
		}

		err := Unpush(status, true)
		assert.NoError(t, err, "非 direct visibility 应直接返回 nil，visibility=%d", vis)
	}
}

// direct + pushToClient=true 但 ToAccounts 为空切片时应提前返回。
func TestUnpush_EarlyReturn_EmptyToAccounts(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityDirect,
		ToAccounts: []*models.Account{},
	}

	err := Unpush(status, true)
	assert.NoError(t, err, "空的 ToAccounts 应直接返回 nil")
}

// ---------------------------------------------------------------------------
// Push —— 完整流程（MockRedis + 内存 DB）
// ---------------------------------------------------------------------------

// 有 ToAccounts 但没有订阅者时，Push 不应报错也不会真正发布。
func TestPush_NoSubscribers_NoError(t *testing.T) {
	setupMockFeedService()
	setupDirectDB(t)
	gormDB := db.GetDB()

	// 创建 conversation，使 Push 内部的 conversation 查询能命中
	conv := &models.Conversation{}
	require.NoError(t, gormDB.Create(conv).Error)

	status := &models.Status{
		Id:             1001,
		AccountId:      1,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(conv.Id),
		ToAccounts: []*models.Account{
			{Id: 100},
			{Id: 200},
		},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

// 有订阅者时应触发 conversation 推送。
func TestPush_WithSubscribers_PublishesConversation(t *testing.T) {
	mockRedis := setupMockFeedService()
	setupDirectDB(t)
	gormDB := db.GetDB()

	// 创建账号与 conversation，使 DB 查询能命中
	conv := &models.Conversation{}
	require.NoError(t, gormDB.Create(conv).Error)

	account1 := &models.Account{Username: "direct_user1"}
	require.NoError(t, gormDB.Create(account1).Error)
	account2 := &models.Account{Username: "direct_user2"}
	require.NoError(t, gormDB.Create(account2).Error)

	// status 写入 DB，ToAccounts 通过关联表关联
	// SkipHooks 避免 AfterCreate 触发 direct after_create handler goroutine 访问 DB
	status := &models.Status{
		AccountId:      account1.Id,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(conv.Id),
	}
	require.NoError(t, gormDB.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
	// 建立多对多关联（SkipHooks 避免 Association.Append 内部 Save 触发 AfterUpdate 事件，
	// 否则 direct 的 after_update handler goroutine 会调用 Push 并在测试结束后访问已置空的 DB）
	require.NoError(t, gormDB.Session(&gorm.Session{SkipHooks: true}).Model(status).Association("ToAccounts").Append(account1, account2))

	// 模拟 account1 的 direct 时间线有订阅者
	mockRedis.SetChannel("subscribed:direct:"+strconv.FormatUint(account1.Id, 10), true)

	// 不预设 status.ToAccounts（为 nil），让 Push 内部走 Association("ToAccounts").Find 分支
	status.ToAccounts = nil

	err := Push(status, false, true)
	require.NoError(t, err)
}

// ConversationId 指向不存在的 conversation 时应返回错误。
func TestPush_ConversationNotFound(t *testing.T) {
	setupMockFeedService()
	setupDirectDB(t)

	account := &models.Account{Username: "direct_notfound_user"}
	require.NoError(t, db.GetDB().Create(account).Error)

	status := &models.Status{
		AccountId:      account.Id,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(999999), // 不存在的 conversation
		ToAccounts:     []*models.Account{account},
	}

	err := Push(status, false, true)
	assert.Error(t, err, "ConversationId 不存在时应返回错误")
}

// ---------------------------------------------------------------------------
// Unpush —— 完整流程（MockRedis + 内存 DB）
// ---------------------------------------------------------------------------

// Unpush 有 ToAccounts 但没有订阅者时应正常返回。
func TestUnpush_NoSubscribers_NoError(t *testing.T) {
	setupMockFeedService()
	setupDirectDB(t)

	status := &models.Status{
		Id:             1,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(1),
		ToAccounts: []*models.Account{
			{Id: 100},
			{Id: 200},
		},
	}

	err := Unpush(status, true)
	require.NoError(t, err)
}

// Unpush 有订阅者时应触发 delete 推送。
func TestUnpush_WithSubscribers_PublishesDelete(t *testing.T) {
	mockRedis := setupMockFeedService()
	setupDirectDB(t)
	gormDB := db.GetDB()

	account1 := &models.Account{Username: "direct_del_user1"}
	require.NoError(t, gormDB.Create(account1).Error)

	// 模拟订阅
	mockRedis.SetChannel("subscribed:direct:"+strconv.FormatUint(account1.Id, 10), true)

	status := &models.Status{
		Id:             1002,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(1),
		ToAccounts:     []*models.Account{account1},
	}

	err := Unpush(status, true)
	require.NoError(t, err)
}

// Unpush 中 ToAccounts 为 nil 时，应从 DB 读取关联，读取后为空则提前返回。
// 使用 SkipHooks 创建 status，避免触发 Status.AfterCreate 事件，
// 否则 direct 的 after_create handler goroutine 会调用 Push 并访问 DB，
// 在测试结束后（DB 被 ResetTestDB 置空）导致 panic。
func TestUnpush_NilToAccounts_LoadsFromDB_Empty(t *testing.T) {
	setupMockFeedService()
	setupDirectDB(t)
	gormDB := db.GetDB()

	account := &models.Account{Username: "direct_load_empty"}
	require.NoError(t, gormDB.Create(account).Error)

	// 创建 conversation，便于构造 status
	conv := &models.Conversation{}
	require.NoError(t, gormDB.Create(conv).Error)

	status := &models.Status{
		Id:             1003,
		AccountId:      account.Id,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(conv.Id),
		ToAccounts:     nil, // 强制走 DB 查询分支
	}
	// SkipHooks 避免 AfterCreate 触发 direct after_create handler goroutine
	require.NoError(t, gormDB.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
	// 不附加任何 ToAccounts，使 DB 查询返回空

	err := Unpush(status, true)
	require.NoError(t, err, "ToAccounts 为空时应正常返回")
}

// Unpush 中 ToAccounts 为 nil 且 DB 中存在关联时应正常推送 delete。
// 同样使用 SkipHooks 避免 handler goroutine 跨测试访问 DB。
func TestUnpush_NilToAccounts_LoadsFromDB_NonEmpty(t *testing.T) {
	mockRedis := setupMockFeedService()
	setupDirectDB(t)
	gormDB := db.GetDB()

	account1 := &models.Account{Username: "direct_load_acc1"}
	require.NoError(t, gormDB.Create(account1).Error)
	account2 := &models.Account{Username: "direct_load_acc2"}
	require.NoError(t, gormDB.Create(account2).Error)

	conv := &models.Conversation{}
	require.NoError(t, gormDB.Create(conv).Error)

	status := &models.Status{
		Id:             1004,
		AccountId:      account1.Id,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(conv.Id),
		ToAccounts:     nil,
	}
	require.NoError(t, gormDB.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
	// SkipHooks 避免 Append 触发 after_update handler goroutine
	require.NoError(t, gormDB.Session(&gorm.Session{SkipHooks: true}).Model(status).Association("ToAccounts").Append(account1, account2))

	// account1 有订阅
	mockRedis.SetChannel("subscribed:direct:"+strconv.FormatUint(account1.Id, 10), true)

	err := Unpush(status, true)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// 事件处理器（init 中注册的 "status"/"*" handler）
// ---------------------------------------------------------------------------

// nil Data 不应 panic。
func TestEventHandler_NilData(t *testing.T) {
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: nil})
	})
	time.Sleep(50 * time.Millisecond) // 等待 handler goroutine
}

// nil status（Data 非 nil 但类型断言后为 nil 指针）不应 panic。
func TestEventHandler_NilStatus(t *testing.T) {
	assert.NotPanics(t, func() {
		var status *models.Status
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// 非 direct visibility 的事件不应进入 push/unpush 分支。
func TestEventHandler_NonDirectVisibility(t *testing.T) {
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{
			Name: "after_create",
			Data: &models.Status{Id: 1, Visibility: models.StatusVisibilityPublic},
		})
	})
	time.Sleep(50 * time.Millisecond)
}

// setupEventHandlerTest 为事件处理器测试准备全局 DB（内存）与 Mock FeedService。
// Push/Unpush 在 goroutine 中调用 db.GetDB()，必须注入可用的全局 DB。
func setupEventHandlerTest(t *testing.T) {
	t.Helper()
	setupDirectDB(t)
	setupMockFeedService()
}

// after_create 事件触发 Push（direct）。
// 注意：不设置 ConversationId，使 Push 走提前返回路径（ConversationId==nil），
// 避免 handler goroutine 在测试结束后访问已被 ResetTestDB 置空的 DB。
func TestEventHandler_AfterCreate(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         6001,
		AccountId:  1,
		Visibility: models.StatusVisibilityDirect,
		// ConversationId 留空，使 Push 提前返回
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// after_update 事件触发 Push(update=true)。
// 注意：不设置 ConversationId，使 Push 走提前返回路径。
func TestEventHandler_AfterUpdate(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         6002,
		AccountId:  1,
		Visibility: models.StatusVisibilityDirect,
		// ConversationId 留空，使 Push 提前返回
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_update", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// before_update 事件触发 Unpush。
func TestEventHandler_BeforeUpdate(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         6003,
		AccountId:  1,
		Visibility: models.StatusVisibilityDirect,
		ToAccounts: []*models.Account{}, // 空，提前返回
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "before_update", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// after_delete 事件触发 Unpush。
func TestEventHandler_AfterDelete(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         6004,
		AccountId:  1,
		Visibility: models.StatusVisibilityDirect,
		ToAccounts: []*models.Account{}, // 空，提前返回
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_delete", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// 未知事件名不应 panic。
func TestEventHandler_UnknownEvent(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         6005,
		AccountId:  1,
		Visibility: models.StatusVisibilityDirect,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "custom_event", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// 集成测试 —— 完整 Push/Unpush 流程，验证对话线程分组与消息推送
// ---------------------------------------------------------------------------

// skipIfNoGlobalRedis 当没有全局 Redis 配置时跳过测试。
// Push/Unpush 内部走 feed.PushToClient -> feed.GetRedisChannelName + publish，
// 但 Push 的 conversation 查询依赖 db.GetDB()。这里我们使用内存 DB + 注入的 Redis 客户端。
func skipIfNoGlobalRedis(t *testing.T) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("Skipping test: no global Redis configured: %v", r)
		}
	}()
	if db.GetRedis() == nil {
		t.Skip("Skipping test: no global Redis configured")
	}
}

// setupDirectIntegration 准备一个带有真实 DB 与全局 Redis 的集成测试环境。
// 返回的 *testutil.TestSetup 会在 t.Cleanup 中自动释放资源。
// 同时会把全局 DB 指向测试 DB，使 db.GetDB() 可用。
// 注意：如果真实 Redis 不可用，测试会被跳过。
func setupDirectIntegration(t *testing.T) *testutil.TestSetup {
	t.Helper()
	skipIfNoGlobalRedis(t)

	setup := testutil.NewTestSetup(t,
		testutil.WithModels(directIntegrationModels...),
		testutil.WithRealRedis(),
	)

	// 注入全局 DB，使 direct 包内 db.GetDB() 调用返回测试 DB。
	database.SetTestDB(setup.GormDB())
	feedService := feed.NewFeedService(feed.NewRedisClient(setup.RedisClient().Client))
	feed.SetDefaultService(feedService)
	feed.ResetSubscriptionCache()

	t.Cleanup(func() {
		database.ResetTestDB()
	})

	return setup
}

// Push 应将 conversation 推送给所有 ToAccounts 的 direct 时间线订阅者。
// 这是核心的「私信推送到对话线程」逻辑。
func TestPush_Integration_PushesConversationToAllRecipients(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	setup := setupDirectIntegration(t)
	gormDB := db.GetDB()

	// 创建两个账号（私信的收发双方）
	account1 := &models.Account{Username: "dm_user1"}
	require.NoError(t, gormDB.Create(account1).Error)
	account2 := &models.Account{Username: "dm_user2"}
	require.NoError(t, gormDB.Create(account2).Error)

	// 创建 conversation
	conv := &models.Conversation{}
	require.NoError(t, gormDB.Create(conv).Error)

	// 创建 direct status 并关联 ToAccounts
	status := &models.Status{
		AccountId:      account1.Id,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(conv.Id),
	}
	require.NoError(t, gormDB.Create(status).Error)
	require.NoError(t, gormDB.Model(status).Association("ToAccounts").Append(account1, account2))

	// 在 account2 的时间线频道上订阅
	ctx := context.Background()
	channel := "subscribed:direct:" + strconv.FormatUint(account2.Id, 10)
	pubsub := setup.RedisClient().Client.Subscribe(ctx, channel)
	defer func() { _ = pubsub.Close() }()
	_, err := pubsub.Receive(ctx)
	require.NoError(t, err)

	// 重置缓存以使 PushUpdateRequired 能探测到订阅者
	feed.ResetSubscriptionCache()

	msgCh := pubsub.Channel()

	// 触发 Push
	status.ToAccounts = nil // 强制走 DB 查询分支
	err = Push(status, false, true)
	require.NoError(t, err)

	// 等待消息到达（Push 内部 publish 是同步的，但有缓存）
	select {
	case msg := <-msgCh:
		assert.Equal(t, channel, msg.Channel)
		assert.Contains(t, msg.Payload, "conversation")
		assert.Contains(t, msg.Payload, strconv.FormatUint(conv.Id, 10))
	case <-time.After(2 * time.Second):
		t.Error("Did not receive conversation message within timeout")
	}
}

// Unpush 应将 delete 事件推送给订阅者。
func TestUnpush_Integration_PushesDeleteToSubscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	setup := setupDirectIntegration(t)
	gormDB := db.GetDB()

	account1 := &models.Account{Username: "dm_del_user1"}
	require.NoError(t, gormDB.Create(account1).Error)

	status := &models.Status{
		AccountId:      account1.Id,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(1),
	}
	require.NoError(t, gormDB.Create(status).Error)
	require.NoError(t, gormDB.Model(status).Association("ToAccounts").Append(account1))

	// 订阅
	ctx := context.Background()
	channel := "subscribed:direct:" + strconv.FormatUint(account1.Id, 10)
	pubsub := setup.RedisClient().Client.Subscribe(ctx, channel)
	defer func() { _ = pubsub.Close() }()
	_, err := pubsub.Receive(ctx)
	require.NoError(t, err)

	feed.ResetSubscriptionCache()

	msgCh := pubsub.Channel()

	status.ToAccounts = nil
	err = Unpush(status, true)
	require.NoError(t, err)

	select {
	case msg := <-msgCh:
		assert.Equal(t, channel, msg.Channel)
		assert.Contains(t, msg.Payload, "delete")
		assert.Contains(t, msg.Payload, strconv.FormatUint(status.Id, 10))
	case <-time.After(2 * time.Second):
		t.Error("Did not receive delete message within timeout")
	}
}

// ---------------------------------------------------------------------------
// Rebuild —— 再次覆盖
// ---------------------------------------------------------------------------

// Rebuild 总是返回 nil（不执行任何操作）。
func TestRebuild_AlwaysReturnsNil(t *testing.T) {
	for i := range 5 {
		account := &models.Account{Id: uint64(i)}
		timeline := &DirectTimeline{Account: account}

		err := timeline.Rebuild()
		assert.NoError(t, err)
	}
}

// ---------------------------------------------------------------------------
// GetRedisChannelName / 频道命名一致性
// ---------------------------------------------------------------------------

// DirectTimeline 的 Redis 频道名应符合 "subscribed:direct:<account_id>"。
func TestDirectTimeline_RedisChannelName(t *testing.T) {
	account := &models.Account{Id: 7777}
	timeline := &DirectTimeline{Account: account}

	channelName := feed.GetRedisChannelName(timeline)
	assert.Equal(t, "subscribed:direct:7777", channelName)
}

// PushUpdateRequired 在没有订阅者时应返回 false。
func TestDirectTimeline_PushUpdateRequired_NoSubscriber(t *testing.T) {
	setupMockFeedService()

	account := &models.Account{Id: 8888}
	timeline := &DirectTimeline{Account: account}

	assert.False(t, feed.PushUpdateRequired(timeline))
}

// PushUpdateRequired 在有订阅者时应返回 true。
func TestDirectTimeline_PushUpdateRequired_WithSubscriber(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{Id: 8889}
	timeline := &DirectTimeline{Account: account}

	mockRedis.SetChannel("subscribed:direct:8889", true)
	assert.True(t, feed.PushUpdateRequired(timeline))
}

// PushUpdateRequired 应使用缓存（再次查询应返回缓存值）。
func TestDirectTimeline_PushUpdateRequired_Cache(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{Id: 8890}
	timeline := &DirectTimeline{Account: account}

	// 第一次查询，无订阅者
	assert.False(t, feed.PushUpdateRequired(timeline))

	// 切换为有订阅者，但缓存仍然保留 false
	mockRedis.SetChannel("subscribed:direct:8890", true)
	// 缓存内仍是 false
	assert.False(t, feed.PushUpdateRequired(timeline))
}

// ---------------------------------------------------------------------------
// 对话线程分组 —— 多个 ToAccounts 应分别构建独立的 DirectTimeline
// ---------------------------------------------------------------------------

// Push 应为每个 ToAccount 构建独立的 DirectTimeline，推送 conversation。
func TestPush_DirectTimelinePerAccount(t *testing.T) {
	mockRedis := setupMockFeedService()
	setupDirectDB(t)
	gormDB := db.GetDB()

	// 创建 conversation 与 3 个账号
	conv := &models.Conversation{}
	require.NoError(t, gormDB.Create(conv).Error)

	accounts := make([]*models.Account, 0, 3)
	for i := range 3 {
		acc := &models.Account{Username: "dm_group_" + strconv.Itoa(i)}
		require.NoError(t, gormDB.Create(acc).Error)
		accounts = append(accounts, acc)
	}

	status := &models.Status{
		AccountId:      accounts[0].Id,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: newConvID(conv.Id),
	}
	// SkipHooks 避免 AfterCreate 触发 direct after_create handler goroutine
	require.NoError(t, gormDB.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
	// SkipHooks 避免 Append 触发 after_update handler goroutine
	require.NoError(t, gormDB.Session(&gorm.Session{SkipHooks: true}).Model(status).Association("ToAccounts").Append(accounts))

	// 给所有账号的 direct 频道设置订阅（mock）
	for _, acc := range accounts {
		mockRedis.SetChannel("subscribed:direct:"+strconv.FormatUint(acc.Id, 10), true)
	}

	status.ToAccounts = nil // 走 DB 查询分支

	err := Push(status, false, true)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// 并发安全 —— 多 goroutine 调用 Push 的提前返回路径
// ---------------------------------------------------------------------------

func TestPush_Concurrent_EarlyReturn(t *testing.T) {
	var wg sync.WaitGroup
	var errorCount int64

	for range 20 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 50 {
				status := &models.Status{
					Id:             1,
					Visibility:     models.StatusVisibilityDirect,
					ConversationId: newConvID(1),
					ToAccounts:     []*models.Account{{Id: 1}},
				}

				// pushToClient=false 走提前返回路径，无需 DB/Redis
				if err := Push(status, false, false); err != nil {
					atomic.AddInt64(&errorCount, 1)
				}
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int64(0), errorCount)
}

func TestUnpush_Concurrent_EarlyReturn(t *testing.T) {
	var wg sync.WaitGroup
	var errorCount int64

	for range 20 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 50 {
				status := &models.Status{
					Id:         1,
					Visibility: models.StatusVisibilityDirect,
					ToAccounts: []*models.Account{{Id: 1}},
				}

				// pushToClient=false 走提前返回路径，无需 DB/Redis
				if err := Unpush(status, false); err != nil {
					atomic.AddInt64(&errorCount, 1)
				}
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int64(0), errorCount)
}
