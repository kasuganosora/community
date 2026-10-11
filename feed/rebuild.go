package feed

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"

	"github.com/kasuganosora/community/models"
)

type rebuildCall struct {
	done chan struct{}
	err  error
}

var rebuildMu sync.Mutex
var rebuildCalls = map[string]*rebuildCall{}

// Concurrent misses for the same feed share one rebuild, not one per request.
// This only coordinates in-flight work; it does not cache authorization/results.
func coalesceRebuild(key string, rebuild func() error) error {
	return coalesceRebuildContext(context.Background(), key, rebuild)
}
func coalesceRebuildContext(ctx context.Context, key string, rebuild func() error) error {
	rebuildMu.Lock()
	if call := rebuildCalls[key]; call != nil {
		rebuildMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-call.done:
		}
		if (errors.Is(call.err, context.Canceled) || errors.Is(call.err, context.DeadlineExceeded)) && ctx.Err() == nil {
			return coalesceRebuildContext(ctx, key, rebuild)
		}
		return call.err
	}
	call := &rebuildCall{done: make(chan struct{})}
	rebuildCalls[key] = call
	rebuildMu.Unlock()
	defer func() { rebuildMu.Lock(); delete(rebuildCalls, key); close(call.done); rebuildMu.Unlock() }()
	call.err = rebuild()
	if call.err == nil {
		rebuildOutcomes.WithLabelValues("success").Inc()
	} else {
		rebuildOutcomes.WithLabelValues("error").Inc()
	}
	return call.err
}

type timelineSnapshot struct {
	items      []uint64
	reblogs    map[uint64]uint64
	duplicates map[uint64][]uint64
}

// Reproduce AddToTimelineItem+TrimTimeline's reblog falloff in memory. Rebuilds
// have at most MAX_ITEMS entries; no per-post Redis round trips are necessary.
func buildTimelineSnapshot(statuses models.Statuses, aggregate bool) timelineSnapshot {
	snapshot := timelineSnapshot{items: []uint64{}, reblogs: map[uint64]uint64{}, duplicates: map[uint64][]uint64{}}
	insert := func(id uint64) {
		i := sort.Search(len(snapshot.items), func(i int) bool { return snapshot.items[i] <= id })
		if i < len(snapshot.items) && snapshot.items[i] == id {
			return
		}
		snapshot.items = append(snapshot.items, 0)
		copy(snapshot.items[i+1:], snapshot.items[i:])
		snapshot.items[i] = id
	}
	for _, status := range statuses {
		if status == nil {
			continue
		}
		if aggregate && status.ReblogId != nil && *status.ReblogId > 0 {
			original := *status.ReblogId
			rank := sort.Search(len(snapshot.items), func(i int) bool { return snapshot.items[i] <= original })
			if rank < len(snapshot.items) && snapshot.items[rank] == original && rank < REBLOG_FALLOFF {
				continue
			}
			if _, exists := snapshot.reblogs[original]; !exists {
				snapshot.reblogs[original] = status.Id
				insert(status.Id)
			} else {
				snapshot.duplicates[original] = append(snapshot.duplicates[original], status.Id)
			}
		} else if snapshot.reblogs[status.Id] == 0 {
			insert(status.Id)
		}
		if len(snapshot.items) > MAX_ITEMS {
			snapshot.items = snapshot.items[:MAX_ITEMS]
		}
		if len(snapshot.items) > REBLOG_FALLOFF {
			threshold := snapshot.items[REBLOG_FALLOFF]
			for original, score := range snapshot.reblogs {
				if score <= threshold {
					delete(snapshot.reblogs, original)
					delete(snapshot.duplicates, original)
				}
			}
		}
	}
	return snapshot
}

// Snapshots can publish only against the lease captured before reading SQL.
const rebuildTimelineScript = `
local k=KEYS[1];local m=k..':meta'
if redis.call('HGET',m,'epoch')~=ARGV[1] or redis.call('HGET',m,'generation')~=ARGV[2] or redis.call('GET',k..':rebuild')~=ARGV[3] then return 0 end
local snapshot=cjson.decode(ARGV[4])
for _,original in ipairs(redis.call('ZRANGE',k..':reblogs',0,-1)) do redis.call('DEL',k..':reblogs:'..original) end
redis.call('DEL',k,k..':reblogs',k..':empty')
for original,score in pairs(snapshot.reblogs) do redis.call('ZADD',k..':reblogs',score,original) end
for _,id in ipairs(snapshot.items) do redis.call('ZADD',k,id,id) end
for original,ids in pairs(snapshot.duplicates) do
 for _,id in ipairs(ids) do redis.call('SADD',k..':reblogs:'..original,id) end
 redis.call('EXPIRE',k..':reblogs:'..original,604800)
end
local now=redis.call('TIME')
redis.call('HSET',m,'ready',1,'checked_at',now[1],'count',#snapshot.items)
redis.call('DEL',k..':rebuild')
redis.call('EXPIRE',k,604800)
redis.call('EXPIRE',k..':reblogs',604800)
redis.call('EXPIRE',m,604800)
redis.call('EXPIRE',k..':versions',604800)
return 1
`

func PublishRebuild(ctx context.Context, timeline Timeline, lease RebuildLease, statuses models.Statuses, aggregate bool) error {
	return getDefaultService().PublishRebuild(ctx, timeline, lease, statuses, aggregate)
}
func (s *FeedService) PublishRebuild(ctx context.Context, timeline Timeline, lease RebuildLease, statuses models.Statuses, aggregate bool) error {
	defer releaseRebuildSlot(lease)
	snapshot := buildTimelineSnapshot(statuses, aggregate)
	items := make([]string, len(snapshot.items))
	for i, id := range snapshot.items {
		items[i] = strconv.FormatUint(id, 10)
	}
	reblogs := map[string]string{}
	for origin, id := range snapshot.reblogs {
		reblogs[strconv.FormatUint(origin, 10)] = strconv.FormatUint(id, 10)
	}
	duplicates := map[string][]string{}
	for origin, ids := range snapshot.duplicates {
		members := make([]string, len(ids))
		for i, id := range ids {
			members[i] = strconv.FormatUint(id, 10)
		}
		duplicates[strconv.FormatUint(origin, 10)] = members
	}
	data, err := json.Marshal(struct {
		Items      []string            `json:"items"`
		Reblogs    map[string]string   `json:"reblogs"`
		Duplicates map[string][]string `json:"duplicates"`
	}{items, reblogs, duplicates})
	if err != nil {
		return err
	}
	result, err := s.eval(ctx, rebuildTimelineScript, []string{TimelineKey(timeline)}, lease.Epoch, lease.Generation, lease.Token, string(data))
	if err != nil {
		return err
	}
	if result != int64(1) {
		return ErrFeedRegenerating
	}
	return nil
}
