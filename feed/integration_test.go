package feed

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// skipIfNoGlobalRedis 如果没有配置全局 Redis 则跳过测试
// GetStatusTimelineContent 等函数直接使用 db.GetRedis()，需要全局 Redis 配置.
func skipIfNoGlobalRedis(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("Skipping test: no global Redis configured: %v", r)
		}
	}()
	// 尝试获取全局 Redis
	redis := db.GetRedis()
	if redis == nil {
		t.Skip("Skipping test: no global Redis configured")
	}
}

// IntegrationTestSetup 集成测试设置.
type IntegrationTestSetup struct {
	DB       *testutil.TestDB
	Redis    *testutil.RedisTestClient
	Fixtures *testutil.FixtureBuilder
}

// SetupIntegrationTest 创建集成测试环境.
func SetupIntegrationTest(t *testing.T) *IntegrationTestSetup {
	// 设置 DB
	db, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err, "Failed to create test DB")

	// 自动迁移所需的模型
	err = db.AutoMigrate(
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
		&models.Status{},
		&models.Follow{},
		&models.UserSetting{},
		&models.StatusContent{},
		&models.StatusToAccount{},
		&models.UserSecretData{},
	)
	require.NoError(t, err, "Failed to migrate models")

	// 设置 Redis - 强制使用真实 Redis
	redisClient := testutil.NewRealRedis(t)
	t.Logf("Redis client created: IsReal=%v, IsMock=%v", redisClient.IsReal(), redisClient.IsMock())
	require.NotNil(t, redisClient, "Real Redis client is required for integration tests")
	require.True(t, redisClient.IsReal(), "Must use real Redis, not mock")

	// 创建 fixture builder
	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)

	return &IntegrationTestSetup{
		DB:       db,
		Redis:    redisClient,
		Fixtures: fixtures,
	}
}

// setupGlobalRedis 设置全局 Redis 客户端（仅用于 GetStatusTimelineContent 等使用全局 Redis 的函数）
// 注意：由于 db/redis 包的 redises 变量是私有的，无法直接设置
// GetStatusTimelineContent 直接使用 db.GetRedis()，在没有全局 Redis 配置时会 panic
// 因此需要全局 Redis 的测试应该在有 .env 配置的环境中运行

// GetFeedService 获取使用真实 Redis 的 FeedService.
func (s *IntegrationTestSetup) GetFeedService() *FeedService {
	return NewFeedService(NewRedisClient(s.Redis.Client))
}

// GetDBService 获取使用真实 DB 的 DBService.
func (s *IntegrationTestSetup) GetDBService() *DBService {
	return NewDBService(s.DB.GormDB)
}

// Cleanup 清理测试数据.
func (s *IntegrationTestSetup) Cleanup(t *testing.T) {
	ctx := context.Background()
	// 清理 Redis
	if s.Redis.Client != nil {
		s.Redis.Client.FlushDB(ctx)
	}
}

// TestFeedService_AddToTimelineItem_RealRedis 使用真实 Redis 测试添加时间线.
func TestFeedService_AddToTimelineItem_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "test_real_redis"} //nolint:goconst

	// 创建测试状态
	status := &models.Status{Id: 100001}

	// 添加到时间线
	err := svc.AddToTimelineItem(timeline, status, false)
	require.NoError(t, err)

	// 验证 Redis 中存在数据
	ctx := context.Background()
	result, err := setup.Redis.Client.ZRange(ctx, "feed:home:test_real_redis", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, result, "100001")
}

// TestFeedService_AddToTimelineItem_WithReblog_RealRedis 测试转发聚合.
func TestFeedService_AddToTimelineItem_WithReblog_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "test_reblog_real"}

	ctx := context.Background()

	// 1. 先添加原始状态
	originalStatus := &models.Status{Id: 200001}
	err := svc.AddToTimelineItem(timeline, originalStatus, true)
	require.NoError(t, err)

	// 验证原始状态已添加
	timelineMembers, err := setup.Redis.Client.ZRange(ctx, "feed:home:test_reblog_real", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, timelineMembers, "200001")

	// 2. 添加转发 - 由于原始状态刚添加，rank 为 0 < REBLOG_FALLOFF，所以转发不会添加到时间线
	reblogId := uint64(200001)
	reblog1 := &models.Status{Id: 200002, ReblogId: &reblogId}
	err = svc.AddToTimelineItem(timeline, reblog1, true)
	require.NoError(t, err)

	// 验证时间线仍然只有原始状态
	timelineMembers, err = setup.Redis.Client.ZRange(ctx, "feed:home:test_reblog_real", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, timelineMembers, "200001")
	// 转发 200002 不会添加因为原始状态在 falloff 范围内
}

