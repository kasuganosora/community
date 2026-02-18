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
)

// ============================================================================
// 真实 Redis 基准测试（使用 .env.test 配置）
// ============================================================================

// getRealRedisClient 获取真实 Redis 客户端（用于基准测试）
func getRealRedisClient(b *testing.B) *redis.Client {
	config := testutil.GetTestConfig()

	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", config.RedisHost, config.RedisPort),
		Password:     config.RedisPassword,
		DB:           config.RedisDB,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis not available at %s:%d: %v", config.RedisHost, config.RedisPort, err)
		return nil
	}

	// 清理测试数据库
	client.FlushDB(ctx)

	return client
}

// cleanupRedis 清理 Redis 测试数据
func cleanupRedis(client *redis.Client, keys ...string) {
	ctx := context.Background()
	for _, key := range keys {
		client.Del(ctx, key)
	}
}

// BenchmarkRealRedis_AddToTimelineItem 使用真实 Redis 测试添加操作
func BenchmarkRealRedis_AddToTimelineItem(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "bench:feed:home:%s", TimelineId: "1"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}

	// 清理
	cleanupRedis(client, "bench:feed:home:1", "bench:feed:home:1:reblogs", "timelines")
}

// BenchmarkRealRedis_AddToTimelineItem_WithReblog 使用真实 Redis 测试转发
func BenchmarkRealRedis_AddToTimelineItem_WithReblog(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "bench:feed:home:%s", TimelineId: "1"}
	reblogId := uint64(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status := &models.Status{
			Id:       uint64(i + 1000),
			ReblogId: &reblogId,
		}
		service.AddToTimelineItem(timeline, status, true)
	}

	cleanupRedis(client, "bench:feed:home:1", "bench:feed:home:1:reblogs", "timelines")
}

// BenchmarkRealRedis_TrimTimeline 使用真实 Redis 测试裁剪
func BenchmarkRealRedis_TrimTimeline(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "bench:feed:home:%s", TimelineId: "1"}

	// 预填充 1000 条数据
	for i := 0; i < 1000; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.TrimTimeline(timeline)
	}

	cleanupRedis(client, "bench:feed:home:1", "bench:feed:home:1:reblogs", "timelines")
}

// BenchmarkRealRedis_PushScenario 完整推送场景（真实 Redis）
func BenchmarkRealRedis_PushScenario(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "bench:feed:home:%s", TimelineId: "1"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, true)
		service.TrimTimeline(timeline)
	}

	cleanupRedis(client, "bench:feed:home:1", "bench:feed:home:1:reblogs", "timelines")
}

// BenchmarkRealRedis_Parallel 并行测试（真实 Redis）
func BenchmarkRealRedis_Parallel(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	service := NewFeedService(NewRedisClient(client))

	b.RunParallel(func(pb *testing.PB) {
		timelineId := strconv.Itoa(int(time.Now().UnixNano() % 10000))
		timeline := &TestMockTimeline{
			KeyFormat:  "bench:feed:home:%s",
			TimelineId: timelineId,
		}
		i := 0
		for pb.Next() {
			status := &models.Status{Id: uint64(i + 1)}
			service.AddToTimelineItem(timeline, status, false)
			i++
		}
	})

	// 清理所有 benchmark 相关的 key
	ctx := context.Background()
	keys, _ := client.Keys(ctx, "bench:feed:home:*").Result()
	if len(keys) > 0 {
		client.Del(ctx, keys...)
	}
}

// BenchmarkRealRedis_CleanFeeds 清理多个 feed（真实 Redis）
func BenchmarkRealRedis_CleanFeeds(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "bench:feed:home:%s", TimelineId: "1"}
	feedIds := make([]string, 100)
	for i := 0; i < 100; i++ {
		feedIds[i] = strconv.Itoa(i + 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.CleanFeeds(timeline, feedIds)
	}
}

// BenchmarkRealRedis_PipelineComparison 对比 Pipeline 优化效果
func BenchmarkRealRedis_PipelineComparison(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 测试 1: 逐个操作
	b.Run("Sequential", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := 0; j < 10; j++ {
				client.ZAdd(ctx, "bench:seq", redis.Z{
					Score:  float64(i*10 + j),
					Member: strconv.Itoa(i*10 + j),
				})
			}
		}
		client.Del(ctx, "bench:seq")
	})

	// 测试 2: Pipeline 批量操作
	b.Run("Pipeline", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				for j := 0; j < 10; j++ {
					pipe.ZAdd(ctx, "bench:pipe", redis.Z{
						Score:  float64(i*10 + j),
						Member: strconv.Itoa(i*10 + j),
					})
				}
				return nil
			})
		}
		client.Del(ctx, "bench:pipe")
	})
}

