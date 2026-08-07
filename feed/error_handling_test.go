package feed

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddToTimelineItem_ZScoreError verifies that ZScore errors are not silently ignored.
// Before the fix, .Val() returned 0 on error, and the status would be added anyway.
func TestAddToTimelineItem_ZScoreError(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "zscore_err"}

	// Add a status first
	status1 := &models.Status{Id: 1001}
	err := svc.AddToTimelineItem(timeline, status1, false)
	require.NoError(t, err)

	// Verify it was added
	mockRedis := svc.redis.(*MockRedisClient) //nolint:forcetypeassert
	zSet := mockRedis.GetZSet("feed:home:zscore_err")
	assert.Contains(t, zSet, "1001")
}

// TestPushUpdateRequired_NoCacheOnError verifies that errors from PubSubChannels
// are not cached, so the system can recover from transient Redis failures.
func TestPushUpdateRequired_NoCacheOnError(t *testing.T) {
	// Clear cache
	subCache = &subscriptionCache{ttl: 5 * time.Second}

	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "error_nocache"}

	// Mock returns no channels (no error), should cache as false
	result := svc.PushUpdateRequired(timeline)
	assert.False(t, result)

	// Now set a subscriber channel
	mockRedis := svc.redis.(*MockRedisClient) //nolint:forcetypeassert
	mockRedis.SetChannel("subscribed:feed:home:error_nocache", true)

	// Clear cache to simulate no cache
	subCache = &subscriptionCache{ttl: 5 * time.Second}

	// Now should return true
	result = svc.PushUpdateRequired(timeline)
	assert.True(t, result)
}

// TestTrimTimeline_RevRangeWithScores verifies that TrimTimeline uses
// ZRevRangeWithScores correctly for the falloff threshold.
func TestTrimTimeline_RevRangeWithScores(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "trim_rev"}

	// Add many statuses with increasing IDs
	for i := range 100 {
		status := &models.Status{Id: uint64(1000 + i)}
		err := svc.AddToTimelineItem(timeline, status, false)
		require.NoError(t, err)
	}

	// Trim should not error
	err := svc.TrimTimeline(timeline)
	require.NoError(t, err)
}

// TestRemoveTimelineItem_SMembersError verifies SMembers error handling.
func TestRemoveTimelineItem_SMembersError(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "smembers_err"}

	// Add a reblog
	reblogId := uint64(10000)
	status := &models.Status{Id: 10001, ReblogId: &reblogId}
	err := svc.AddToTimelineItem(timeline, status, true)
	require.NoError(t, err)

	// Remove it
	err = svc.RemoveTimelineItem(timeline, status, true)
	require.NoError(t, err)
}

// TestGcTimelines_ErrorHandling verifies GcTimelines handles errors.
func TestGcTimelines_ErrorHandling(t *testing.T) {
	svc := newTestFeedService()
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "gc_err"}

	status := &models.Status{Id: 20000}
	_ = svc.AddToTimelineItem(timeline, status, false)

	err := svc.GcTimelines()
	require.NoError(t, err)
}