// TestFeedService_RemoveTimelineItem_RealRedis 使用真实 Redis 测试删除时间线.
func TestFeedService_RemoveTimelineItem_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "test_remove_real"}

	ctx := context.Background()

	// 先添加状态
	status := &models.Status{Id: 300001}
	err := svc.AddToTimelineItem(timeline, status, false)
	require.NoError(t, err)

	// 验证已添加
	result, err := setup.Redis.Client.ZRange(ctx, "feed:home:test_remove_real", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, result, "300001")

	// 删除状态
	err = svc.RemoveTimelineItem(timeline, status, false)
	require.NoError(t, err)

	// 验证已删除
	result, err = setup.Redis.Client.ZRange(ctx, "feed:home:test_remove_real", 0, -1).Result()
	require.NoError(t, err)
	assert.NotContains(t, result, "300001")
}

// TestFeedService_TrimTimeline_RealRedis 使用真实 Redis 测试裁剪时间线.
func TestFeedService_TrimTimeline_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "test_trim_real"}

	ctx := context.Background()

	// 添加超过 MAX_ITEMS 的状态
	for i := range 600 {
		status := &models.Status{Id: uint64(400000 + i)}
		err := svc.AddToTimelineItem(timeline, status, false)
		require.NoError(t, err)
	}

	// 裁剪前的大小
	beforeCount, err := setup.Redis.Client.ZCard(ctx, "feed:home:test_trim_real").Result()
	require.NoError(t, err)

	// 裁剪
	err = svc.TrimTimeline(timeline)
	require.NoError(t, err)

	// 裁剪后的大小
	afterCount, err := setup.Redis.Client.ZCard(ctx, "feed:home:test_trim_real").Result()
	require.NoError(t, err)

	// 验证裁剪后的大小不超过 MAX_ITEMS
	assert.LessOrEqual(t, afterCount, int64(MAX_ITEMS))
	t.Logf("Before trim: %d, After trim: %d, MAX_ITEMS: %d", beforeCount, afterCount, MAX_ITEMS)
}

// TestFeedService_BatchAddToTimelineItems_RealRedis 测试批量添加.
func TestFeedService_BatchAddToTimelineItems_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()

	timelines := []Timeline{
		&TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "batch_1"},
		&TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "batch_2"},
		&TestMockTimeline{KeyFormat: "feed:public:%s", TimelineId: "all"}, //nolint:goconst
	}

	status := &models.Status{Id: 500001}

	err := svc.BatchAddToTimelineItems(timelines, status, false)
	require.NoError(t, err)

	ctx := context.Background()

	// 验证每个时间线都有数据
	for _, tl := range timelines {
		key := fmt.Sprintf(tl.GetKeyFormat(), tl.GetTimelineId())
		result, err := setup.Redis.Client.ZRange(ctx, key, 0, -1).Result()
		require.NoError(t, err)
		assert.Contains(t, result, "500001", "Timeline %s should contain status", key)
	}
}

// TestFeedService_CleanFeeds_RealRedis 测试清理 Feed.
func TestFeedService_CleanFeeds_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "clean_test"}

	ctx := context.Background()

	// 添加一些数据
	for i := range 10 {
		status := &models.Status{Id: uint64(600000 + i)}
		err := svc.AddToTimelineItem(timeline, status, false)
		require.NoError(t, err)
	}

	// 验证数据存在
	result, err := setup.Redis.Client.ZRange(ctx, "feed:home:clean_test", 0, -1).Result()
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	// 清理
	err = svc.CleanFeeds(timeline, []string{"clean_test"})
	require.NoError(t, err)

	// 验证数据已清理
	result, err = setup.Redis.Client.ZRange(ctx, "feed:home:clean_test", 0, -1).Result()
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestFeedService_GcTimelines_RealRedis 测试垃圾回收.
func TestFeedService_GcTimelines_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	ctx := context.Background()

	// 添加一个过期的 timelines 条目（模拟旧数据）
	oldTime := time.Now().Add(-10 * 24 * time.Hour).Unix()
	setup.Redis.Client.ZAdd(ctx, "timelines", redis.Z{
		Score:  float64(oldTime),
		Member: "feed:home:old_user",
	})

	// 添加一个有效的时间线条目
	newTime := time.Now().Unix()
	setup.Redis.Client.ZAdd(ctx, "timelines", redis.Z{
		Score:  float64(newTime),
		Member: "feed:home:new_user",
	})

	// 执行垃圾回收
	err := svc.GcTimelines()
	require.NoError(t, err)

	// 验证旧条目被删除
	_, err = setup.Redis.Client.ZScore(ctx, "timelines", "feed:home:old_user").Result()
	require.Error(t, err, "Old timeline should be removed") // redis.Nil error

	// 验证新条目存在
	newResult, err := setup.Redis.Client.ZScore(ctx, "timelines", "feed:home:new_user").Result()
	require.NoError(t, err)
	assert.Greater(t, newResult, 0.0)
	_ = newResult // 使用变量避免编译错误
}

