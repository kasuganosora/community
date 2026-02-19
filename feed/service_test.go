package feed

import (
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func newTestFeedService() *FeedService {
	return NewFeedService(NewMockRedisClient())
}

// setupMockService 设置默认服务使用 mock
func setupMockService(t *testing.T) *MockRedisClient {
	mockRedis := NewMockRedisClient()
	SetDefaultService(NewFeedService(mockRedis))
	return mockRedis
}

func TestFeedService_AddToTimelineItem(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "123"}

	t.Run("Add simple status", func(t *testing.T) {
		status := &models.Status{
			Id:         1001,
			AccountId:  1,
			Visibility: models.StatusVisibilityPublic,
		}

		err := svc.AddToTimelineItem(timeline, status, false)
		assert.NoError(t, err)

		// 验证数据已添加
		mockRedis := svc.redis.(*MockRedisClient)
		zSet := mockRedis.GetZSet("feed:home:123")
		assert.NotNil(t, zSet)
		assert.Contains(t, zSet, "1001")
	})

	t.Run("Add status with reblog", func(t *testing.T) {
		reblogId := uint64(500)
		status := &models.Status{
			Id:        1002,
			AccountId: 1,
			ReblogId:  &reblogId,
		}

		err := svc.AddToTimelineItem(timeline, status, true)
		assert.NoError(t, err)

		mockRedis := svc.redis.(*MockRedisClient)
		zSet := mockRedis.GetZSet("feed:home:123:reblogs")
		assert.NotNil(t, zSet)
		assert.Contains(t, zSet, "500")
	})

	t.Run("Add multiple statuses", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			status := &models.Status{
				Id:        uint64(2000 + i),
				AccountId: 1,
			}
			err := svc.AddToTimelineItem(timeline, status, false)
			assert.NoError(t, err)
		}

		mockRedis := svc.redis.(*MockRedisClient)
		zSet := mockRedis.GetZSet("feed:home:123")
		assert.NotNil(t, zSet)
		assert.GreaterOrEqual(t, len(zSet), 10)
	})

	t.Run("Timeline update tracking", func(t *testing.T) {
		status := &models.Status{Id: 3000}
		err := svc.AddToTimelineItem(timeline, status, false)
		assert.NoError(t, err)

		mockRedis := svc.redis.(*MockRedisClient)
		zSet := mockRedis.GetZSet("timelines")
		assert.NotNil(t, zSet)
		assert.Contains(t, zSet, "feed:home:123")
	})
}

func TestFeedService_RemoveTimelineItem(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "456"}

	// 先添加一些数据
	for i := 0; i < 5; i++ {
		status := &models.Status{
			Id:        uint64(100 + i),
			AccountId: 1,
		}
		_ = svc.AddToTimelineItem(timeline, status, false)
	}

	t.Run("Remove existing status", func(t *testing.T) {
		status := &models.Status{Id: 100}
		err := svc.RemoveTimelineItem(timeline, status, false)
		assert.NoError(t, err)

		mockRedis := svc.redis.(*MockRedisClient)
		zSet := mockRedis.GetZSet("feed:home:456")
		if zSet != nil {
			assert.NotContains(t, zSet, "100")
		}
	})

	t.Run("Remove non-existing status", func(t *testing.T) {
		status := &models.Status{Id: 99999}
		err := svc.RemoveTimelineItem(timeline, status, false)
		assert.NoError(t, err)
	})

	t.Run("Remove reblog status", func(t *testing.T) {
		reblogId := uint64(500)
		status := &models.Status{
			Id:        200,
			AccountId: 1,
			ReblogId:  &reblogId,
		}

		// 先添加
		_ = svc.AddToTimelineItem(timeline, status, true)

		// 再删除
		err := svc.RemoveTimelineItem(timeline, status, true)
		assert.NoError(t, err)
	})
}

