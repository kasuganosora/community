package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type FeedRuntimeStatus struct {
	Enabled               bool      `json:"enabled"`
	RedisAvailable        bool      `json:"redis_available"`
	Reason                string    `json:"reason,omitempty"`
	PoolGeneration        string    `json:"pool_generation,omitempty"`
	PoolAsOf              time.Time `json:"pool_as_of,omitempty"`
	PoolMode              string    `json:"pool_mode,omitempty"`
	PoolCandidates        int       `json:"pool_candidates"`
	PoolStale             bool      `json:"pool_stale"`
	PoolTruncated         bool      `json:"pool_truncated"`
	PoolPayloadBytes      int       `json:"pool_payload_bytes"`
	PoolLeaseActive       bool      `json:"pool_lease_active"`
	PoolLeaseRemainingMS  int64     `json:"pool_lease_remaining_ms"`
	SnapshotReservations  int64     `json:"snapshot_reservations"`
	SnapshotMaximum       int       `json:"snapshot_maximum"`
	SnapshotReservedBytes int64     `json:"snapshot_reserved_bytes"`
	CacheBudgetBytes      int64     `json:"cache_budget_bytes"`
	CheckedAt             time.Time `json:"checked_at"`
}

// RuntimeStatus reads fixed coordination keys only. Reserved bytes are a
// conservative admission budget, explicitly not measured Redis allocation.
// It never enumerates user snapshots, interests, content or individual actors.
func RuntimeStatus(parent context.Context) (FeedRuntimeStatus, error) {
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	config := feedConfig()
	status := FeedRuntimeStatus{CheckedAt: time.Now().UTC(), PoolStale: true, SnapshotMaximum: config.MaxActiveSnapshots, CacheBudgetBytes: config.MaxCacheBytes}
	budgetMaximum := int((config.MaxCacheBytes - (8 << 20)) / (64 << 10))
	if budgetMaximum < status.SnapshotMaximum {
		status.SnapshotMaximum = budgetMaximum
	}
	if status.SnapshotMaximum < 0 {
		status.SnapshotMaximum = 0
	}
	enabled, err := EnabledContext(ctx)
	if err != nil {
		return status, err
	}
	status.Enabled = enabled
	client := feedRedis()
	if client == nil {
		status.Reason = "redis_unavailable"
		return status, nil
	}
	pipe := client.Pipeline()
	poolResult := pipe.Get(ctx, feedPoolKey)
	leaseResult := pipe.PTTL(ctx, feedPoolLeaseKey)
	reservations := pipe.ZCount(ctx, "community:discovery:{snapshot-budget}:permits", strconv.FormatInt(status.CheckedAt.UnixMilli(), 10), "+inf")
	_, err = pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		status.Reason = "redis_unavailable"
		return status, nil
	}
	status.RedisAvailable = true
	status.SnapshotReservations = reservations.Val()
	status.SnapshotReservedBytes = status.SnapshotReservations * (64 << 10)
	lease := leaseResult.Val()
	if lease > 0 {
		status.PoolLeaseActive = true
		status.PoolLeaseRemainingMS = lease.Milliseconds()
	}
	data, err := poolResult.Bytes()
	if errors.Is(err, redis.Nil) {
		status.Reason = "pool_not_built"
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.PoolPayloadBytes = len(data)
	var pool candidatePool
	if len(data) > feedPoolMaximumBytes || json.Unmarshal(data, &pool) != nil {
		status.Reason = "invalid_pool"
		return status, nil
	}
	status.PoolGeneration = pool.Generation
	status.PoolAsOf = pool.AsOf
	status.PoolMode = pool.Mode
	status.PoolCandidates = len(pool.Candidates)
	status.PoolTruncated = pool.Truncated
	status.PoolStale = pool.AsOf.After(status.CheckedAt) || status.CheckedAt.Sub(pool.AsOf) >= PoolMaxAge
	if status.PoolStale {
		status.Reason = "pool_expired"
	}
	return status, nil
}