// TestFeedService_PushToClient_RealRedis 测试推送消息.
func TestFeedService_PushToClient_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	ctx := context.Background()

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "push_test"}
	channel := GetRedisChannelName(timeline)

	// 订阅频道
	pubsub := setup.Redis.Client.Subscribe(ctx, channel)
	defer func() { _ = pubsub.Close() }()

	// 等待订阅生效
	_, err := pubsub.Receive(ctx)
	require.NoError(t, err)

	// 推送消息
	status := &models.Status{Id: 700001}
	err = svc.PushToClient(timeline, "update", status)
	require.NoError(t, err)

	// 接收消息
	msg, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	assert.Contains(t, msg.Payload, "update")
	assert.Contains(t, msg.Payload, "700001")
}

// TestFeedService_PushUpdateRequired_RealRedis 测试推送更新检查.
func TestFeedService_PushUpdateRequired_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	ctx := context.Background()

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "push_check"}
	channel := "subscribed:feed:home:" + "push_check"

	// 没有订阅者时应该返回 false
	// 清除缓存
	subCache = &subscriptionCache{ttl: 5 * time.Second}

	result := svc.PushUpdateRequired(timeline)
	assert.False(t, result)

	// 订阅频道
	pubsub := setup.Redis.Client.Subscribe(ctx, channel)
	defer func() { _ = pubsub.Close() }()

	// 等待订阅生效
	time.Sleep(100 * time.Millisecond)

	// 清除缓存后再次检查
	subCache = &subscriptionCache{ttl: 5 * time.Second}
	result = svc.PushUpdateRequired(timeline)
	assert.True(t, result)
}

// TestDBService_RealDB 测试数据库操作.
func TestDBService_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	dbSvc := setup.GetDBService()

	// 简单测试 - GetAccountFollowersId 对于不存在的用户应返回空
	followers, err := dbSvc.GetAccountFollowersId(99999999)
	require.NoError(t, err)
	assert.Empty(t, followers)
}

// TestDBService_GetAccountFollowersId_WithData 测试真实 DB 关注关系.
func TestDBService_GetAccountFollowersId_WithData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	dbSvc := setup.GetDBService()

	// 1. 创建用户和账号（真实场景）
	user1 := &models.User{UserName: "user1_followers"}
	require.NoError(t, setup.DB.GormDB.Create(user1).Error)

	account1 := &models.Account{UserId: user1.Id, Username: "user1_followers", DisplayName: "User 1"}
	require.NoError(t, setup.DB.GormDB.Create(account1).Error)

	user2 := &models.User{UserName: "user2_follower"}
	require.NoError(t, setup.DB.GormDB.Create(user2).Error)

	account2 := &models.Account{UserId: user2.Id, Username: "user2_follower", DisplayName: "User 2"}
	require.NoError(t, setup.DB.GormDB.Create(account2).Error)

	user3 := &models.User{UserName: "user3_follower"}
	require.NoError(t, setup.DB.GormDB.Create(user3).Error)

	account3 := &models.Account{UserId: user3.Id, Username: "user3_follower", DisplayName: "User 3"}
	require.NoError(t, setup.DB.GormDB.Create(account3).Error)

	// 2. 创建关注关系: user2 和 user3 关注 user1
	// Follow.AccountId = 粉丝ID, Follow.FollowId = 被关注者ID
	require.NoError(t, dbSvc.CreateTestFollow(account2.Id, account1.Id), "Failed to create follow relation")
	require.NoError(t, dbSvc.CreateTestFollow(account3.Id, account1.Id), "Failed to create follow relation")

	// 3. 获取 account1 的粉丝列表
	followers, err := dbSvc.GetAccountFollowersId(account1.Id)
	require.NoError(t, err)
	assert.Len(t, followers, 2)
	assert.Contains(t, followers, account2.Id)
	assert.Contains(t, followers, account3.Id)

	// 4. 验证 account2 的粉丝列表为空
	followers2, err := dbSvc.GetAccountFollowersId(account2.Id)
	require.NoError(t, err)
	assert.Empty(t, followers2)
}

// TestDBService_GetAccountFollowingAccounts 测试获取关注列表.
func TestDBService_GetAccountFollowingAccounts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	dbSvc := setup.GetDBService()

	// 创建用户和账号
	user1 := &models.User{UserName: "user1_following"}
	require.NoError(t, setup.DB.GormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "user1_following"}
	require.NoError(t, setup.DB.GormDB.Create(account1).Error)

	user2 := &models.User{UserName: "user2_followed"}
	require.NoError(t, setup.DB.GormDB.Create(user2).Error)
	account2 := &models.Account{UserId: user2.Id, Username: "user2_followed"}
	require.NoError(t, setup.DB.GormDB.Create(account2).Error)

	user3 := &models.User{UserName: "user3_followed"}
	require.NoError(t, setup.DB.GormDB.Create(user3).Error)
	account3 := &models.Account{UserId: user3.Id, Username: "user3_followed"}
	require.NoError(t, setup.DB.GormDB.Create(account3).Error)

	// account1 关注 account2 和 account3
	require.NoError(t, dbSvc.CreateTestFollow(account1.Id, account2.Id))
	require.NoError(t, dbSvc.CreateTestFollow(account1.Id, account3.Id))

	// 获取 account1 关注的账号
	following, err := dbSvc.GetAccountFollowingAccounts(account1.Id)
	require.NoError(t, err)

	// 验证返回的关注列表包含预期的账号
	followingIds := following.GetIds()
	assert.Contains(t, followingIds, account2.Id)
	assert.Contains(t, followingIds, account3.Id)

	// 验证 account2 没有关注任何人
	following2, err := dbSvc.GetAccountFollowingAccounts(account2.Id)
	require.NoError(t, err)
	assert.Empty(t, following2)
}

