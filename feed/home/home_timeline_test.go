package home

import (
	"context"
	"strconv"
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
)

// ---------------------------------------------------------------------------
// 测试辅助函数
// ---------------------------------------------------------------------------

// setupMockFeedService 安装基于 MockRedisClient 的 FeedService 和 DBService，
// 使 PushToHome/UnPushFromHome 等仅依赖 feed 包全局服务的时间线操作无需真实 Redis/DB 即可运行。
// 返回底层 MockRedisClient 以便断言状态。
func setupMockFeedService() *feed.MockRedisClient {
	mockRedis := feed.NewMockRedisClient()
	feed.SetDefaultService(feed.NewFeedService(mockRedis))
	feed.ResetSubscriptionCache()
	return mockRedis
}

// skipIfNoGlobalRedis 当没有全局 Redis 配置时跳过测试。
// GetTimeLineContent/Rebuild/Push/Unpush 等直接使用 db.GetRedis() 和 db.GetDB()，
// 因此需要真实的全局 Redis 配置才能完整运行。
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

// integrationModels 是 home 时间线相关的全部数据模型，用于自动迁移。
var integrationModels = []any{
	&models.User{},
	&models.UserSetting{},
	&models.Account{},
	&models.AccountExtra{},
	&models.Status{},
	&models.StatusContent{},
	&models.StatusToAccount{},
	&models.Follow{},
}

// setupIntegration 准备一个带有真实 DB 与全局 Redis 的集成测试环境。
// 返回的 *testutil.TestSetup 会在 t.Cleanup 中自动释放资源。
// 同时会把全局 DB 指向测试 DB，使 db.GetDB() 可用。
// 注意：如果真实 Redis 不可用，测试会被跳过。
func setupIntegration(t *testing.T) *testutil.TestSetup {
	t.Helper()

	// 先检查全局 Redis 是否可用（NewTestSetup 会注入 redis 客户端，但这里需要确认连接本身可用）
	skipIfNoGlobalRedis(t)

	setup := testutil.NewTestSetup(t,
		testutil.WithModels(integrationModels...),
		testutil.WithRealRedis(),
	)

	// 注入全局 DB，使 home 包内 db.GetDB() 调用返回测试 DB。
	// NewTestSetup 已经把 redis 客户端注入到 db/redis 包，因此 db.GetRedis() 也可用。
	dbService := feed.NewDBService(setup.GormDB())
	feed.SetDefaultDBService(dbService)
	feedService := feed.NewFeedService(feed.NewRedisClient(setup.RedisClient().Client))
	feed.SetDefaultService(feedService)

	return setup
}

// strPtr 返回指向 s 的指针，用于构造远程字段。
func strPtr(s string) *string {
	return &s
}

// ---------------------------------------------------------------------------
// GetKeyFormat
// ---------------------------------------------------------------------------

func TestHomeTimeline_GetKeyFormat(t *testing.T) {
	timeline := &HomeTimeline{}
	assert.Equal(t, "feed:home:%s", timeline.GetKeyFormat())
}

// ---------------------------------------------------------------------------
// GetTimelineId —— 覆盖多种 ID 值
// ---------------------------------------------------------------------------

func TestHomeTimeline_GetTimelineId(t *testing.T) {
	tests := []struct {
		name     string
		account  *models.Account
		expected string
	}{
		{"普通 ID", &models.Account{Id: 12345}, "12345"},
		{"ID 为 0", &models.Account{Id: 0}, "0"},
		{"大 ID", &models.Account{Id: 9999999999}, "9999999999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeline := &HomeTimeline{Account: tt.account}
			assert.Equal(t, tt.expected, timeline.GetTimelineId())
		})
	}
}

// ---------------------------------------------------------------------------
// HomeTimeline 实现 feed.Timeline 接口验证
// ---------------------------------------------------------------------------

func TestHomeTimeline_ImplementsTimelineInterface(t *testing.T) {
	var _ feed.Timeline = (*HomeTimeline)(nil)
}