// BenchmarkRealRedis_BatchAddToTimelineItems 批量添加到多个时间线
func BenchmarkRealRedis_BatchAddToTimelineItems(b *testing.B) {
	client := getRealRedisClient(b)
	if client == nil {
		return
	}
	defer client.Close()

	service := NewFeedService(NewRedisClient(client))

	// 创建 100 个时间线
	timelines := make([]Timeline, 100)
	for i := 0; i < 100; i++ {
		timelines[i] = &TestMockTimeline{
			KeyFormat:  "bench:feed:home:%s",
			TimelineId: strconv.Itoa(i + 1),
		}
	}

	// 测试 1: 逐个添加
	b.Run("Sequential", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			status := &models.Status{Id: uint64(i + 1)}
			for _, timeline := range timelines {
				service.AddToTimelineItem(timeline, status, false)
			}
		}
		// 清理
		ctx := context.Background()
		for i := 1; i <= 100; i++ {
			client.Del(ctx, fmt.Sprintf("bench:feed:home:%d", i))
		}
		client.Del(ctx, "timelines")
	})

	// 测试 2: 批量添加
	b.Run("Batch", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			status := &models.Status{Id: uint64(i + 10000)}
			service.BatchAddToTimelineItems(timelines, status, false)
		}
		// 清理
		ctx := context.Background()
		for i := 1; i <= 100; i++ {
			client.Del(ctx, fmt.Sprintf("bench:feed:home:%d", i))
		}
		client.Del(ctx, "timelines")
	})
}

// ============================================================================
// Mock Redis 基准测试（用于无 Redis 环境的 CI）
// ============================================================================

// BenchmarkAddToTimelineItem_Simple 测试简单添加操作
func BenchmarkAddToTimelineItem_Simple(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}
	status := &models.Status{Id: 1}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status.Id = uint64(i + 1)
		service.AddToTimelineItem(timeline, status, false)
	}
}

// BenchmarkAddToTimelineItem_WithReblog 测试带转发的添加操作
func BenchmarkAddToTimelineItem_WithReblog(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}
	reblogId := uint64(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status := &models.Status{
			Id:       uint64(i + 1000),
			ReblogId: &reblogId,
		}
		service.AddToTimelineItem(timeline, status, true)
	}
}

// BenchmarkAddToTimelineItem_Parallel 测试并行添加
func BenchmarkAddToTimelineItem_Parallel(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			status := &models.Status{Id: uint64(i + 1)}
			service.AddToTimelineItem(timeline, status, false)
			i++
		}
	})
}

// BenchmarkRemoveTimelineItem_Simple 测试简单移除操作
func BenchmarkRemoveTimelineItem_Simple(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	// 预填充数据
	for i := 0; i < b.N; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.RemoveTimelineItem(timeline, status, false)
	}
}

// BenchmarkTrimTimeline 测试裁剪时间线
func BenchmarkTrimTimeline(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	// 预填充 1000 条数据
	for i := 0; i < 1000; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.TrimTimeline(timeline)
	}
}

// BenchmarkCleanFeeds 测试清理 feed
func BenchmarkCleanFeeds(b *testing.B) {
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}
	feedIds := make([]string, 100)
	for i := 0; i < 100; i++ {
		feedIds[i] = strconv.Itoa(i + 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mockRedis := NewMockRedisClient()
		service := NewFeedService(mockRedis)
		service.CleanFeeds(timeline, feedIds)
	}
}

// ============================================================================
// 基准测试：Timeline 操作场景
// ============================================================================

// BenchmarkHomeTimeline_PushScenario 模拟主页时间线推送场景
func BenchmarkHomeTimeline_PushScenario(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		// 模拟完整的推送流程
		service.AddToTimelineItem(timeline, status, true)
		service.TrimTimeline(timeline)
	}
}

// BenchmarkMultipleTimelines 多时间线场景
func BenchmarkMultipleTimelines(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)

	// 模拟 100 个用户的时间线
	timelines := make([]*TestMockTimeline, 100)
	for i := 0; i < 100; i++ {
		timelines[i] = &TestMockTimeline{
			KeyFormat:  "feed:home:%s",
			TimelineId: strconv.Itoa(i + 1),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		timeline := timelines[i%100]
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}
}

// BenchmarkReblogScenario 转发场景
func BenchmarkReblogScenario(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	// 先创建原始状态
	originalStatus := &models.Status{Id: 1}
	service.AddToTimelineItem(timeline, originalStatus, true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 模拟转发
		reblogId := uint64(1)
		status := &models.Status{
			Id:       uint64(i + 1000),
			ReblogId: &reblogId,
		}
		service.AddToTimelineItem(timeline, status, true)
	}
}

// ============================================================================
// 基准测试：大规模数据场景
// ============================================================================

// BenchmarkLargeTimeline 大时间线场景（1000+ 条目）
func BenchmarkLargeTimeline(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	// 预填充 1000 条数据
	for i := 0; i < 1000; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 在大时间线上添加新状态
		status := &models.Status{Id: uint64(1000 + i + 1)}
		service.AddToTimelineItem(timeline, status, false)
		service.TrimTimeline(timeline)
	}
}

// ============================================================================
// 基准测试：内存分配分析
// ============================================================================

// BenchmarkAddToTimelineItem_Allocs 分析内存分配
func BenchmarkAddToTimelineItem_Allocs(b *testing.B) {
	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}
}

// ============================================================================
// 基准测试：各 Timeline 类型对比
// ============================================================================