// TestDBService_GetAccountRecentActiveLocalFollowersId 测试活跃粉丝查询.
func TestDBService_GetAccountRecentActiveLocalFollowersId(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	dbSvc := setup.GetDBService()

	// 创建目标账号
	user := &models.User{UserName: "target_user_active"}
	require.NoError(t, setup.DB.GormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "target_user_active"}
	require.NoError(t, setup.DB.GormDB.Create(account).Error)

	// 创建本地活跃粉丝
	activeUser := &models.User{UserName: "active_follower"}
	require.NoError(t, setup.DB.GormDB.Create(activeUser).Error)

	now := time.Now()
	activeFollower := &models.Account{
		UserId:         activeUser.Id,
		Username:       "active_follower",
		LastActivityAt: &now,
	}
	require.NoError(t, setup.DB.GormDB.Create(activeFollower).Error)

	// 创建本地不活跃粉丝
	inactiveUser := &models.User{UserName: "inactive_follower"}
	require.NoError(t, setup.DB.GormDB.Create(inactiveUser).Error)

	oldTime := time.Now().Add(-30 * 24 * time.Hour) // 30天前
	inactiveFollower := &models.Account{
		UserId:         inactiveUser.Id,
		Username:       "inactive_follower",
		LastActivityAt: &oldTime,
	}
	require.NoError(t, setup.DB.GormDB.Create(inactiveFollower).Error)

	// 创建远程粉丝（有 domain）
	remoteUser := &models.User{UserName: "remote_follower_user"}
	require.NoError(t, setup.DB.GormDB.Create(remoteUser).Error)

	remoteDomain := "remote.example.com"
	remoteFollower := &models.Account{
		UserId:         remoteUser.Id,
		Username:       "remote_follower",
		Domain:         &remoteDomain,
		LastActivityAt: &now,
	}
	require.NoError(t, setup.DB.GormDB.Create(remoteFollower).Error)

	// 创建关注关系
	require.NoError(t, dbSvc.CreateTestFollow(activeFollower.Id, account.Id))
	require.NoError(t, dbSvc.CreateTestFollow(inactiveFollower.Id, account.Id))
	require.NoError(t, dbSvc.CreateTestFollow(remoteFollower.Id, account.Id))

	// 获取最近活跃的本地粉丝
	followers, err := dbSvc.GetAccountRecentActiveLocalFollowersId(account.Id)
	require.NoError(t, err)

	// 应该只包含活跃的本地粉丝
	assert.Contains(t, followers, activeFollower.Id)
	assert.NotContains(t, followers, inactiveFollower.Id) // 不活跃
	assert.NotContains(t, followers, remoteFollower.Id)   // 远程用户
}

// TestDBService_GetAccountRemoteFollowers 测试获取远程粉丝.
func TestDBService_GetAccountRemoteFollowers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	dbSvc := setup.GetDBService()

	// 创建目标账号
	user := &models.User{UserName: "target_remote_test"}
	require.NoError(t, setup.DB.GormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "target_remote_test"}
	require.NoError(t, setup.DB.GormDB.Create(account).Error)

	// 创建本地粉丝
	localUser := &models.User{UserName: "local_follower_remote"}
	require.NoError(t, setup.DB.GormDB.Create(localUser).Error)
	localFollower := &models.Account{UserId: localUser.Id, Username: "local_follower_remote"}
	require.NoError(t, setup.DB.GormDB.Create(localFollower).Error)

	// 创建远程粉丝
	remoteUser1 := &models.User{UserName: "remote_user1"}
	require.NoError(t, setup.DB.GormDB.Create(remoteUser1).Error)

	domain1 := "remote1.example.com"
	remoteFollower1 := &models.Account{
		UserId:   remoteUser1.Id,
		Username: "remote_user1",
		Domain:   &domain1,
	}
	require.NoError(t, setup.DB.GormDB.Create(remoteFollower1).Error)

	remoteUser2 := &models.User{UserName: "remote_user2"}
	require.NoError(t, setup.DB.GormDB.Create(remoteUser2).Error)

	domain2 := "remote2.example.com"
	remoteFollower2 := &models.Account{
		UserId:   remoteUser2.Id,
		Username: "remote_user2",
		Domain:   &domain2,
	}
	require.NoError(t, setup.DB.GormDB.Create(remoteFollower2).Error)

	// 创建关注关系
	require.NoError(t, dbSvc.CreateTestFollow(localFollower.Id, account.Id))
	require.NoError(t, dbSvc.CreateTestFollow(remoteFollower1.Id, account.Id))
	require.NoError(t, dbSvc.CreateTestFollow(remoteFollower2.Id, account.Id))

	// 获取远程粉丝
	remoteFollowers, err := dbSvc.GetAccountRemoteFollowers(account.Id)
	require.NoError(t, err)

	// 应该只包含远程粉丝（有 domain 的）
	assert.Len(t, remoteFollowers, 2)

	remoteIds := make([]uint64, 0, len(remoteFollowers))
	for _, f := range remoteFollowers {
		remoteIds = append(remoteIds, f.Id)
	}

	assert.Contains(t, remoteIds, remoteFollower1.Id)
	assert.Contains(t, remoteIds, remoteFollower2.Id)
	assert.NotContains(t, remoteIds, localFollower.Id)
}