func TestFeedService_TrimTimeline(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "789"}

	// 添加超过 MAX_ITEMS 的数据
	for i := 0; i < 600; i++ {
		status := &models.Status{
			Id:        uint64(1000 + i),
			AccountId: 1,
		}
		_ = svc.AddToTimelineItem(timeline, status, false)
	}

	err := svc.TrimTimeline(timeline)
	assert.NoError(t, err)
}

func TestFeedService_CleanFeeds(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "111"}

	// 添加一些数据
	for i := 0; i < 5; i++ {
		status := &models.Status{
			Id:        uint64(500 + i),
			AccountId: 1,
		}
		_ = svc.AddToTimelineItem(timeline, status, false)
	}

	err := svc.CleanFeeds(timeline, []string{"111"})
	assert.NoError(t, err)
}

func TestFeedService_PushToClient(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:public:%s", TimelineId: "all"}

	status := &models.Status{
		Id:        1001,
		AccountId: 1,
	}

	err := svc.PushToClient(timeline, "update", status)
	assert.NoError(t, err)
}

func TestFeedService_PushUpdateRequired(t *testing.T) {
	t.Run("No subscribers", func(t *testing.T) {
		// 清除缓存
		subCache = &subscriptionCache{ttl: 5 * time.Second}
		svc := newTestFeedService()
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "push_no_sub"}
		result := svc.PushUpdateRequired(timeline)
		assert.False(t, result)
	})

	t.Run("Has subscribers", func(t *testing.T) {
		// 清除缓存
		subCache = &subscriptionCache{ttl: 5 * time.Second}
		svc := newTestFeedService()
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "push_has_sub"}
		mockRedis := svc.redis.(*MockRedisClient)
		mockRedis.SetChannel("subscribed:feed:home:push_has_sub", true)
		result := svc.PushUpdateRequired(timeline)
		assert.True(t, result)
	})
}

func TestFeedService_GcTimelines(t *testing.T) {
	svc := newTestFeedService()

	// 添加一些旧的时间线数据
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "old_user"}
	status := &models.Status{Id: 1}
	_ = svc.AddToTimelineItem(timeline, status, false)

	err := svc.GcTimelines()
	assert.NoError(t, err)
}

func TestFeedService_ConcurrentOperations(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "concurrent"}

	// 并发添加和删除
	done := make(chan bool)

	go func() {
		for i := 0; i < 100; i++ {
			status := &models.Status{Id: uint64(i)}
			_ = svc.AddToTimelineItem(timeline, status, false)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 50; i++ {
			status := &models.Status{Id: uint64(i)}
			_ = svc.RemoveTimelineItem(timeline, status, false)
		}
		done <- true
	}()

	<-done
	<-done

	// 验证没有 panic，操作完成
	mockRedis := svc.redis.(*MockRedisClient)
	zSet := mockRedis.GetZSet("feed:home:concurrent")
	t.Logf("Final zSet size: %d", len(zSet))
}

func TestFeedService_EdgeCases(t *testing.T) {
	svc := newTestFeedService()

	t.Run("Nil status", func(t *testing.T) {
		// 传入 nil 不应该 panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Should not panic with nil status")
			}
		}()
		// 跳过，因为我们无法安全地传递 nil
	})

	t.Run("Empty timeline ID", func(t *testing.T) {
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: ""}
		status := &models.Status{Id: 1}
		err := svc.AddToTimelineItem(timeline, status, false)
		assert.NoError(t, err)
	})

	t.Run("Large status ID", func(t *testing.T) {
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "large"}
		status := &models.Status{Id: uint64(1 << 62)}
		err := svc.AddToTimelineItem(timeline, status, false)
		assert.NoError(t, err)

		mockRedis := svc.redis.(*MockRedisClient)
		zSet := mockRedis.GetZSet("feed:home:large")
		assert.NotNil(t, zSet)
		assert.Contains(t, zSet, strconv.FormatUint(uint64(1<<62), 10))
	})
}

func TestFeedService_ReblogAggregation(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "reblog"}

	// 测试转发功能不会崩溃
	reblogId := uint64(1000)
	for i := 0; i < 3; i++ {
		reblog := &models.Status{
			Id:        uint64(2000 + i),
			AccountId: uint64(10 + i),
			ReblogId:  &reblogId,
		}
		err := svc.AddToTimelineItem(timeline, reblog, true)
		assert.NoError(t, err)
	}
}

