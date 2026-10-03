package feed

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
)

// InvalidateTimelineContext clears candidates after an out-of-band committed
// write. It waits for this process's in-flight rebuild, so an older SQL snapshot
// cannot repopulate the cache after invalidation. It emits no client events.
// Distributed writers require a maintenance/cold-start boundary when migrating.
func InvalidateTimelineContext(ctx context.Context, timeline Timeline) error {
	return getDefaultService().InvalidateTimelineContext(ctx, timeline)
}
func (s *FeedService) InvalidateTimelineContext(ctx context.Context, timeline Timeline) error {
	key := fmt.Sprintf(timeline.GetKeyFormat(), timeline.GetTimelineId())
	var call *rebuildCall
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rebuildMu.Lock()
		prior := rebuildCalls[key]
		if prior == nil {
			call = &rebuildCall{done: make(chan struct{})}
			rebuildCalls[key] = call
			rebuildMu.Unlock()
			break
		}
		rebuildMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-prior.done:
		}
	}
	defer func() {
		rebuildMu.Lock()
		// A reader waiting on this barrier still needs a real rebuild after
		// invalidation; it must not mistake cleanup for a populated snapshot.
		if call.err == nil {
			call.err = context.Canceled
		}
		delete(rebuildCalls, key)
		close(call.done)
		rebuildMu.Unlock()
	}()
	// Atomic cleanup includes aggregation sets and the negative-cache marker.
	_, call.err = s.redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Eval(ctx, `local origins=redis.call('ZRANGE',KEYS[1]..':reblogs',0,-1)
for _,id in ipairs(origins) do redis.call('DEL',KEYS[1]..':reblogs:'..id) end
redis.call('DEL',KEYS[1],KEYS[1]..':reblogs',KEYS[1]..':empty')
redis.call('ZREM','timelines',KEYS[1])
return 1`, []string{key})
		return nil
	})
	return call.err
}