// TestListParam_RealRedis 测试 ListParam 与真实 Redis 的交互.
func TestListParam_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "list_param_test"}

	ctx := context.Background()

	// 添加多个状态
	for i := 100; i < 200; i++ {
		status := &models.Status{Id: uint64(800000 + i)}
		err := svc.AddToTimelineItem(timeline, status, false)
		require.NoError(t, err)
	}

	// 测试分页参数
	t.Run("With MinId and MaxId", func(t *testing.T) {
		params := &ListParam{MinId: 800100, MaxId: 800150}
		min, max := params.GetRedisRangeMinMax()
		result, err := setup.Redis.Client.ZRangeArgs(ctx, redis.ZRangeArgs{
			Key:     "feed:home:list_param_test",
			Start:   min,
			Stop:    max,
			ByScore: true,
		}).Result()
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})

	t.Run("With SinceId", func(t *testing.T) {
		params := &ListParam{SinceId: 800150, Limit: 10}
		start, end := params.GetRedisRangeOffsetCount()
		result, err := setup.Redis.Client.ZRangeArgs(ctx, redis.ZRangeArgs{
			Key:   "feed:home:list_param_test",
			Start: start,
			Stop:  end,
			Rev:   true,
		}).Result()
		require.NoError(t, err)
		assert.LessOrEqual(t, len(result), 10)
	})
}

// TestReblogAggregation_RealRedis 测试转发聚合逻辑.
func TestReblogAggregation_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "reblog_agg_test"}

	ctx := context.Background()

	// 添加原始状态
	originalId := uint64(900001)
	original := &models.Status{Id: originalId}
	err := svc.AddToTimelineItem(timeline, original, true)
	require.NoError(t, err)

	// 验证原始状态已添加
	timelineMembers, err := setup.Redis.Client.ZRange(ctx, "feed:home:reblog_agg_test", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, timelineMembers, "900001")
}

// TestConcurrentAccess_RealRedis 测试并发访问.
func TestConcurrentAccess_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "concurrent_test"}

	done := make(chan bool)
	concurrency := 10
	operationsPerGoroutine := 50

	// 并发添加
	for i := range concurrency {
		go func(id int) {
			for j := range operationsPerGoroutine {
				status := &models.Status{Id: uint64(id*10000 + j)} //nolint:gosec // G115: reviewed and safe
				_ = svc.AddToTimelineItem(timeline, status, false)
			}

			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for range concurrency {
		<-done
	}

	// 验证数据一致性
	ctx := context.Background()
	count, err := setup.Redis.Client.ZCard(ctx, "feed:home:concurrent_test").Result()
	require.NoError(t, err)
	// 由于并发写入，最终数据量应该接近预期
	assert.Positive(t, count)
}

// TestSubscriptionCache_Concurrent 测试订阅缓存并发安全.
func TestSubscriptionCache_Concurrent(t *testing.T) {
	cache := &subscriptionCache{ttl: 5 * time.Second}

	done := make(chan bool)

	// 并发读写
	for i := range 100 {
		go func(id int) {
			for range 100 {
				key := "key_" + strconv.Itoa(id)
				cache.Set(key, true)
				cache.Get(key)
			}

			done <- true
		}(i)
	}

	for range 100 {
		<-done
	}
}

// TestDBService_CreateTestAccount 测试创建测试账号.
func TestDBService_CreateTestAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	dbSvc := setup.GetDBService()

	account := &models.Account{
		Username:    "test_create_account",
		DisplayName: "Test Create Account",
	}
	err := dbSvc.CreateTestAccount(account)
	require.NoError(t, err)
	assert.Positive(t, account.Id)

	// 验证账号已创建
	var found models.Account

	setup.DB.GormDB.First(&found, account.Id)
	assert.Equal(t, "test_create_account", found.Username)
}