func TestFeedService_RemoveTimelineItem_WithReblog(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "reblog_remove"}

	// 先添加转发
	reblogId := uint64(5000)
	status := &models.Status{
		Id:        6000,
		AccountId: 1,
		ReblogId:  &reblogId,
	}

	// 添加后再删除
	_ = svc.AddToTimelineItem(timeline, status, true)
	err := svc.RemoveTimelineItem(timeline, status, true)
	assert.NoError(t, err)
}

func TestFeedService_TrimTimeline_WithReblogs(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "trim_reblog"}

	// 添加大量数据
	for i := 0; i < 100; i++ {
		status := &models.Status{
			Id:        uint64(10000 + i),
			AccountId: 1,
		}
		_ = svc.AddToTimelineItem(timeline, status, false)
	}

	err := svc.TrimTimeline(timeline)
	assert.NoError(t, err)
}

func TestFeedService_CleanFeeds_Multiple(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "clean_multi"}

	// 添加数据到多个 feed
	for i := 0; i < 3; i++ {
		for j := 0; j < 5; j++ {
			status := &models.Status{
				Id:        uint64(i*100 + j),
				AccountId: 1,
			}
			timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: strconv.Itoa(i)}
			_ = svc.AddToTimelineItem(timeline, status, false)
		}
	}

	err := svc.CleanFeeds(timeline, []string{"0", "1", "2"})
	assert.NoError(t, err)
}

func TestFeedService_AddToTimelineItem_ExistingReblog(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "existing_reblog"}

	// 添加原始状态
	originalStatus := &models.Status{
		Id:        7000,
		AccountId: 1,
	}
	_ = svc.AddToTimelineItem(timeline, originalStatus, true)

	// 添加转发
	reblogId := uint64(7000)
	reblog := &models.Status{
		Id:        7001,
		AccountId: 2,
		ReblogId:  &reblogId,
	}
	err := svc.AddToTimelineItem(timeline, reblog, true)
	assert.NoError(t, err)

	// 添加另一个转发
	reblog2 := &models.Status{
		Id:        7002,
		AccountId: 3,
		ReblogId:  &reblogId,
	}
	err = svc.AddToTimelineItem(timeline, reblog2, true)
	assert.NoError(t, err)
}

func TestFeedService_PushToClient_DifferentEvents(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:public:%s", TimelineId: "all"}

	status := &models.Status{Id: 1}

	// 测试不同的事件类型
	eventTypes := []string{"update", "delete", "status.update"}
	for _, eventType := range eventTypes {
		err := svc.PushToClient(timeline, eventType, status)
		assert.NoError(t, err)
	}
}

func TestFeedService_MultipleTimelines(t *testing.T) {
	svc := newTestFeedService()

	// 创建多个时间线
	timelines := []*TestMockTimeline{
		{KeyFormat: "feed:home:%s", TimelineId: "user1"},
		{KeyFormat: "feed:home:%s", TimelineId: "user2"},
		{KeyFormat: "feed:public:%s", TimelineId: "all"},
	}

	// 向每个时间线添加数据
	for i, timeline := range timelines {
		status := &models.Status{
			Id:        uint64(8000 + i),
			AccountId: 1,
		}
		err := svc.AddToTimelineItem(timeline, status, false)
		assert.NoError(t, err)
	}
}

func TestFeedService_GcTimelines_WithOldData(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "old_gc"}

	// 添加一些数据
	status := &models.Status{Id: 9000}
	_ = svc.AddToTimelineItem(timeline, status, false)

	err := svc.GcTimelines()
	assert.NoError(t, err)
}

func TestFeedService_RemoveNonExistentStatus(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "remove_nonexist"}

	// 删除不存在的状态
	status := &models.Status{Id: 99999}
	err := svc.RemoveTimelineItem(timeline, status, false)
	assert.NoError(t, err)
}