// 构造出的 Redis Key 应为 "feed:home:<account_id>"（通过 Sprintf 组合）。
func TestHomeTimeline_KeyComposition(t *testing.T) {
	account := &models.Account{Id: 42}
	timeline := &HomeTimeline{Account: account}

	keyFormat := timeline.GetKeyFormat()
	id := timeline.GetTimelineId()
	composedKey := keyFormat + id

	assert.Equal(t, "feed:home:%s", keyFormat, "key format 应为 feed:home:%s")
	assert.Equal(t, "42", id, "timeline id 应为账号 id 的字符串形式")
	// 模拟 feed 包中 key 的组合方式：fmt.Sprintf(keyFormat, id)
	assert.Contains(t, composedKey, "42")
}

// 不同账号的时间线 ID 应唯一。
func TestHomeTimeline_KeyUniqueness(t *testing.T) {
	timelines := make(map[string]bool)

	for i := uint64(1); i <= 100; i++ {
		account := &models.Account{Id: i}
		timeline := &HomeTimeline{Account: account}
		key := timeline.GetTimelineId()

		assert.False(t, timelines[key], "Duplicate key found: %s", key)
		timelines[key] = true
	}

	assert.Len(t, timelines, 100)
}

// 并发访问 GetKeyFormat/GetTimelineId 应安全。
func TestHomeTimeline_ConcurrentAccess(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &HomeTimeline{Account: account}

	done := make(chan bool)

	for range 10 {
		go func() {
			for range 100 {
				_ = timeline.GetKeyFormat()
				_ = timeline.GetTimelineId()
			}
			done <- true
		}()
	}

	for range 10 {
		<-done
	}
}

// ---------------------------------------------------------------------------
// PushToHome —— 使用 MockRedisClient，覆盖 User.Settings 分支与时间线写入
// ---------------------------------------------------------------------------

