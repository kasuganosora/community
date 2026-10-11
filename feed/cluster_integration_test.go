package feed

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Requires a disposable three-primary/three-replica cluster. Never run against
// application Redis: FEED_CLUSTER_TEST_FAILOVER=1 intentionally stops a primary.
func TestFeedRealClusterProtocol(t *testing.T) {
	addresses := os.Getenv("FEED_CLUSTER_TEST_ADDRS")
	if addresses == "" {
		t.Skip("set FEED_CLUSTER_TEST_ADDRS to an isolated real Redis Cluster")
	}
	client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: strings.Split(addresses, ","), DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, ContextTimeoutEnabled: true, MaxRetries: 1, MaxRedirects: 2})
	defer client.Close()
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err())
	service := NewFeedService(NewRedisClient(client))
	prefix := fmt.Sprintf("clusterqa_%d", time.Now().UnixNano())
	timelines := make([]Timeline, 24)
	for idx := range timelines {
		timelines[idx] = &TestMockTimeline{KeyFormat: "feed:" + prefix + ":%s", TimelineId: fmt.Sprint(idx)}
	}
	epochs, errs := service.projectionEpochs(ctx, timelines, true)
	for _, err := range errs {
		require.NoError(t, err)
	}
	for idx, timeline := range timelines {
		lease, err := service.BeginRebuild(ctx, timeline)
		require.NoError(t, err)
		require.Equal(t, epochs[idx], lease.Epoch)
		require.NoError(t, service.PublishRebuild(ctx, timeline, lease, models.Statuses{{Id: 10}}, false))
	}
	targets := make([]ProjectionTarget, len(timelines))
	for idx, timeline := range timelines {
		targets[idx] = ProjectionTarget{timeline, Projection{Epoch: epochs[idx], BatchID: uint64(idx + 1), LeaseGeneration: 1, LeaseToken: "initial", RemainingLease: 20 * time.Second, StatusID: 20, ObjectVersion: 1}}
	}
	for _, err := range service.BatchApplyProjections(ctx, targets) {
		require.NoError(t, err, "cross-slot projection pipeline must route every Lua to its own slot")
	}
	for _, timeline := range timelines {
		require.Equal(t, []string{"20", "10"}, client.ZRevRange(ctx, TimelineKey(timeline), 0, -1).Val())
	}
	for idx := range targets {
		targets[idx].Projection.Remove = true
		targets[idx].Projection.ObjectVersion = 2
	}
	for _, err := range service.BatchApplyProjections(ctx, targets) {
		require.NoError(t, err)
	}
	for idx := range targets {
		targets[idx].Projection.Remove = false
		targets[idx].Projection.ObjectVersion = 1
	}
	for _, err := range service.BatchApplyProjections(ctx, targets) {
		require.ErrorIs(t, err, ErrSuperseded)
	}
	lease, err := service.BeginRebuild(ctx, timelines[0])
	require.NoError(t, err)
	lowID := Projection{Epoch: epochs[0], BatchID: 100, LeaseGeneration: 1, LeaseToken: "low-id", RemainingLease: 20 * time.Second, StatusID: 3, ObjectVersion: 1}
	require.NoError(t, service.ApplyProjection(ctx, timelines[0], lowID))
	require.ErrorIs(t, service.PublishRebuild(ctx, timelines[0], lease, models.Statuses{{Id: 10}}, false), ErrFeedRegenerating)
	require.NoError(t, client.ZScore(ctx, TimelineKey(timelines[0]), "3").Err())
	var mu sync.Mutex
	seen := map[string]bool{}
	require.NoError(t, ScanTimelineMetadata(ctx, client, "feed:v3:{"+prefix+":*}:items:meta", func(key string) error { mu.Lock(); seen[key] = true; mu.Unlock(); return nil }))
	require.Len(t, seen, len(timelines), "the same scanner used by CLI/import must visit every primary")
	slots, err := client.ClusterSlots(ctx).Result()
	require.NoError(t, err)
	owners := map[string]bool{}
	for _, timeline := range timelines {
		slot, err := client.ClusterKeySlot(ctx, TimelineKey(timeline)).Result()
		require.NoError(t, err)
		for _, rangeInfo := range slots {
			if int(slot) >= rangeInfo.Start && int(slot) <= rangeInfo.End {
				owners[rangeInfo.Nodes[0].Addr] = true
			}
		}
	}
	require.Len(t, owners, 3, "fixture must actually distribute across all three primaries")
	t.Logf("24 timelines across %d primaries: batch, tombstones, CAS and metadata scan passed", len(owners))
	if os.Getenv("FEED_CLUSTER_TEST_FAILOVER") != "1" {
		return
	}
	require.NoError(t, client.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
		n, err := node.Wait(ctx, 1, 2*time.Second).Result()
		if err != nil {
			return err
		}
		if n < 1 {
			return fmt.Errorf("primary has no synchronized replica")
		}
		return nil
	}))
	target := timelines[len(timelines)-1]
	slot, err := client.ClusterKeySlot(ctx, TimelineKey(target)).Result()
	require.NoError(t, err)
	down := ""
	replicaAddress := ""
	// CLUSTER SLOTS can briefly omit newly announced replicas even after
	// WAIT confirms replication. Refresh topology rather than using the early
	// snapshot captured during the cross-slot assertions.
	require.Eventually(t, func() bool {
		refreshed, refreshErr := client.ClusterSlots(ctx).Result()
		if refreshErr != nil {
			return false
		}
		for _, rangeInfo := range refreshed {
			if int(slot) >= rangeInfo.Start && int(slot) <= rangeInfo.End {
				down = rangeInfo.Nodes[0].Addr
				if len(rangeInfo.Nodes) > 1 {
					replicaAddress = rangeInfo.Nodes[1].Addr
				}
			}
		}
		return down != "" && replicaAddress != ""
	}, 5*time.Second, 100*time.Millisecond)

	require.NotEmpty(t, down)
	require.NotEmpty(t, replicaAddress)
	replica := redis.NewClient(&redis.Options{Addr: replicaAddress, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, ContextTimeoutEnabled: true})
	defer replica.Close()
	require.NoError(t, replica.ScriptFlush(ctx).Err())
	cached, err := replica.ScriptExists(ctx, projectionScriptSHA, prepareEpochScriptSHA).Result()
	require.NoError(t, err)
	require.Equal(t, []bool{false, false}, cached)
	t.Logf("replica %s script cache cleared before promotion: projection=false prepare=false", replicaAddress)
	observer := &clusterScriptObserver{}
	client.AddHook(observer)
	primary := redis.NewClient(&redis.Options{Addr: down, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second})
	defer primary.Close()
	require.NoError(t, primary.ShutdownNoSave(ctx).Err())
	t.Logf("stopped isolated primary %s after WAIT confirmed replication", down)
	start := time.Now()
	deadline := start.Add(20 * time.Second)
	promoted := false
	for time.Now().Before(deadline) {
		checkCtx, cancel := context.WithTimeout(ctx, time.Second)
		role, roleErr := replica.Do(checkCtx, "ROLE").Slice()
		cancel()
		if roleErr == nil && len(role) > 0 && role[0] == "master" {
			promoted = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, promoted, "replica must actually become master")
	cached, err = replica.ScriptExists(ctx, projectionScriptSHA, prepareEpochScriptSHA).Result()
	require.NoError(t, err)
	require.Equal(t, []bool{false, false}, cached, "promoted master must start without script cache")
	t.Logf("promoted master %s verified empty script cache before new hot-path request", replicaAddress)
	attempts, failures := 0, 0
	p := Projection{Epoch: epochs[len(epochs)-1], BatchID: 200, LeaseGeneration: 1, LeaseToken: "after-failover", RemainingLease: 5 * time.Second, StatusID: 30, ObjectVersion: 1}
	var lastErr error
	for time.Now().Before(deadline) {
		attempts++
		p.RemainingLease = time.Until(deadline) - 250*time.Millisecond
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, prepareErrors := service.projectionEpochs(attemptCtx, []Timeline{target}, true)
		lastErr = prepareErrors[0]
		if lastErr == nil {
			lastErr = service.BatchApplyProjections(attemptCtx, []ProjectionTarget{{target, p}})[0]
		}
		cancel()
		if lastErr == nil {
			break
		}
		failures++
		time.Sleep(150 * time.Millisecond)
	}
	require.NoError(t, lastErr, "cluster must recover within the explicit bounded retry window")
	require.Less(t, time.Since(start), 20*time.Second)
	require.NoError(t, client.ZScore(ctx, TimelineKey(target), "30").Err())
	require.ErrorIs(t, client.ZScore(ctx, TimelineKey(target), "20").Err(), redis.Nil, "replicated deletion must survive failover")
	currentEpoch, err := service.ReadTimelineEpoch(ctx, target)
	require.NoError(t, err)
	require.Equal(t, epochs[len(epochs)-1], currentEpoch)
	require.GreaterOrEqual(t, observer.missing.Load(), int64(2), "both promoted-node scripts must produce actual NOSCRIPT replies")
	require.GreaterOrEqual(t, observer.fallbacks.Load(), int64(2), "both scripts must recover through EVAL")
	cached, err = replica.ScriptExists(ctx, projectionScriptSHA, prepareEpochScriptSHA).Result()
	require.NoError(t, err)
	require.Equal(t, []bool{true, true}, cached)
	original := targets[len(targets)-1].Projection
	original.RemainingLease = time.Until(deadline)
	require.ErrorIs(t, service.BatchApplyProjections(ctx, []ProjectionTarget{{target, original}})[0], ErrSuperseded)
	newer := p
	newer.LeaseGeneration = 2
	newer.LeaseToken = "new-owner"
	newer.StatusID = 31
	newer.RemainingLease = time.Until(deadline)
	require.NoError(t, service.BatchApplyProjections(ctx, []ProjectionTarget{{target, newer}})[0])
	p.RemainingLease = time.Until(deadline)
	require.ErrorIs(t, service.BatchApplyProjections(ctx, []ProjectionTarget{{target, p}})[0], ErrStaleProjection)
	newer.LeaseToken = "wrong-owner"
	require.ErrorIs(t, service.BatchApplyProjections(ctx, []ProjectionTarget{{target, newer}})[0], ErrStaleProjection)
	t.Logf("failover recovered in %s; attempts=%d transient_errors=%d NOSCRIPT=%d EVALfallback=%d; epoch, tombstone and lease fencing retained", time.Since(start), attempts, failures, observer.missing.Load(), observer.fallbacks.Load())
}

// Observe server replies without logging command arguments or event payloads.
type clusterScriptObserver struct {
	missing   atomic.Int64
	fallbacks atomic.Int64
}

func (h *clusterScriptObserver) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (h *clusterScriptObserver) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *clusterScriptObserver) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		for _, cmd := range cmds {
			if cmd.Name() == "evalsha" && redis.HasErrorPrefix(cmd.Err(), "NOSCRIPT") {
				h.missing.Add(1)
			}
			if cmd.Name() == "eval" && cmd.Err() == nil {
				h.fallbacks.Add(1)
			}
		}
		return err
	}
}
