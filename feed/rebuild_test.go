package feed

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTimelineSnapshotPreservesReblogFalloff(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprint(aggregate), func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			defer client.Close()
			service := NewFeedService(NewRedisClient(client))
			old := &TestMockTimeline{KeyFormat: "old:%s", TimelineId: "1"}
			next := &TestMockTimeline{KeyFormat: "new:%s", TimelineId: "1"}
			statuses := models.Statuses{}
			for id := uint64(600); id > 0; id-- {
				status := &models.Status{Id: id}
				if id%5 == 0 {
					original := uint64(10 + (id % 7))
					status.ReblogId = &original
				}
				statuses = append(statuses, status)
			}
			for _, status := range statuses {
				require.NoError(t, service.AddToTimelineItem(old, status, aggregate))
				require.NoError(t, service.TrimTimeline(old))
			}
			counter := &pipelineCounter{}
			client.AddHook(counter)
			require.NoError(t, service.RebuildTimeline(next, statuses, aggregate))
			require.Equal(t, int64(1), counter.calls.Load(), "a rebuild uses one Redis pipeline round trip")
			ctx := context.Background()
			for _, suffix := range []string{"", ":reblogs"} {
				want, err := client.ZRevRangeWithScores(ctx, "old:1"+suffix, 0, -1).Result()
				require.NoError(t, err)
				got, err := client.ZRevRangeWithScores(ctx, "new:1"+suffix, 0, -1).Result()
				require.NoError(t, err)
				require.Equal(t, want, got)
			}
			for origin := 10; origin < 17; origin++ {
				want, err := client.SMembers(ctx, fmt.Sprintf("old:1:reblogs:%d", origin)).Result()
				require.NoError(t, err)
				got, err := client.SMembers(ctx, fmt.Sprintf("new:1:reblogs:%d", origin)).Result()
				require.NoError(t, err)
				require.ElementsMatch(t, want, got)
			}
		})
	}
}

func TestColdRebuildCoalescesConcurrentMisses(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	work := func() error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}
	wg.Add(1)
	go func() { defer wg.Done(); require.NoError(t, coalesceRebuild("test:coalesce", work)) }()
	<-started
	for range 15 {
		wg.Add(1)
		go func() { defer wg.Done(); require.NoError(t, coalesceRebuild("test:coalesce", work)) }()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
}

func TestTimelineSnapshotPreservesConcurrentPush(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "live:%s", TimelineId: "1"}
	snapshot := models.Statuses{&models.Status{Id: 100}, &models.Status{Id: 90}}
	// The SQL snapshot was read before this post was published.
	require.NoError(t, service.AddToTimelineItem(timeline, &models.Status{Id: 101}, false))
	var wg sync.WaitGroup
	for id := uint64(102); id < 118; id++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			require.NoError(t, service.AddToTimelineItem(timeline, &models.Status{Id: id}, false))
		}(id)
	}
	require.NoError(t, service.RebuildTimeline(timeline, snapshot, true))
	wg.Wait()
	members, err := client.ZRange(context.Background(), "live:1", 0, -1).Result()
	require.NoError(t, err)
	for id := uint64(101); id < 118; id++ {
		require.Contains(t, members, strconv.FormatUint(id, 10))
	}
	require.Contains(t, members, "100")
	require.Contains(t, members, "90")
}
func TestTimelineSnapshotKeepsDecimalMemberIDs(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "decimal:%s", TimelineId: "1"}
	require.NoError(t, service.RebuildTimeline(timeline, models.Statuses{&models.Status{Id: 117375256454983075}}, false))
	members, err := client.ZRange(context.Background(), "decimal:1", 0, -1).Result()
	require.NoError(t, err)
	require.Equal(t, []string{"117375256454983075"}, members)
}
func TestColdRebuildWaitingRequestCanCancel(t *testing.T) {
	start := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = coalesceRebuild("cancel:waiting", func() error { close(start); <-release; return nil })
	}()
	<-start
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := coalesceRebuildContext(ctx, "cancel:waiting", func() error { t.Error("canceled waiter must not rebuild"); return nil })
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	<-done
	rebuildMu.Lock()
	_, exists := rebuildCalls["cancel:waiting"]
	rebuildMu.Unlock()
	require.False(t, exists)
}

type pipelineCounter struct{ calls atomic.Int64 }

func (h *pipelineCounter) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *pipelineCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *pipelineCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { h.calls.Add(1); return next(ctx, cmds) }
}
func TestPageAggregationAcrossCacheAndDatabase(t *testing.T) {
	original := uint64(90)
	for _, enabled := range []bool{true, false} {
		page := newPageAggregation(enabled)
		require.True(t, page.accept(&models.Status{Id: 100, ReblogId: &original}))
		require.Equal(t, !enabled, page.accept(&models.Status{Id: 90}))
	}
	page := newPageAggregation(true)
	require.True(t, page.accept(&models.Status{Id: 100, ReblogId: &original}))
	for id := uint64(200); id < 240; id++ {
		require.True(t, page.accept(&models.Status{Id: id}))
	}
	require.True(t, page.accept(&models.Status{Id: 90}), "original returns after falloff")
}

func TestTimelineCanceledBeforeAnyStorageAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := GetStatusTimelineContent(&TestMockTimeline{}, &ListParam{Context: ctx}, func(tx *gorm.DB) (*gorm.DB, error) { t.Fatal("canceled request queried database"); return tx, nil }, nil)
	require.ErrorIs(t, err, context.Canceled)
}