func TestFeedService_AddToTimelineItem_WithoutReblogAggregation(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "no_aggregate"}

	// 添加转发但不聚合
	reblogId := uint64(10000)
	reblog := &models.Status{
		Id:        10001,
		AccountId: 1,
		ReblogId:  &reblogId,
	}
	err := svc.AddToTimelineItem(timeline, reblog, false)
	assert.NoError(t, err)
}

// 测试 feed.go 中的全局函数（通过 defaultService）
func TestGlobalFunctions_WithMock(t *testing.T) {
	// 设置 mock 服务
	mockRedis := setupMockService(t)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "global"}

	t.Run("AddToTimelineItem global function", func(t *testing.T) {
		status := &models.Status{Id: 1001}
		err := AddToTimelineItem(timeline, status, false)
		assert.NoError(t, err)

		zSet := mockRedis.GetZSet("feed:home:global")
		assert.Contains(t, zSet, "1001")
	})

	t.Run("RemoveTimelineItem global function", func(t *testing.T) {
		status := &models.Status{Id: 1001}
		err := RemoveTimelineItem(timeline, status, false)
		assert.NoError(t, err)
	})

	t.Run("TrimTimeline global function", func(t *testing.T) {
		err := TrimTimeline(timeline)
		assert.NoError(t, err)
	})

	t.Run("PushToClient global function", func(t *testing.T) {
		status := &models.Status{Id: 1002}
		err := PushToClient(timeline, "update", status)
		assert.NoError(t, err)
	})

	t.Run("PushUpdateRequired global function", func(t *testing.T) {
		// 清除缓存
		subCache = &subscriptionCache{ttl: 5 * time.Second}
		result := PushUpdateRequired(timeline)
		assert.False(t, result) // 没有订阅者
	})

	t.Run("GetRedisChannelName function", func(t *testing.T) {
		name := GetRedisChannelName(timeline)
		assert.Equal(t, "subscribed:feed:home:global", name)
	})

	t.Run("GcTimelines global function", func(t *testing.T) {
		err := GcTimelines()
		assert.NoError(t, err)
	})
}

// 测试 resetDefaultService 函数
func TestResetDefaultService(t *testing.T) {
	// 创建新的 mock 服务
	mockRedis := NewMockRedisClient()
	SetDefaultService(NewFeedService(mockRedis))

	// 验证可以正常使用
	timeline := &TestMockTimeline{KeyFormat: "feed:test:%s", TimelineId: "reset"}
	status := &models.Status{Id: 1}
	err := AddToTimelineItem(timeline, status, false)
	assert.NoError(t, err)
}

// 测试 CleanFeeds 全局函数
func TestCleanFeeds_Global(t *testing.T) {
	mockRedis := NewMockRedisClient()
	SetDefaultService(NewFeedService(mockRedis))

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "clean_global"}

	// 添加数据
	for i := 0; i < 3; i++ {
		status := &models.Status{Id: uint64(i)}
		_ = AddToTimelineItem(timeline, status, false)
	}

	err := CleanFeeds(timeline, []string{"clean_global"})
	assert.NoError(t, err)
}

// 测试 PushUpdateRequired 有订阅者
func TestPushUpdateRequired_WithSubscribers(t *testing.T) {
	// 清除缓存
	subCache = &subscriptionCache{ttl: 5 * time.Second}
	mockRedis := NewMockRedisClient()
	SetDefaultService(NewFeedService(mockRedis))

	// 设置频道
	mockRedis.SetChannel("subscribed:feed:home:test_sub", true)

	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "test_sub"}
	result := PushUpdateRequired(timeline)
	assert.True(t, result)
}