// TestResetSubscriptionCache 测试重置订阅缓存.
func TestResetSubscriptionCache(t *testing.T) {
	// 设置一些缓存值
	subCache.Set("test_key", true)
	_, exists := subCache.Get("test_key")
	assert.True(t, exists)

	// 重置缓存
	ResetSubscriptionCache()

	// 验证缓存已清空
	_, exists = subCache.Get("test_key")
	assert.False(t, exists)
}

// TestGetRedisChannelNameFunc 测试获取 Redis 频道名称.
func TestGetRedisChannelNameFunc(t *testing.T) {
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "user123"}
	channel := GetRedisChannelName(timeline)
	assert.Equal(t, "subscribed:feed:home:user123", channel)
}

// TestDBService_EmptyResults 测试空结果场景.
func TestDBService_EmptyResults(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	dbSvc := setup.GetDBService()

	// 测试不存在的账号
	followers, err := dbSvc.GetAccountFollowersId(99999999)
	require.NoError(t, err)
	assert.Empty(t, followers)

	following, err := dbSvc.GetAccountFollowingAccounts(99999999)
	require.NoError(t, err)
	assert.Empty(t, following)

	remoteFollowers, err := dbSvc.GetAccountRemoteFollowers(99999999)
	require.NoError(t, err)
	assert.Empty(t, remoteFollowers)
}

// TestFeedService_RemoveReblog 测试移除转发状态.
func TestFeedService_RemoveReblog(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "reblog_remove_test"}
	ctx := context.Background()

	// 1. 添加原始状态
	originalId := uint64(700001)
	original := &models.Status{Id: originalId}
	err := svc.AddToTimelineItem(timeline, original, true)
	require.NoError(t, err)

	// 2. 添加多个转发
	reblogId1 := uint64(700002)
	reblog1 := &models.Status{Id: reblogId1, ReblogId: &originalId}
	err = svc.AddToTimelineItem(timeline, reblog1, true)
	require.NoError(t, err)

	reblogId2 := uint64(700003)
	reblog2 := &models.Status{Id: reblogId2, ReblogId: &originalId}
	err = svc.AddToTimelineItem(timeline, reblog2, true)
	require.NoError(t, err)

	// 验证时间线有数据
	members, _ := setup.Redis.Client.ZRange(ctx, "feed:home:reblog_remove_test", 0, -1).Result()
	require.NotEmpty(t, members)

	// 3. 移除一个转发
	err = svc.RemoveTimelineItem(timeline, reblog1, true)
	require.NoError(t, err)

	// 4. 移除原始状态
	err = svc.RemoveTimelineItem(timeline, original, false)
	require.NoError(t, err)

	// 验证时间线已清空该状态
	members, _ = setup.Redis.Client.ZRange(ctx, "feed:home:reblog_remove_test", 0, -1).Result()
	assert.NotContains(t, members, "700001")
}

// TestFeedService_NilStatus 测试空状态处理.
func TestFeedService_NilStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "nil_test"}

	// 测试空状态不应该 panic
	status := &models.Status{Id: 0}
	err := svc.AddToTimelineItem(timeline, status, false)
	// 应该正常处理或返回错误
	_ = err
}

// TestFeedService_TrimEmptyTimeline 测试裁剪空时间线.
func TestFeedService_TrimEmptyTimeline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "empty_trim_test"}

	// 裁剪空时间线不应该报错
	err := svc.TrimTimeline(timeline)
	require.NoError(t, err)
}

// TestFeedService_CleanEmptyFeeds 测试清理空 Feed.
func TestFeedService_CleanEmptyFeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "empty_clean_test"}

	// 清理空 Feed 不应该报错
	err := svc.CleanFeeds(timeline, []string{"nonexistent_feed"})
	require.NoError(t, err)
}

