package feed

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"

	"github.com/kasuganosora/community/models"
)

type rebuildCall struct {
	done chan struct{}
	err  error
}

type rebuildBucket struct {
	mu    sync.Mutex
	calls map[string]*rebuildCall
}

var rebuildBuckets [64]rebuildBucket

func rebuildBucketFor(key string) *rebuildBucket {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &rebuildBuckets[h.Sum32()&63]
}

func hasRebuildCall(key string) bool {
	b := rebuildBucketFor(key)
	b.mu.Lock()
	_, exists := b.calls[key]
	b.mu.Unlock()
	return exists
}

// Concurrent misses for the same feed share one rebuild, not one per request.
// This only coordinates in-flight work; it does not cache authorization/results.
func coalesceRebuild(key string, rebuild func() error) error {
	return coalesceRebuildContext(context.Background(), key, rebuild)
}
func coalesceRebuildContext(ctx context.Context, key string, rebuild func() error) error {
	b := rebuildBucketFor(key)
	b.mu.Lock()
	if b.calls == nil {
		b.calls = make(map[string]*rebuildCall)
	}
	if call := b.calls[key]; call != nil {
		b.mu.Unlock()
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
	b.calls[key] = call
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.calls, key); close(call.done); b.mu.Unlock() }()
	call.err = rebuild()
	if call.err == nil {
		rebuildOutcomes.WithLabelValues("success").Inc()
	} else {
		rebuildOutcomes.WithLabelValues("error").Inc()
	}
	return call.err
}

type timelineRank struct {
	id    uint64
	score float64
}

// newer reports whether a should be shown above b. Publication time wins;
// id only breaks ties, matching StatusTimelineOrder.
func (a timelineRank) newer(b timelineRank) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.id > b.id
}

type timelineSnapshot struct {
	items      []timelineRank
	reblogs    map[uint64]timelineRank
	duplicates map[uint64][]uint64
}

// Reproduce AddToTimelineItem+TrimTimeline's reblog falloff in memory. Rebuilds
// have at most MAX_ITEMS entries; no per-post Redis round trips are necessary.
// Items are newest publication time first, not highest serial id.
func buildTimelineSnapshot(statuses models.Statuses, aggregate bool) timelineSnapshot {
	snapshot := timelineSnapshot{items: []timelineRank{}, reblogs: map[uint64]timelineRank{}, duplicates: map[uint64][]uint64{}}
	ordered := make(models.Statuses, 0, len(statuses))
	for _, status := range statuses {
		if status != nil {
			ordered = append(ordered, status)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return (timelineRank{id: ordered[i].Id, score: TimelineScore(ordered[i])}).newer(timelineRank{id: ordered[j].Id, score: TimelineScore(ordered[j])})
	})
	insert := func(item timelineRank) {
		i := sort.Search(len(snapshot.items), func(i int) bool { return !snapshot.items[i].newer(item) })
		if i < len(snapshot.items) && snapshot.items[i].id == item.id {
			return
		}
		snapshot.items = append(snapshot.items, timelineRank{})
		copy(snapshot.items[i+1:], snapshot.items[i:])
		snapshot.items[i] = item
	}
	for _, status := range ordered {
		item := timelineRank{id: status.Id, score: TimelineScore(status)}
		if aggregate && status.ReblogId != nil && *status.ReblogId > 0 {
			original := *status.ReblogId
			position := -1
			for i, existing := range snapshot.items {
				if existing.id == original {
					position = i
					break
				}
			}
			if position >= 0 && position < REBLOG_FALLOFF {
				continue
			}
			if _, exists := snapshot.reblogs[original]; !exists {
				snapshot.reblogs[original] = item
				insert(item)
			} else {
				snapshot.duplicates[original] = append(snapshot.duplicates[original], status.Id)
			}
		} else if _, represented := snapshot.reblogs[status.Id]; !represented {
			insert(item)
		}
		if len(snapshot.items) > MAX_ITEMS {
			snapshot.items = snapshot.items[:MAX_ITEMS]
		}
		if len(snapshot.items) > REBLOG_FALLOFF {
			boundary := snapshot.items[REBLOG_FALLOFF]
			for original, rep := range snapshot.reblogs {
				// At or below the falloff row: same comparison as score <= boundary
				// when scores were serial ids.
				if !rep.newer(boundary) {
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
for _,item in ipairs(snapshot.items) do redis.call('ZADD',k,item.score,item.id) end
for original,ids in pairs(snapshot.duplicates) do
 for _,id in ipairs(ids) do redis.call('SADD',k..':reblogs:'..original,id) end
 redis.call('EXPIRE',k..':reblogs:'..original,604800)
end
local now=redis.call('TIME')
redis.call('HSET',m,'ready',1,'checked_at',now[1],'count',#snapshot.items,'complete',ARGV[5],'rank','ca1')
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
	complete := 0
	if len(statuses) < MAX_ITEMS {
		complete = 1
	}
	snapshot := buildTimelineSnapshot(statuses, aggregate)
	items := make([]struct {
		ID    string `json:"id"`
		Score string `json:"score"`
	}, len(snapshot.items))
	for i, item := range snapshot.items {
		items[i].ID = strconv.FormatUint(item.id, 10)
		items[i].Score = strconv.FormatFloat(item.score, 'f', -1, 64)
	}
	reblogs := map[string]string{}
	for origin, rep := range snapshot.reblogs {
		reblogs[strconv.FormatUint(origin, 10)] = strconv.FormatFloat(rep.score, 'f', -1, 64)
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
		Items      any                 `json:"items"`
		Reblogs    map[string]string   `json:"reblogs"`
		Duplicates map[string][]string `json:"duplicates"`
	}{items, reblogs, duplicates})
	if err != nil {
		return err
	}
	result, err := s.eval(ctx, rebuildTimelineScript, []string{TimelineKey(timeline)}, lease.Epoch, lease.Generation, lease.Token, string(data), complete)
	if err != nil {
		return err
	}
	if result != int64(1) {
		return ErrFeedRegenerating
	}
	return nil
}
