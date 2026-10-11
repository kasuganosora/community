package feed

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func protocolFixture(t *testing.T) (*miniredis.Miniredis, *redis.Client, *FeedService, *TestMockTimeline) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, client, NewFeedService(NewRedisClient(client)), &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "42"}
}
func TestProtocolSnapshotCASAndDeleteTombstone(t *testing.T) {
	_, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	_, err := service.ReadTimelineEpoch(ctx, timeline)
	require.ErrorIs(t, err, ErrNeedsRebuild)
	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(lease)
	_, err = service.BeginRebuild(ctx, timeline)
	require.ErrorIs(t, err, ErrFeedRegenerating)
	p := Projection{Epoch: lease.Epoch, BatchID: 1, LeaseGeneration: 1, LeaseToken: "one", RemainingLease: time.Second, StatusID: 10, ObjectVersion: 2, Remove: true}
	require.NoError(t, service.ApplyProjection(ctx, timeline, p))
	require.ErrorIs(t, service.PublishRebuild(ctx, timeline, lease, models.Statuses{&models.Status{Id: 10}}, false), ErrFeedRegenerating)
	p.Remove = false
	p.ObjectVersion = 1
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrSuperseded)
	p.ObjectVersion = 2
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrSuperseded)
	require.Zero(t, client.ZCard(ctx, TimelineKey(timeline)).Val())
	require.False(t, client.Exists(ctx, "timelines").Val() > 0)
}
func TestProtocolEpochResetAndNewLeaseFence(t *testing.T) {
	server, _, service, timeline := protocolFixture(t)
	ctx := context.Background()
	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(lease)
	require.NoError(t, service.PublishRebuild(ctx, timeline, lease, nil, false))
	p := Projection{Epoch: lease.Epoch, BatchID: 2, LeaseGeneration: 2, LeaseToken: "new", RemainingLease: time.Second, StatusID: 5, ObjectVersion: 1}
	require.NoError(t, service.ApplyProjection(ctx, timeline, p))
	p.LeaseGeneration = 1
	p.LeaseToken = "old"
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrStaleProjection)
	server.FlushAll()
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrNeedsRebuild)
	next, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(next)
	require.NotEqual(t, lease.Epoch, next.Epoch)
	p.LeaseGeneration = 3
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrNeedsRebuild)
	require.ErrorIs(t, service.PublishRebuild(ctx, timeline, lease, nil, false), ErrFeedRegenerating)
}
func TestProtocolInvalidationFencesLowIDSnapshot(t *testing.T) {
	_, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(lease)
	require.NoError(t, service.InvalidateTimelineContext(ctx, timeline))
	require.ErrorIs(t, service.PublishRebuild(ctx, timeline, lease, models.Statuses{&models.Status{Id: 100}}, false), ErrFeedRegenerating)
	fresh, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(fresh)
	require.NoError(t, service.PublishRebuild(ctx, timeline, fresh, models.Statuses{&models.Status{Id: 100}, &models.Status{Id: 2}}, false))
	require.Equal(t, []string{"100", "2"}, client.ZRevRange(ctx, TimelineKey(timeline), 0, -1).Val())
}
func TestProtocolMissingVersionMetadataFailsClosed(t *testing.T) {
	_, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(lease)
	require.NoError(t, service.PublishRebuild(ctx, timeline, lease, nil, false))
	require.NoError(t, client.Del(ctx, TimelineKey(timeline)+":versions").Err())
	p := Projection{Epoch: lease.Epoch, BatchID: 1, LeaseGeneration: 1, LeaseToken: "token", RemainingLease: time.Second, StatusID: 5, ObjectVersion: 1}
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrNeedsRebuild)
	fresh, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(fresh)
	require.NotEqual(t, lease.Epoch, fresh.Epoch)
}
func TestProtocolBatchPartialFailureAndExpiry(t *testing.T) {
	server, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	server.SetTime(time.Unix(10000, 0))
	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(lease)
	require.NoError(t, service.PublishRebuild(ctx, timeline, lease, nil, false))
	p := Projection{Epoch: lease.Epoch, BatchID: 1, LeaseGeneration: 1, LeaseToken: "token", RemainingLease: time.Second, StatusID: 5, ObjectVersion: 1}
	bad := p
	bad.Epoch = "bad"
	results := service.BatchApplyProjections(ctx, []ProjectionTarget{{timeline, p}, {timeline, bad}})
	require.NoError(t, results[0])
	require.ErrorIs(t, results[1], ErrNeedsRebuild)
	server.SetTime(time.Unix(10002, 0))
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrStaleProjection)
	require.Equal(t, float64(10000*1000+7*24*60*60*1000), client.ZScore(ctx, TimelineKey(timeline)+":version_expiry", "5").Val())
}

func TestProtocolColdBootstrapIsNotReadyAndUsesFreshEpoch(t *testing.T) {
	server, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	epochs, failures := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, failures[0])
	require.NotEmpty(t, epochs[0])
	require.Equal(t, "0", client.HGet(ctx, TimelineKey(timeline)+":meta", "ready").Val())
	again, failures := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, failures[0])
	require.Equal(t, epochs, again)
	server.FlushAll()
	next, failures := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, failures[0])
	require.NotEqual(t, epochs, next)
	p := Projection{Epoch: epochs[0], BatchID: 1, LeaseGeneration: 1, LeaseToken: "old", RemainingLease: time.Second, StatusID: 5, ObjectVersion: 1}
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrNeedsRebuild)
}