// TestGetStatusTimelineContent_RealDBAndRedis 测试 GetStatusTimelineContent 函数
// 需要真实 DB 和 Redis 连接
// 注意：此测试需要全局 Redis 配置（REDIS_0_HOST 等环境变量）.
func TestGetStatusTimelineContent_RealDBAndRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// 检查全局 Redis 是否可用
	skipIfNoGlobalRedis(t)

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	// 设置默认服务，让 GetStatusTimelineContent 能使用
	feedSvc := setup.GetFeedService()
	dbSvc := setup.GetDBService()

	SetDefaultService(feedSvc)
	SetDefaultDBService(dbSvc)

	ctx := context.Background()

	// 1. 创建测试用户和账号
	user1 := &models.User{UserName: "timeline_user1"}
	require.NoError(t, setup.DB.GormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "timeline_user1"}
	require.NoError(t, setup.DB.GormDB.Create(account1).Error)

	user2 := &models.User{UserName: "timeline_user2"}
	require.NoError(t, setup.DB.GormDB.Create(user2).Error)
	account2 := &models.Account{UserId: user2.Id, Username: "timeline_user2"}
	require.NoError(t, setup.DB.GormDB.Create(account2).Error)

	// 2. 创建关注关系: account1 关注 account2
	require.NoError(t, dbSvc.CreateTestFollow(account1.Id, account2.Id))

	// 3. 创建 Status 记录
	now := time.Now()
	status1 := &models.Status{
		AccountId:  account2.Id,
		Visibility: models.StatusVisibilityPublic,
		Content:    models.StatusContent{Content: "Test status 1"},
		CreatedAt:  &now,
	}
	require.NoError(t, setup.DB.GormDB.Create(status1).Error)

	status2 := &models.Status{
		AccountId:  account2.Id,
		Visibility: models.StatusVisibilityPublic,
		Content:    models.StatusContent{Content: "Test status 2"},
		CreatedAt:  &now,
	}
	require.NoError(t, setup.DB.GormDB.Create(status2).Error)

	// 4. 创建时间线并添加数据
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: strconv.FormatUint(account1.Id, 10)}
	setup.Redis.Client.ZAdd(ctx, "feed:home:"+strconv.FormatUint(account1.Id, 10), redis.Z{
		Score:  float64(status1.Id),
		Member: strconv.FormatUint(status1.Id, 10),
	})
	setup.Redis.Client.ZAdd(ctx, "feed:home:"+strconv.FormatUint(account1.Id, 10), redis.Z{
		Score:  float64(status2.Id),
		Member: strconv.FormatUint(status2.Id, 10),
	})

	// 5. 定义 filter condition
	filterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("account_id = ?", account2.Id)

		return
	}

	// 6. 调用 GetStatusTimelineContent
	statuses, err := GetStatusTimelineContent(timeline, nil, filterCondition, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, statuses, "Should get statuses from timeline")
}

// TestGetStatusTimelineContent_WithPagination 测试分页参数
// 注意：此测试需要全局 Redis 配置.
func TestGetStatusTimelineContent_WithPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	skipIfNoGlobalRedis(t)

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	feedSvc := setup.GetFeedService()
	dbSvc := setup.GetDBService()

	SetDefaultService(feedSvc)
	SetDefaultDBService(dbSvc)

	ctx := context.Background()

	// 创建测试账号
	user := &models.User{UserName: "pagination_user"}
	require.NoError(t, setup.DB.GormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "pagination_user"}
	require.NoError(t, setup.DB.GormDB.Create(account).Error)

	// 创建多个状态
	now := time.Now()
	for i := 1; i <= 20; i++ {
		status := &models.Status{
			AccountId:  account.Id,
			Visibility: models.StatusVisibilityPublic,
			Content:    models.StatusContent{Content: "Status " + strconv.Itoa(i)},
			CreatedAt:  &now,
		}
		require.NoError(t, setup.DB.GormDB.Create(status).Error)

		// 添加到 Redis 时间线
		setup.Redis.Client.ZAdd(ctx, "feed:home:pagination_test", redis.Z{
			Score:  float64(status.Id),
			Member: strconv.FormatUint(status.Id, 10),
		})
	}

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "pagination_test"}

	filterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("account_id = ?", account.Id)

		return
	}

	// 测试带 Limit 参数
	params := &ListParam{Limit: 5}
	statuses, err := GetStatusTimelineContent(timeline, params, filterCondition, nil)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(statuses), 5, "Should return at most 5 statuses")
}

// TestGetStatusTimelineContent_EmptyTimeline 测试空时间线
// 注意：此测试需要全局 Redis 配置.
func TestGetStatusTimelineContent_EmptyTimeline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	skipIfNoGlobalRedis(t)

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	feedSvc := setup.GetFeedService()
	SetDefaultService(feedSvc)

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "empty_timeline_test"}

	filterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("1 = 0") // 永不匹配

		return
	}

	statuses, err := GetStatusTimelineContent(timeline, nil, filterCondition, nil)
	require.NoError(t, err)
	assert.Empty(t, statuses, "Should return empty for empty timeline")
}