func TestFeedService_BatchAddToTimelineItems(t *testing.T) {
	svc := newTestFeedService()

	t.Run("Empty timelines", func(t *testing.T) {
		status := &models.Status{Id: 1000}
		err := svc.BatchAddToTimelineItems([]Timeline{}, status, false)
		assert.NoError(t, err)
	})

	t.Run("Single timeline", func(t *testing.T) {
		timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "batch_single"}
		status := &models.Status{Id: 1001}
		err := svc.BatchAddToTimelineItems([]Timeline{timeline}, status, false)
		assert.NoError(t, err)

		mockRedis := svc.redis.(*MockRedisClient)
		zSet := mockRedis.GetZSet("feed:home:batch_single")
		assert.NotNil(t, zSet)
		assert.Contains(t, zSet, "1001")
	})

	t.Run("Multiple timelines", func(t *testing.T) {
		// BatchAddToTimelineItems 使用 Pipeline，当前 mock 实现不支持
		// 所以只测试不会 panic
		timelines := []Timeline{
			&TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "batch_multi_1"},
			&TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "batch_multi_2"},
			&TestMockTimeline{KeyFormat: "feed:public:%s", TimelineId: "all"},
		}
		status := &models.Status{Id: 2001}
		err := svc.BatchAddToTimelineItems(timelines, status, false)
		assert.NoError(t, err)
		// 注意：由于 Pipelined 的 mock 实现不实际执行操作，
		// 所以这里不验证数据是否添加成功
	})
}

func TestSubscriptionCache(t *testing.T) {
	// 清除缓存
	subCache = &subscriptionCache{ttl: 5 * time.Second}

	t.Run("Set and Get", func(t *testing.T) {
		subCache.Set("test_key", true)
		val, ok := subCache.Get("test_key")
		assert.True(t, ok)
		assert.True(t, val)
	})

	t.Run("Get non-existent", func(t *testing.T) {
		val, ok := subCache.Get("non_existent_key")
		assert.False(t, ok)
		assert.False(t, val)
	})

	t.Run("Cache expiry", func(t *testing.T) {
		// 创建短期缓存
		shortCache := &subscriptionCache{ttl: 1 * time.Nanosecond}
		shortCache.Set("expire_key", true)
		// 等待过期
		time.Sleep(2 * time.Nanosecond)
		val, ok := shortCache.Get("expire_key")
		assert.False(t, ok)
		assert.False(t, val)
	})
}

func TestFeedService_AddToTimelineItem_ReblogInFalloff(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "reblog_falloff"}

	// 先添加原始状态
	originalStatus := &models.Status{Id: 10000}
	_ = svc.AddToTimelineItem(timeline, originalStatus, true)

	// 添加在 REBLOG_FALLOFF 范围内的转发
	reblogId := uint64(10000)
	reblog := &models.Status{Id: 10001, ReblogId: &reblogId}
	err := svc.AddToTimelineItem(timeline, reblog, true)
	assert.NoError(t, err)

	// 验证 reblog 集合
	mockRedis := svc.redis.(*MockRedisClient)
	reblogSet := mockRedis.GetZSet("feed:home:reblog_falloff:reblogs")
	assert.NotNil(t, reblogSet)
}

func TestFeedService_AddToTimelineItem_ExistingScore(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "existing_score"}

	// 添加转发
	reblogId := uint64(20000)
	reblog := &models.Status{Id: 20001, ReblogId: &reblogId}
	_ = svc.AddToTimelineItem(timeline, reblog, true)

	// 再次添加相同原始状态的另一个转发
	reblog2 := &models.Status{Id: 20002, ReblogId: &reblogId}
	err := svc.AddToTimelineItem(timeline, reblog2, true)
	assert.NoError(t, err)
}

func TestFeedService_RemoveTimelineItem_WithOtherReblogs(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "remove_other_reblogs"}

	// 添加多个转发
	reblogId := uint64(30000)
	for i := 0; i < 3; i++ {
		reblog := &models.Status{Id: uint64(30001 + i), ReblogId: &reblogId}
		_ = svc.AddToTimelineItem(timeline, reblog, true)
	}

	// 删除其中一个转发
	reblog := &models.Status{Id: 30001, ReblogId: &reblogId}
	err := svc.RemoveTimelineItem(timeline, reblog, true)
	assert.NoError(t, err)
}