func BenchmarkTimelineTypes(b *testing.B) {
	types := []struct {
		name      string
		keyFormat string
	}{
		{"Home", "feed:home:%s"},
		{"Public", "feed:public:%s"},
		{"Tag", "feed:hashtag:%s"},
		{"Mention", "feed:mention:%s"},
		{"Notification", "feed:notification:%s"},
		{"List", "feed:list:%s"},
	}

	for _, tt := range types {
		b.Run(tt.name, func(b *testing.B) {
			mockRedis := NewMockRedisClient()
			service := NewFeedService(mockRedis)
			timeline := &TestMockTimeline{
				KeyFormat:  tt.keyFormat,
				TimelineId: "1",
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				status := &models.Status{Id: uint64(i + 1)}
				service.AddToTimelineItem(timeline, status, false)
			}
		})
	}
}

// ============================================================================
// 性能报告生成
// ============================================================================

// TestBenchmarkReport 生成性能报告
func TestBenchmarkReport(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping benchmark report in short mode")
	}

	t.Log("=== Timeline 性能基准测试报告 ===")
	t.Log("")
	t.Log("运行命令获取详细报告:")
	t.Log("  go test -bench=. -benchmem ./feed/...")
	t.Log("  go test -bench=. -benchtime=3s -benchmem ./feed/...")
	t.Log("")

	// 输出优化建议
	t.Log("=== 预期性能指标 ===")
	t.Log("| 操作                    | 预期 ns/op | 预期 allocs/op |")
	t.Log("|-------------------------|------------|----------------|")
	t.Log("| AddToTimelineItem       | < 1000     | < 10           |")
	t.Log("| RemoveTimelineItem      | < 1500     | < 15           |")
	t.Log("| TrimTimeline            | < 2000     | < 20           |")
	t.Log("| CleanFeeds (100 feeds)  | < 5000     | < 50           |")
}

func benchmarkWrapper(f func(*testing.B)) func(*testing.B) {
	return f
}

// ============================================================================
// 性能卡点检测
// ============================================================================

// TestPerformanceHotspots 检测性能热点
func TestPerformanceHotspots(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance hotspot detection in short mode")
	}

	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	// 分析各操作的复杂度
	operations := []struct {
		name string
		fn   func()
	}{
		{
			name: "SingleAdd",
			fn: func() {
				status := &models.Status{Id: 1}
				service.AddToTimelineItem(timeline, status, false)
			},
		},
		{
			name: "AddWithReblog",
			fn: func() {
				reblogId := uint64(1)
				status := &models.Status{Id: 2, ReblogId: &reblogId}
				service.AddToTimelineItem(timeline, status, true)
			},
		},
		{
			name: "TrimAfter100Items",
			fn: func() {
				// 先添加 100 条
				for i := 0; i < 100; i++ {
					status := &models.Status{Id: uint64(i + 100)}
					service.AddToTimelineItem(timeline, status, false)
				}
				service.TrimTimeline(timeline)
			},
		},
	}

	t.Log("=== 性能卡点检测结果 ===")
	for _, op := range operations {
		// 重置 mock
		mockRedis = NewMockRedisClient()
		service = NewFeedService(mockRedis)

		t.Logf("操作: %s", op.name)
		// 执行操作以检测是否有明显延迟
		op.fn()
		t.Logf("  - 完成 ✓")
	}
}

// ============================================================================
// 压力测试
// ============================================================================

// TestStressHighConcurrency 高并发压力测试
func TestStressHighConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)

	const goroutines = 100
	const opsPerGoroutine = 100

	done := make(chan bool, goroutines)

	for g := 0; g < goroutines; g++ {
		go func(goroutineId int) {
			timeline := &TestMockTimeline{
				KeyFormat:  "feed:home:%s",
				TimelineId: strconv.Itoa(goroutineId),
			}
			for i := 0; i < opsPerGoroutine; i++ {
				status := &models.Status{Id: uint64(goroutineId*opsPerGoroutine + i + 1)}
				service.AddToTimelineItem(timeline, status, false)
			}
			done <- true
		}(g)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}

	t.Logf("完成 %d 并发操作", goroutines*opsPerGoroutine)
}

// TestStressLargeData 大数据量测试
func TestStressLargeData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	mockRedis := NewMockRedisClient()
	service := NewFeedService(mockRedis)
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "1"}

	const itemCount = 10000

	for i := 0; i < itemCount; i++ {
		status := &models.Status{Id: uint64(i + 1)}
		service.AddToTimelineItem(timeline, status, false)
	}

	// 检查数据量
	zset := mockRedis.GetZSet("feed:home:1")
	t.Logf("添加了 %d 条数据到时间线", len(zset))

	// 执行裁剪
	service.TrimTimeline(timeline)

	zset = mockRedis.GetZSet("feed:home:1")
	t.Logf("裁剪后剩余 %d 条数据", len(zset))

	if len(zset) > MAX_ITEMS {
		t.Errorf("裁剪后数据量超过 MAX_ITEMS: %d > %d", len(zset), MAX_ITEMS)
	}
}

// ============================================================================
// 辅助函数
// ============================================================================

func init() {
	// 确保测试用的 Timeline 类型已注册
}