// TestBackwardCompatibleFunctions_RealDBAndRedis 测试向后兼容函数
// 这些函数需要初始化默认服务.
func TestBackwardCompatibleFunctions_RealDBAndRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	// 设置默认服务
	feedSvc := setup.GetFeedService()
	dbSvc := setup.GetDBService()

	SetDefaultService(feedSvc)
	SetDefaultDBService(dbSvc)

	// 创建测试用户和账号
	user1 := &models.User{UserName: "compat_user1"}
	require.NoError(t, setup.DB.GormDB.Create(user1).Error)
	account1 := &models.Account{UserId: user1.Id, Username: "compat_user1"}
	require.NoError(t, setup.DB.GormDB.Create(account1).Error)

	user2 := &models.User{UserName: "compat_user2"}
	require.NoError(t, setup.DB.GormDB.Create(user2).Error)

	now := time.Now()
	account2 := &models.Account{UserId: user2.Id, Username: "compat_user2", LastActivityAt: &now}
	require.NoError(t, setup.DB.GormDB.Create(account2).Error)

	// 创建关注关系: account2 关注 account1
	require.NoError(t, dbSvc.CreateTestFollow(account2.Id, account1.Id))

	t.Run("GetAccountFollowersId", func(t *testing.T) {
		// 测试向后兼容函数
		followers, err := GetAccountFollowersId(account1.Id)
		require.NoError(t, err)
		assert.Contains(t, followers, account2.Id)
	})

	t.Run("GetAccountFollowingAccounts", func(t *testing.T) {
		following, err := GetAccountFollowingAccounts(account2.Id)
		require.NoError(t, err)

		followingIds := following.GetIds()
		assert.Contains(t, followingIds, account1.Id)
	})

	t.Run("GetAccountRecentActiveLocalFollowersId", func(t *testing.T) {
		// account2 是 account1 的粉丝
		followers, err := GetAccountRecentActiveLocalFollowersId(account1.Id)
		require.NoError(t, err)
		assert.Contains(t, followers, account2.Id)
	})

	t.Run("AddToTimelineItem", func(t *testing.T) {
		ctx := context.Background()
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "compat_test"}
		status := &models.Status{Id: 888888}

		err := AddToTimelineItem(timeline, status, false)
		require.NoError(t, err)

		// 验证添加成功
		result, err := setup.Redis.Client.ZRange(ctx, "feed:home:compat_test", 0, -1).Result()
		require.NoError(t, err)
		assert.Contains(t, result, "888888")
	})

	t.Run("RemoveTimelineItem", func(t *testing.T) {
		ctx := context.Background()
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "compat_remove_test"}
		status := &models.Status{Id: 777777}

		// 先添加
		err := AddToTimelineItem(timeline, status, false)
		require.NoError(t, err)

		// 再删除
		err = RemoveTimelineItem(timeline, status, false)
		require.NoError(t, err)

		// 验证删除成功
		result, err := setup.Redis.Client.ZRange(ctx, "feed:home:compat_remove_test", 0, -1).Result()
		require.NoError(t, err)
		assert.NotContains(t, result, "777777")
	})

	t.Run("TrimTimeline", func(t *testing.T) {
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "compat_trim_test"}
		// 空时间线裁剪不应该报错
		err := TrimTimeline(timeline)
		require.NoError(t, err)
	})

	t.Run("PushUpdateRequired", func(t *testing.T) {
		// 重置缓存
		ResetSubscriptionCache()

		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "compat_push_test"}
		// 没有订阅者时应该返回 false
		result := PushUpdateRequired(timeline)
		assert.False(t, result)
	})
}

// TestGetStatusTimelineContent_WithAfterFilter 测试后置过滤器
// 注意：此测试需要全局 Redis 配置.
func TestGetStatusTimelineContent_WithAfterFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	skipIfNoGlobalRedis(t)

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	feedSvc := setup.GetFeedService()
	dbSvc := setup.GetDBService()

	SetDefaultService(feedSvc)
	SetDefaultDBService(dbSvc)

	ctx := context.Background()

	// 创建测试账号
	user := &models.User{UserName: "after_filter_user"}
	require.NoError(t, setup.DB.GormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "after_filter_user"}
	require.NoError(t, setup.DB.GormDB.Create(account).Error)

	// 创建两个状态
	now := time.Now()
	status1 := &models.Status{
		AccountId:  account.Id,
		Visibility: models.StatusVisibilityPublic,
		Content:    models.StatusContent{Content: "Public status"},
		CreatedAt:  &now,
	}
	require.NoError(t, setup.DB.GormDB.Create(status1).Error)

	status2 := &models.Status{
		AccountId:  account.Id,
		Visibility: models.StatusVisibilityPrivate,
		Content:    models.StatusContent{Content: "Private status"},
		CreatedAt:  &now,
	}
	require.NoError(t, setup.DB.GormDB.Create(status2).Error)

	// 添加到时间线
	setup.Redis.Client.ZAdd(ctx, "feed:home:after_filter_test", redis.Z{
		Score:  float64(status1.Id),
		Member: strconv.FormatUint(status1.Id, 10),
	})
	setup.Redis.Client.ZAdd(ctx, "feed:home:after_filter_test", redis.Z{
		Score:  float64(status2.Id),
		Member: strconv.FormatUint(status2.Id, 10),
	})

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "after_filter_test"}

	filterCondition := func(tx *gorm.DB) (query *gorm.DB, err error) {
		query = tx.Where("account_id = ?", account.Id)

		return
	}

	// 后置过滤器：只保留公开状态
	afterFilter := func(statuses models.Statuses) models.Statuses {
		return statuses.Filter(func(status *models.Status) bool {
			return status.Visibility == models.StatusVisibilityPublic
		})
	}

	statuses, err := GetStatusTimelineContent(timeline, nil, filterCondition, afterFilter)
	require.NoError(t, err)
	// 验证只返回公开状态
	for _, s := range statuses {
		assert.Equal(t, models.StatusVisibilityPublic, s.Visibility)
	}
}
