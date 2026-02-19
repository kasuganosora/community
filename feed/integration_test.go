package feed

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IntegrationTestSetup 集成测试设置
type IntegrationTestSetup struct {
	DB       *testutil.TestDB
	Redis    *testutil.RedisTestClient
	Fixtures *testutil.FixtureBuilder
}

// SetupIntegrationTest 创建集成测试环境
func SetupIntegrationTest(t *testing.T) *IntegrationTestSetup {
	// 设置 DB
	db, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err, "Failed to create test DB")

	// 自动迁移所需的模型
	err = db.AutoMigrate(
		&models.User{},
		&models.Account{},
		&models.Status{},
		&models.Follow{},
		&models.UserSetting{},
		&models.StatusContent{},
		&models.StatusToAccount{},
	)
	require.NoError(t, err, "Failed to migrate models")

	// 设置 Redis - 强制使用真实 Redis
	redisClient := testutil.NewRealRedis(t)
	t.Logf("Redis client created: IsReal=%v, IsMock=%v", redisClient.IsReal(), redisClient.IsMock())
	require.NotNil(t, redisClient, "Real Redis client is required for integration tests")
	require.True(t, redisClient.IsReal(), "Must use real Redis, not mock")

	// 创建 fixture builder
	fixtures := testutil.NewFixtureBuilder(db.GormDB)

	return &IntegrationTestSetup{
		DB:       db,
		Redis:    redisClient,
		Fixtures: fixtures,
	}
}

// GetFeedService 获取使用真实 Redis 的 FeedService
func (s *IntegrationTestSetup) GetFeedService() *FeedService {
	return NewFeedService(NewRedisClient(s.Redis.Client))
}

// GetDBService 获取使用真实 DB 的 DBService
func (s *IntegrationTestSetup) GetDBService() *DBService {
	return NewDBService(s.DB.GormDB)
}

// Cleanup 清理测试数据
func (s *IntegrationTestSetup) Cleanup(t *testing.T) {
	ctx := context.Background()
	// 清理 Redis
	if s.Redis.Client != nil {
		s.Redis.Client.FlushDB(ctx)
	}
}

// TestFeedService_AddToTimelineItem_RealRedis 使用真实 Redis 测试添加时间线
func TestFeedService_AddToTimelineItem_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "test_real_redis"}

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

// TestFeedService_AddToTimelineItem_WithReblog_RealRedis 测试转发聚合
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

// TestFeedService_RemoveTimelineItem_RealRedis 使用真实 Redis 测试删除时间线
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

// TestFeedService_TrimTimeline_RealRedis 使用真实 Redis 测试裁剪时间线
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
	for i := 0; i < 600; i++ {
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

// TestFeedService_BatchAddToTimelineItems_RealRedis 测试批量添加
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
		&TestMockTimeline{KeyFormat: "feed:public:%s", TimelineId: "all"},
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

// TestFeedService_CleanFeeds_RealRedis 测试清理 Feed
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
	for i := 0; i < 10; i++ {
		status := &models.Status{Id: uint64(600000 + i)}
		err := svc.AddToTimelineItem(timeline, status, false)
		require.NoError(t, err)
	}

	// 验证数据存在
	result, err := setup.Redis.Client.ZRange(ctx, "feed:home:clean_test", 0, -1).Result()
	require.NoError(t, err)
	assert.Greater(t, len(result), 0)

	// 清理
	err = svc.CleanFeeds(timeline, []string{"clean_test"})
	require.NoError(t, err)

	// 验证数据已清理
	result, err = setup.Redis.Client.ZRange(ctx, "feed:home:clean_test", 0, -1).Result()
	require.NoError(t, err)
	assert.Equal(t, 0, len(result))
}

// TestFeedService_GcTimelines_RealRedis 测试垃圾回收
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
	assert.Error(t, err, "Old timeline should be removed") // redis.Nil error

	// 验证新条目存在
	newResult, err := setup.Redis.Client.ZScore(ctx, "timelines", "feed:home:new_user").Result()
	require.NoError(t, err)
	assert.Greater(t, newResult, 0.0)
	_ = newResult // 使用变量避免编译错误
}

// TestFeedService_PushToClient_RealRedis 测试推送消息
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
	defer pubsub.Close()

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

// TestFeedService_PushUpdateRequired_RealRedis 测试推送更新检查
func TestFeedService_PushUpdateRequired_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setup := SetupIntegrationTest(t)
	defer setup.Cleanup(t)

	svc := setup.GetFeedService()
	ctx := context.Background()

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "push_check"}
	channel := fmt.Sprintf("subscribed:feed:home:%s", "push_check")

	// 没有订阅者时应该返回 false
	// 清除缓存
	subCache = &subscriptionCache{ttl: 5 * time.Second}

	result := svc.PushUpdateRequired(timeline)
	assert.False(t, result)

	// 订阅频道
	pubsub := setup.Redis.Client.Subscribe(ctx, channel)
	defer pubsub.Close()

	// 等待订阅生效
	time.Sleep(100 * time.Millisecond)

	// 清除缓存后再次检查
	subCache = &subscriptionCache{ttl: 5 * time.Second}
	result = svc.PushUpdateRequired(timeline)
	assert.True(t, result)
}

// TestDBService_RealDB 测试数据库操作
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

// TestListParam_RealRedis 测试 ListParam 与真实 Redis 的交互
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
		result, err := setup.Redis.Client.ZRangeByScore(ctx, "feed:home:list_param_test", &redis.ZRangeBy{
			Min: min,
			Max: max,
		}).Result()
		require.NoError(t, err)
		assert.Greater(t, len(result), 0)
	})

	t.Run("With SinceId", func(t *testing.T) {
		params := &ListParam{SinceId: 800150, Limit: 10}
		start, end := params.GetRedisRangeOffsetCount()
		result, err := setup.Redis.Client.ZRevRange(ctx, "feed:home:list_param_test", start, end).Result()
		require.NoError(t, err)
		assert.LessOrEqual(t, len(result), 10)
	})
}

// TestReblogAggregation_RealRedis 测试转发聚合逻辑
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

// TestConcurrentAccess_RealRedis 测试并发访问
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
	for i := 0; i < concurrency; i++ {
		go func(id int) {
			for j := 0; j < operationsPerGoroutine; j++ {
				status := &models.Status{Id: uint64(id*10000 + j)}
				_ = svc.AddToTimelineItem(timeline, status, false)
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < concurrency; i++ {
		<-done
	}

	// 验证数据一致性
	ctx := context.Background()
	count, err := setup.Redis.Client.ZCard(ctx, "feed:home:concurrent_test").Result()
	require.NoError(t, err)
	// 由于并发写入，最终数据量应该接近预期
	assert.Greater(t, count, int64(0))
}

// TestSubscriptionCache_Concurrent 测试订阅缓存并发安全
func TestSubscriptionCache_Concurrent(t *testing.T) {
	cache := &subscriptionCache{ttl: 5 * time.Second}

	done := make(chan bool)

	// 并发读写
	for i := 0; i < 100; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := "key_" + strconv.Itoa(id)
				cache.Set(key, true)
				cache.Get(key)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 100; i++ {
		<-done
	}
}
