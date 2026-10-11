package feed

import "context"

// Invalidation fences in-flight cross-process snapshots without waiting for SQL.
func InvalidateTimelineContext(ctx context.Context, timeline Timeline) error {
	return getDefaultService().InvalidateTimelineContext(ctx, timeline)
}
func (s *FeedService) InvalidateTimelineContext(ctx context.Context, timeline Timeline) error {
	_, err := s.eval(ctx, `local k=KEYS[1];local m=k..':meta'
if redis.call('HEXISTS',m,'epoch')==1 then redis.call('HINCRBY',m,'generation',1);redis.call('HSET',m,'ready',0) end
for _,id in ipairs(redis.call('ZRANGE',k..':reblogs',0,-1)) do redis.call('DEL',k..':reblogs:'..id) end
redis.call('DEL',k,k..':reblogs',k..':empty',k..':rebuild')
return 1`, []string{TimelineKey(timeline)})
	return err
}