func TestProtocolExpiredVersionsAreGarbageCollected(t *testing.T) {
	server, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	server.SetTime(time.Unix(100, 0))
	epochs, errs := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, errs[0])
	p := Projection{Epoch: epochs[0], BatchID: 1, LeaseGeneration: 1, LeaseToken: "one", RemainingLease: time.Second, StatusID: 5, ObjectVersion: 2, Remove: true}
	require.NoError(t, service.ApplyProjection(ctx, timeline, p))
	server.SetTime(time.Unix(100, 0).Add(8 * 24 * time.Hour))
	p.BatchID = 2
	p.StatusID = 6
	require.NoError(t, service.ApplyProjection(ctx, timeline, p))
	require.False(t, client.HExists(ctx, TimelineKey(timeline)+":versions", "5").Val())
	require.True(t, client.HExists(ctx, TimelineKey(timeline)+":versions", "6").Val())
}

func TestProtocolTwoInstancesOnlyOneSnapshotPublishes(t *testing.T) {
	server, _, first, timeline := protocolFixture(t)
	ctx := context.Background()
	secondClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer secondClient.Close()
	second := NewFeedService(NewRedisClient(secondClient))
	firstLease, err := first.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(firstLease)
	_, err = second.BeginRebuild(ctx, timeline)
	require.ErrorIs(t, err, ErrFeedRegenerating)
	server.FastForward(31 * time.Second)
	secondLease, err := second.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	require.NoError(t, second.PublishRebuild(ctx, timeline, secondLease, models.Statuses{&models.Status{Id: 7}}, false))
	require.ErrorIs(t, first.PublishRebuild(ctx, timeline, firstLease, models.Statuses{&models.Status{Id: 8}}, false), ErrFeedRegenerating)
	require.Equal(t, []string{"7"}, secondClient.ZRevRange(ctx, TimelineKey(timeline), 0, -1).Val())
}

func TestProtocolMissingLiveFenceRejected(t *testing.T) {
	_, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	epochs, errs := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, errs[0])
	p := Projection{Epoch: epochs[0], BatchID: 10, LeaseGeneration: 2, LeaseToken: "new", RemainingLease: time.Second, StatusID: 5, ObjectVersion: 1}
	require.NoError(t, service.ApplyProjection(ctx, timeline, p))
	require.NoError(t, client.Del(ctx, TimelineKey(timeline)+":fence:10").Err())
	p.LeaseGeneration = 1
	p.LeaseToken = "old"
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrNeedsRebuild)
}

func TestProtocolLeasedInvalidationFencedAndIdempotent(t *testing.T) {
	server, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	require.NoError(t, service.PublishRebuild(ctx, timeline, lease, models.Statuses{{Id: 10}}, false))
	snapshot, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	defer releaseRebuildSlot(snapshot)
	p := Projection{Epoch: lease.Epoch, BatchID: 8, LeaseGeneration: 2, LeaseToken: "new", RemainingLease: time.Second, Invalidate: true}
	require.NoError(t, service.ApplyProjection(ctx, timeline, p))
	key := TimelineKey(timeline)
	generation := client.HGet(ctx, key+":meta", "generation").Val()
	require.NoError(t, service.ApplyProjection(ctx, timeline, p))
	require.Equal(t, generation, client.HGet(ctx, key+":meta", "generation").Val(), "same batch replay must not invalidate a new SQL snapshot twice")
	require.ErrorIs(t, service.PublishRebuild(ctx, timeline, snapshot, models.Statuses{{Id: 10}}, false), ErrFeedRegenerating)
	p.LeaseGeneration = 1
	p.LeaseToken = "old"
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrStaleProjection)
	server.FlushAll()
	_, errs := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, errs[0])
	p.LeaseGeneration = 3
	require.ErrorIs(t, service.ApplyProjection(ctx, timeline, p), ErrNeedsRebuild)
}

func TestExpiredReadBudgetReleasesLocalSlotWithoutRedisCleanup(t *testing.T) {
	server, client, service, timeline := protocolFixture(t)
	previousService, previousOverride := defaultService, defaultServiceOverride
	SetDefaultService(service)
	t.Cleanup(func() { defaultService = previousService; defaultServiceOverride = previousOverride })
	before := len(rebuildSlots)
	lease, err := BeginRebuild(context.Background(), timeline)
	require.NoError(t, err)
	require.Equal(t, before+1, len(rebuildSlots))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ReleaseRebuild(ctx, timeline, lease)
	require.Equal(t, before, len(rebuildSlots))
	require.Equal(t, lease.Token, client.Get(context.Background(), TimelineKey(timeline)+":rebuild").Val(), "expired read must not synchronously contact Redis or delete a successor lease")
	server.FastForward(31 * time.Second)
	next, err := BeginRebuild(context.Background(), timeline)
	require.NoError(t, err)
	require.NotEqual(t, lease.Token, next.Token)
	ReleaseRebuild(context.Background(), timeline, next)
}
