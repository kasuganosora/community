package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
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

// RebuildTimeline writes a precomputed feed in a bounded pipeline. The caller
// must select the same candidates and visibility rules as its normal read path.
func RebuildTimeline(timeline Timeline, statuses models.Statuses, aggregate bool) error {
	return RebuildTimelineContext(context.Background(), timeline, statuses, aggregate)
}
func RebuildTimelineContext(ctx context.Context, timeline Timeline, statuses models.Statuses, aggregate bool) error {
	return getDefaultService().rebuildTimelineContext(ctx, timeline, statuses, aggregate)
}
func (s *FeedService) RebuildTimeline(timeline Timeline, statuses models.Statuses, aggregate bool) error {
	return s.rebuildTimelineContext(context.Background(), timeline, statuses, aggregate)
}

// Replace only the candidate snapshot's old range. Pushes committed after the
// SQL snapshot have newer IDs and survive this atomic merge, including their
// reblog metadata. No global publication lock is taken.
const rebuildTimelineScript = `
local key=KEYS[1]
local reblogs=key..':reblogs'
local cutoff=ARGV[1]
local snapshot=cjson.decode(ARGV[2])
local origins=redis.call('ZRANGE',reblogs,0,-1)
for _,original in ipairs(origins) do
 local setkey=reblogs..':'..original
 for _,member in ipairs(redis.call('SMEMBERS',setkey)) do
  if tonumber(member)<=tonumber(cutoff) then redis.call('SREM',setkey,member) end
 end
end
redis.call('ZREMRANGEBYSCORE',key,'-inf',cutoff)
redis.call('ZREMRANGEBYSCORE',reblogs,'-inf',cutoff)
for original,score in pairs(snapshot.reblogs) do redis.call('ZADD',reblogs,'NX',score,original) end
for _,id in ipairs(snapshot.items) do
 if not redis.call('ZSCORE',reblogs,id) then redis.call('ZADD',key,id,id) end
end
for original,ids in pairs(snapshot.duplicates) do
 for _,id in ipairs(ids) do redis.call('SADD',reblogs..':'..original,id) end
end
redis.call('ZREMRANGEBYRANK',key,0,-513)
local threshold=redis.call('ZREVRANGE',key,40,40,'WITHSCORES')
if #threshold>0 then
 for _,original in ipairs(redis.call('ZRANGEBYSCORE',reblogs,0,threshold[2])) do
  redis.call('ZREM',reblogs,original)
  redis.call('DEL',reblogs..':'..original)
 end
end
redis.call('ZADD','timelines',ARGV[3],key)
return 1
`

func (s *FeedService) rebuildTimelineContext(ctx context.Context, timeline Timeline, statuses models.Statuses, aggregate bool) error {
	snapshot := buildTimelineSnapshot(statuses, aggregate)
	var cutoff uint64
	for _, status := range statuses {
		if status != nil && status.Id > cutoff {
			cutoff = status.Id
		}
	}
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
	key := fmt.Sprintf(timeline.GetKeyFormat(), timeline.GetTimelineId())
	_, err = s.redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Eval(ctx, rebuildTimelineScript, []string{key}, cutoff, string(data), time.Now().Unix())
		return nil
	})
	return err
}