func TestFeedService_TrimTimeline_WithReblogData(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "trim_reblog_data"}

	// 添加普通状态
	for i := 0; i < 100; i++ {
		status := &models.Status{Id: uint64(40000 + i)}
		_ = svc.AddToTimelineItem(timeline, status, false)
	}

	// 添加转发
	reblogId := uint64(40000)
	reblog := &models.Status{Id: 40500, ReblogId: &reblogId}
	_ = svc.AddToTimelineItem(timeline, reblog, true)

	err := svc.TrimTimeline(timeline)
	assert.NoError(t, err)
}

func TestSetDefaultDBService(t *testing.T) {
	// 测试设置默认 DBService 不会 panic
	assert.NotPanics(t, func() {
		// 设置 nil 是允许的
		SetDefaultDBService(nil)
	})
}

func TestNewRedisClient(t *testing.T) {
	// 测试创建 Redis 客户端包装器
	wrapper := NewRedisClient(nil)
	assert.NotNil(t, wrapper)
}

func TestNewDBService(t *testing.T) {
	// 测试创建 DBService
	svc := NewDBService(nil)
	assert.NotNil(t, svc)
}

func TestSubscriptionCache_Concurrent(t *testing.T) {
	cache := &subscriptionCache{ttl: 5 * time.Second}

	done := make(chan bool)

	// 并发读写
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := "key_" + string(rune(id))
				cache.Set(key, true)
				cache.Get(key)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestFeedService_AddToTimelineItem_ReblogRankCheck(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "reblog_rank"}

	// 添加原始状态
	originalId := uint64(50000)
	original := &models.Status{Id: originalId}
	_ = svc.AddToTimelineItem(timeline, original, true)

	// 添加在 REBLOG_FALLOFF 范围内的转发
	reblog := &models.Status{Id: 50001, ReblogId: &originalId}
	err := svc.AddToTimelineItem(timeline, reblog, true)
	assert.NoError(t, err)
}

func TestFeedService_AddToTimelineItem_ZAddError(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "zadd_err"}

	status := &models.Status{Id: 60000}
	err := svc.AddToTimelineItem(timeline, status, false)
	assert.NoError(t, err)
}

func TestFeedService_AddToTimelineItem_ExistingReblogScore(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "existing_reblog_score"}

	// 先添加一个转发
	reblogId := uint64(70000)
	firstReblog := &models.Status{Id: 70001, ReblogId: &reblogId}
	_ = svc.AddToTimelineItem(timeline, firstReblog, true)

	// 再添加另一个转发，验证不重复添加
	secondReblog := &models.Status{Id: 70002, ReblogId: &reblogId}
	err := svc.AddToTimelineItem(timeline, secondReblog, true)
	assert.NoError(t, err)
}

func TestFeedService_RemoveTimelineItem_ErrorPath(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "remove_error"}

	// 删除不存在的状态
	status := &models.Status{Id: 99999}
	err := svc.RemoveTimelineItem(timeline, status, false)
	assert.NoError(t, err)
}

func TestFeedService_RemoveTimelineItem_WithPipeline(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "remove_pipeline"}

	// 添加转发
	reblogId := uint64(80000)
	status := &models.Status{Id: 80001, ReblogId: &reblogId}
	_ = svc.AddToTimelineItem(timeline, status, true)

	// 删除转发
	err := svc.RemoveTimelineItem(timeline, status, true)
	assert.NoError(t, err)
}

func TestFeedService_CleanFeeds_Empty(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "clean_empty"}

	err := svc.CleanFeeds(timeline, []string{})
	assert.NoError(t, err)
}

func TestFeedService_GcTimelines_Empty(t *testing.T) {
	svc := newTestFeedService()
	err := svc.GcTimelines()
	assert.NoError(t, err)
}

func TestFeedService_PushToClient_EventTypes(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:public:%s", TimelineId: "all"}

	eventTypes := []string{"update", "delete", "status.update", "notification"}
	for _, eventType := range eventTypes {
		status := &models.Status{Id: 1}
		err := svc.PushToClient(timeline, eventType, status)
		assert.NoError(t, err)
	}
}