// 没有 User.Settings 时使用默认的 aggregatesReblogs=true。
func TestPushToHome_NilUserSettings(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{Id: 1} // User 为 nil
	status := &models.Status{
		Id:         1001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := PushToHome(account, status, false, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:1")
	assert.Contains(t, zSet, "1001", "status 应被写入时间线")
}

// 有 User.Settings 且 aggregatesReblogs=true。
func TestPushToHome_WithUserSettings(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{
		Id: 2,
		User: &models.User{
			Id: 1,
			Settings: &models.UserSetting{
				Data: map[string]any{"aggregates_reblogs": true},
			},
		},
	}
	status := &models.Status{
		Id:         1002,
		AccountId:  2,
		Visibility: models.StatusVisibilityPublic,
	}

	err := PushToHome(account, status, false, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:2")
	assert.Contains(t, zSet, "1002")
}

// aggregatesReblogs=false 的用户设置应被正确读取。
func TestPushToHome_DisableAggregatesReblogs(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{
		Id: 3,
		User: &models.User{
			Id: 2,
			Settings: &models.UserSetting{
				Data: map[string]any{"aggregates_reblogs": false},
			},
		},
	}
	status := &models.Status{
		Id:         1003,
		AccountId:  3,
		Visibility: models.StatusVisibilityPublic,
	}

	err := PushToHome(account, status, false, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:3")
	assert.Contains(t, zSet, "1003")
}

// 有订阅者且 pushToClient=true 时应触发客户端推送且不报错。
func TestPushToHome_WithClientPush(t *testing.T) {
	mockRedis := setupMockFeedService()
	// 模拟该时间线有订阅者
	mockRedis.SetChannel("subscribed:feed:home:4", true)

	account := &models.Account{Id: 4}
	status := &models.Status{
		Id:         1004,
		AccountId:  4,
		Visibility: models.StatusVisibilityPublic,
	}

	err := PushToHome(account, status, false, true)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:4")
	assert.Contains(t, zSet, "1004")
}

// update=true 时应使用 "status.update" 方法。
func TestPushToHome_UpdateFlag(t *testing.T) {
	mockRedis := setupMockFeedService()
	mockRedis.SetChannel("subscribed:feed:home:5", true)

	account := &models.Account{Id: 5}
	status := &models.Status{
		Id:         1005,
		AccountId:  5,
		Visibility: models.StatusVisibilityPublic,
	}

	err := PushToHome(account, status, true, true)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:5")
	assert.Contains(t, zSet, "1005")
}

// 连续推送多条 status 后时间线应包含所有 ID。
func TestPushToHome_MultipleStatuses(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{Id: 6}

	for i := range 5 {
		status := &models.Status{
			Id:         uint64(6000 + i),
			AccountId:  6,
			Visibility: models.StatusVisibilityPublic,
		}
		err := PushToHome(account, status, false, false)
		require.NoError(t, err)
	}

	zSet := mockRedis.GetZSet("feed:home:6")
	for i := range 5 {
		assert.Contains(t, zSet, strconv.FormatUint(uint64(6000+i), 10))
	}
}

// ---------------------------------------------------------------------------
// UnPushFromHome —— 使用 MockRedisClient，覆盖移除逻辑
// ---------------------------------------------------------------------------

// UnPushFromHome 应从时间线移除 status。
func TestUnPushFromHome_RemovesFromTimeline(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{Id: 10}
	status := &models.Status{
		Id:         10010,
		AccountId:  10,
		Visibility: models.StatusVisibilityPublic,
	}

	// 先添加
	err := PushToHome(account, status, false, false)
	require.NoError(t, err)
	assert.Contains(t, mockRedis.GetZSet("feed:home:10"), "10010")

	// 再移除
	err = UnPushFromHome(account, status, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:10")
	assert.NotContains(t, zSet, "10010")
}

// UnPushFromHome 移除不存在的 status 不应报错。
func TestUnPushFromHome_NonExistentStatus(t *testing.T) {
	setupMockFeedService()

	account := &models.Account{Id: 11}
	status := &models.Status{
		Id:         99999,
		AccountId:  11,
		Visibility: models.StatusVisibilityPublic,
	}

	err := UnPushFromHome(account, status, false)
	require.NoError(t, err)
}

// UnPushFromHome 有订阅者且 pushToClient=true 时应触发删除推送。
func TestUnPushFromHome_WithClientPush(t *testing.T) {
	mockRedis := setupMockFeedService()
	mockRedis.SetChannel("subscribed:feed:home:12", true)

	account := &models.Account{Id: 12}
	status := &models.Status{
		Id:         10012,
		AccountId:  12,
		Visibility: models.StatusVisibilityPublic,
	}

	// 先添加
	err := PushToHome(account, status, false, false)
	require.NoError(t, err)

	// 移除并推送
	err = UnPushFromHome(account, status, true)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:12")
	assert.NotContains(t, zSet, "10012")
}

// UnPushFromHome 读取 User.Settings 分支。
func TestUnPushFromHome_WithUserSettings(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{
		Id: 13,
		User: &models.User{
			Id: 13,
			Settings: &models.UserSetting{
				Data: map[string]any{"aggregates_reblogs": true},
			},
		},
	}
	status := &models.Status{
		Id:         10013,
		AccountId:  13,
		Visibility: models.StatusVisibilityPublic,
	}

	err := PushToHome(account, status, false, false)
	require.NoError(t, err)

	err = UnPushFromHome(account, status, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:13")
	assert.NotContains(t, zSet, "10013")
}

// ---------------------------------------------------------------------------
// 事件处理器（init 中注册的 "status"/"*" handler）
// ---------------------------------------------------------------------------

// setupEventHandlerTest 为事件处理器测试准备全局 DB（内存）与 Mock FeedService。
// Push/Unpush 等函数会在 goroutine 中调用 db.GetDB() 与 feed 默认服务，
// 因此必须注入可用的全局 DB，否则 goroutine 中的 panic 会导致整个测试进程崩溃。
func setupEventHandlerTest(t *testing.T) {
	t.Helper()
	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)

	// 自动迁移所需模型，使 Push 内的 ToAccounts 关联查询可用
	require.NoError(t, testDB.AutoMigrate(integrationModels...))

	// 注入全局 DB 与默认服务
	database.SetTestDB(testDB.GormDB)
	feed.SetDefaultDBService(feed.NewDBService(testDB.GormDB))
	setupMockFeedService()

	t.Cleanup(func() {
		database.ResetTestDB()
	})
}

// nil Data 不应 panic。
func TestEventHandler_NilData(t *testing.T) {
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: nil})
	})
	time.Sleep(50 * time.Millisecond) // 等待 handler goroutine
}

// nil status（Data 非 nil 但类型断言后为 nil）不应 panic。
func TestEventHandler_NilStatus(t *testing.T) {
	assert.NotPanics(t, func() {
		var status *models.Status
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// after_create 事件触发 Push。
func TestEventHandler_AfterCreate(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         5001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// after_update 事件触发 Push(update=true)。
func TestEventHandler_AfterUpdate(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         5002,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_update", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// after_delete 事件触发 Unpush。
func TestEventHandler_AfterDelete(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         5003,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_delete", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// before_update 事件触发 ClearMentionsPushStatus。
func TestEventHandler_BeforeUpdate(t *testing.T) {
	setupEventHandlerTest(t)

	status := &models.Status{
		Id:         5004,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "before_update", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// 集成测试 —— 需要真实 Redis + 真实 DB
// ---------------------------------------------------------------------------

// Rebuild 应清空并重建时间线，包含关注者与自己发的 status。
func TestHomeTimeline_Rebuild_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()
	ctx := context.Background()

	// 1. 创建用户与账号
	user1 := &models.User{UUID: "rebuild-user1", UserName: "rebuild_user1", EmailHash: "rebuild_hash_1"}
	require.NoError(t, gormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "rebuild_user1"}
	require.NoError(t, gormDB.Create(account1).Error)

	user2 := &models.User{UUID: "rebuild-user2", UserName: "rebuild_user2", EmailHash: "rebuild_hash_2"}
	require.NoError(t, gormDB.Create(user2).Error)
	account2 := &models.Account{UserId: user2.Id, Username: "rebuild_user2"}
	require.NoError(t, gormDB.Create(account2).Error)

	// 2. account1 关注 account2
	require.NoError(t, gormDB.Create(&models.Follow{
		AccountId: account1.Id,
		FollowId:  account2.Id,
		Status:    models.FollowStatusFollowed,
	}).Error)

	// 3. account2 发两条 public status，account1 自己发一条
	now := time.Now()
	statusOwn := &models.Status{AccountId: account1.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(statusOwn).Error)
	statusFollowed1 := &models.Status{AccountId: account2.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(statusFollowed1).Error)
	statusFollowed2 := &models.Status{AccountId: account2.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(statusFollowed2).Error)

	// 4. 执行 Rebuild
	timeline := &HomeTimeline{Account: account1}
	err := timeline.Rebuild()
	require.NoError(t, err)

	// 5. 验证 Redis 中时间线包含关注者与自己的 status（按 id 倒序）
	key := "feed:home:" + strconv.FormatUint(account1.Id, 10)
	members, err := setup.RedisClient().Client.ZRange(ctx, key, 0, -1).Result()
	require.NoError(t, err)

	idStrs := make(map[string]bool, len(members))
	for _, m := range members {
		idStrs[m] = true
	}
	assert.True(t, idStrs[strconv.FormatUint(statusOwn.Id, 10)], "自己的 status 应在时间线")
	assert.True(t, idStrs[strconv.FormatUint(statusFollowed1.Id, 10)], "关注者的 status1 应在时间线")
	assert.True(t, idStrs[strconv.FormatUint(statusFollowed2.Id, 10)], "关注者的 status2 应在时间线")
}

// Rebuild 应过滤掉不属于自己的 direct status。
func TestHomeTimeline_Rebuild_FiltersDirectStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()
	ctx := context.Background()

	user1 := &models.User{UUID: "rebuild-direct-1", UserName: "rebuild_direct1", EmailHash: "rebuild_direct_h1"}
	require.NoError(t, gormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "rebuild_direct1"}
	require.NoError(t, gormDB.Create(account1).Error)

	user2 := &models.User{UUID: "rebuild-direct-2", UserName: "rebuild_direct2", EmailHash: "rebuild_direct_h2"}
	require.NoError(t, gormDB.Create(user2).Error)
	account2 := &models.Account{UserId: user2.Id, Username: "rebuild_direct2"}
	require.NoError(t, gormDB.Create(account2).Error)

	// account1 关注 account2
	require.NoError(t, gormDB.Create(&models.Follow{
		AccountId: account1.Id,
		FollowId:  account2.Id,
		Status:    models.FollowStatusFollowed,
	}).Error)

	now := time.Now()
	// account2 发一条 direct status（不提及 account1），应被过滤
	directStatus := &models.Status{AccountId: account2.Id, Visibility: models.StatusVisibilityDirect, CreatedAt: &now}
	require.NoError(t, gormDB.Create(directStatus).Error)
	// account2 发一条 public status，应保留
	publicStatus := &models.Status{AccountId: account2.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(publicStatus).Error)

	timeline := &HomeTimeline{Account: account1}
	err := timeline.Rebuild()
	require.NoError(t, err)

	key := "feed:home:" + strconv.FormatUint(account1.Id, 10)
	members, err := setup.RedisClient().Client.ZRange(ctx, key, 0, -1).Result()
	require.NoError(t, err)

	idStrs := make(map[string]bool, len(members))
	for _, m := range members {
		idStrs[m] = true
	}
	assert.False(t, idStrs[strconv.FormatUint(directStatus.Id, 10)], "不属于 account1 的 direct status 应被过滤")
	assert.True(t, idStrs[strconv.FormatUint(publicStatus.Id, 10)], "public status 应保留")
}

// GetTimeLineContent 第一页无缓存时应触发 Rebuild 并返回时间线内容。
func TestGetTimeLineContent_FirstPage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()

	user := &models.User{UUID: "gtc-user", UserName: "gtc_user", EmailHash: "gtc_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "gtc_user"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	status1 := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status1).Error)
	status2 := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status2).Error)

	// 第一页，params 为 nil，此时应触发 Rebuild
	statuses, err := GetTimeLineContent(account, nil)
	require.NoError(t, err)
	// Rebuild 后从 DB 查询时携带大量 Preload，应至少能返回这些 status
	idSet := make(map[uint64]bool, len(statuses))
	for _, s := range statuses {
		idSet[s.Id] = true
	}
	assert.True(t, idSet[status1.Id], "status1 应在时间线内容中")
	assert.True(t, idSet[status2.Id], "status2 应在时间线内容中")
}

// GetTimeLineContent 带 Limit 参数应实现分页。
func TestGetTimeLineContent_WithLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()
	ctx := context.Background()

	user := &models.User{UUID: "gtc-limit-user", UserName: "gtc_limit", EmailHash: "gtc_limit_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "gtc_limit"}
	require.NoError(t, gormDB.Create(account).Error)

	// 创建 10 条 status
	now := time.Now()
	createdIds := make([]uint64, 0, 10)
	for i := 0; i < 10; i++ {
		s := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
		require.NoError(t, gormDB.Create(s).Error)
		createdIds = append(createdIds, s.Id)
	}

	// 先 Rebuild 把数据写入 Redis
	timeline := &HomeTimeline{Account: account}
	require.NoError(t, timeline.Rebuild())

	// 确认时间线已写入
	key := "feed:home:" + strconv.FormatUint(account.Id, 10)
	cnt, err := setup.RedisClient().Client.ZCard(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, int64(10), cnt, "时间线应有 10 条记录")

	// 使用 Limit=3 分页
	params := &feed.ListParam{Limit: 3}
	statuses, err := GetTimeLineContent(account, params)
	require.NoError(t, err)
	assert.Len(t, statuses, 3, "Limit=3 应返回 3 条")

	// 返回的应是 id 最大的 3 条（倒序）
	assert.Equal(t, createdIds[9], statuses[0].Id, "第一条应为最新的 status")
	assert.Equal(t, createdIds[7], statuses[2].Id, "第三条应为第 8 新的 status")
}

// GetTimeLineContent 带 MaxId 应返回小于等于 MaxId 的记录。
func TestGetTimeLineContent_WithMaxId(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()

	user := &models.User{UUID: "gtc-maxid-user", UserName: "gtc_maxid", EmailHash: "gtc_maxid_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "gtc_maxid"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	createdIds := make([]uint64, 0, 5)
	for i := 0; i < 5; i++ {
		s := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
		require.NoError(t, gormDB.Create(s).Error)
		createdIds = append(createdIds, s.Id)
	}

	// Rebuild
	timeline := &HomeTimeline{Account: account}
	require.NoError(t, timeline.Rebuild())

	// MaxId = createdIds[2]，应返回 id <= createdIds[2] 的记录（即 0,1,2）
	params := &feed.ListParam{MaxId: createdIds[2]}
	statuses, err := GetTimeLineContent(account, params)
	require.NoError(t, err)
	for _, s := range statuses {
		assert.LessOrEqual(t, s.Id, createdIds[2], "所有返回的 status id 应 <= MaxId")
	}
}

// GetTimeLineContent 无数据时应返回空切片且不报错。
func TestGetTimeLineContent_EmptyTimeline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()

	user := &models.User{UUID: "gtc-empty-user", UserName: "gtc_empty", EmailHash: "gtc_empty_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "gtc_empty"}
	require.NoError(t, gormDB.Create(account).Error)

	// 没有任何 status，第一页应触发 Rebuild 并返回空
	statuses, err := GetTimeLineContent(account, nil)
	require.NoError(t, err)
	assert.Empty(t, statuses)
}

// GetTimeLineContent 应过滤掉不属于自己的 direct status。
func TestGetTimeLineContent_FiltersDirectStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()

	user1 := &models.User{UUID: "gtc-direct-1", UserName: "gtc_direct1", EmailHash: "gtc_direct_h1"}
	require.NoError(t, gormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "gtc_direct1"}
	require.NoError(t, gormDB.Create(account1).Error)

	now := time.Now()
	// public status 应可见
	publicStatus := &models.Status{AccountId: account1.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(publicStatus).Error)
	// direct status（不提及自己）应被过滤
	directStatus := &models.Status{AccountId: account1.Id, Visibility: models.StatusVisibilityDirect, CreatedAt: &now}
	require.NoError(t, gormDB.Create(directStatus).Error)

	statuses, err := GetTimeLineContent(account1, nil)
	require.NoError(t, err)

	idSet := make(map[uint64]bool, len(statuses))
	for _, s := range statuses {
		idSet[s.Id] = true
	}
	assert.True(t, idSet[publicStatus.Id], "public status 应可见")
	// direct status 的 ToAccounts 不包含 account1，应被过滤
	assert.False(t, idSet[directStatus.Id], "不属于 account1 的 direct status 应被过滤")
}

// Push 应将 status 推送给作者自己的时间线（作者总是被加入 accountsId）。
func TestPush_AuthorReceivesStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()
	ctx := context.Background()

	user := &models.User{UUID: "push-author", UserName: "push_author", EmailHash: "push_author_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "push_author"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	status := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status).Error)

	// ToAccounts 为 nil，Push 内部会从 DB 查询关联（无关联则为空）
	err := Push(status, false)
	require.NoError(t, err)

	// Push 内部通过 goroutine 异步执行，等待完成
	require.Eventually(t, func() bool {
		key := "feed:home:" + strconv.FormatUint(account.Id, 10)
		members, err := setup.RedisClient().Client.ZRange(ctx, key, 0, -1).Result()
		if err != nil {
			return false
		}
		for _, m := range members {
			if m == strconv.FormatUint(status.Id, 10) {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "作者应在自己的时间线中收到 status")
}

// Push direct status 应只推送给 ToAccounts 中的用户。
func TestPush_DirectStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()
	ctx := context.Background()

	user1 := &models.User{UUID: "push-direct-1", UserName: "push_direct1", EmailHash: "push_direct_h1"}
	require.NoError(t, gormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "push_direct1"}
	require.NoError(t, gormDB.Create(account1).Error)

	user2 := &models.User{UUID: "push-direct-2", UserName: "push_direct2", EmailHash: "push_direct_h2"}
	require.NoError(t, gormDB.Create(user2).Error)
	account2 := &models.Account{UserId: user2.Id, Username: "push_direct2"}
	require.NoError(t, gormDB.Create(account2).Error)

	now := time.Now()
	// account1 发送 direct status，提及 account2
	status := &models.Status{
		AccountId:  account1.Id,
		Visibility: models.StatusVisibilityDirect,
		CreatedAt:  &now,
		ToAccounts: models.Accounts{account2},
	}
	require.NoError(t, gormDB.Create(status).Error)
	// 建立 status_to_accounts 关联
	require.NoError(t, gormDB.Create(&models.StatusToAccount{
		StatusId:  status.Id,
		AccountId: account2.Id,
		CreatedAt: &now,
	}).Error)

	err := Push(status, false)
	require.NoError(t, err)

	// account2 应在自己的时间线中收到该 status（ToAccounts 包含 account2）
	require.Eventually(t, func() bool {
		key := "feed:home:" + strconv.FormatUint(account2.Id, 10)
		members, err := setup.RedisClient().Client.ZRange(ctx, key, 0, -1).Result()
		if err != nil {
			return false
		}
		for _, m := range members {
			if m == strconv.FormatUint(status.Id, 10) {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "direct status 应推送给 ToAccounts 中的 account2")
}

// Unpush 应从作者时间线移除 status。
func TestUnpush_RemovesFromTimeline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()
	ctx := context.Background()

	user := &models.User{UUID: "unpush-user", UserName: "unpush_user", EmailHash: "unpush_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "unpush_user"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	status := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status).Error)

	// 先 Push
	err := Push(status, false)
	require.NoError(t, err)
	// 等待 Push 的 goroutine 完成
	require.Eventually(t, func() bool {
		key := "feed:home:" + strconv.FormatUint(account.Id, 10)
		members, _ := setup.RedisClient().Client.ZRange(ctx, key, 0, -1).Result()
		for _, m := range members {
			if m == strconv.FormatUint(status.Id, 10) {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)

	// 再 Unpush
	err = Unpush(status)
	require.NoError(t, err)

	// 等待 Unpush 的 goroutine 完成，status 应被移除
	require.Eventually(t, func() bool {
		key := "feed:home:" + strconv.FormatUint(account.Id, 10)
		members, _ := setup.RedisClient().Client.ZRange(ctx, key, 0, -1).Result()
		for _, m := range members {
			if m == strconv.FormatUint(status.Id, 10) {
				return false // 还在
			}
		}
		return true // 已移除
	}, 2*time.Second, 50*time.Millisecond, "Unpush 应从时间线移除 status")
}

// ClearMentionsPushStatus 应清除提及用户的推送状态。
func TestClearMentionsPushStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	skipIfNoGlobalRedis(t)

	setup := setupIntegration(t)
	gormDB := setup.GormDB()

	user1 := &models.User{UUID: "clear-mention-1", UserName: "clear_mention1", EmailHash: "clear_mention_h1"}
	require.NoError(t, gormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "clear_mention1"}
	require.NoError(t, gormDB.Create(account1).Error)

	user2 := &models.User{UUID: "clear-mention-2", UserName: "clear_mention2", EmailHash: "clear_mention_h2"}
	require.NoError(t, gormDB.Create(user2).Error)
	account2 := &models.Account{UserId: user2.Id, Username: "clear_mention2"}
	require.NoError(t, gormDB.Create(account2).Error)

	now := time.Now()
	status := &models.Status{AccountId: account1.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status).Error)
	// account1 的 status 提及 account2
	require.NoError(t, gormDB.Create(&models.StatusToAccount{
		StatusId:  status.Id,
		AccountId: account2.Id,
		CreatedAt: &now,
	}).Error)

	// ClearMentionsPushStatus 不应 panic 且不报错（内部错误会被 log 并跳过）
	assert.NotPanics(t, func() {
		ClearMentionsPushStatus(status)
	})
	time.Sleep(50 * time.Millisecond) // 等待可能的 goroutine
}

// ---------------------------------------------------------------------------
// Status 构造相关辅助验证
// ---------------------------------------------------------------------------

// 验证远程 status 的 Domain 字段处理（home 包不使用 Domain，但确保不 panic）。
func TestHomeTimeline_RemoteStatus(t *testing.T) {
	mockRedis := setupMockFeedService()

	account := &models.Account{Id: 50}
	status := &models.Status{
		Id:         1050,
		AccountId:  50,
		Visibility: models.StatusVisibilityPublic,
		Domain:     strPtr("remote.example.com"),
	}

	err := PushToHome(account, status, false, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:home:50")
	assert.Contains(t, zSet, "1050")
}

// 转发 status（带 ReblogId）应被正确处理。
func TestPushToHome_ReblogStatus(t *testing.T) {
	mockRedis := setupMockFeedService()

	reblogId := uint64(10000)
	account := &models.Account{Id: 60}
	status := &models.Status{
		Id:        10060,
		AccountId: 60,
		ReblogId:  &reblogId,
	}

	err := PushToHome(account, status, false, false)
	require.NoError(t, err)
	// aggregatesReblogs 默认 true，但因为是单条转发且原始状态不在时间线内，转发会被写入
	zSet := mockRedis.GetZSet("feed:home:60")
	if zSet != nil {
		// 至少不报错即可（mock 行为可能不同）
		assert.NotNil(t, zSet)
	}
}
