package feed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var projectionScriptSHA = redis.NewScript(projectionScript).Hash()
var prepareEpochScriptSHA = redis.NewScript(prepareEpochScript).Hash()

var ErrNeedsRebuild = errors.New("feed cache requires rebuild")
var ErrStaleProjection = errors.New("feed projection lease is stale")
var ErrSuperseded = errors.New("feed object projection is superseded")

// TimelineKey isolates every feed's related keys in one Redis Cluster slot.
func TimelineKey(t Timeline) string {
	key := fmt.Sprintf(t.GetKeyFormat(), t.GetTimelineId())
	if strings.HasPrefix(key, "feed:v3:{") {
		return key
	}
	key = strings.TrimPrefix(key, "feed:")
	if strings.HasPrefix(key, "list:v2:") {
		key = "list:" + strings.TrimPrefix(key, "list:v2:")
	}
	key = strings.ReplaceAll(key, "%", "%25")
	key = strings.ReplaceAll(strings.ReplaceAll(key, "{", "%7B"), "}", "%7D")
	return "feed:v3:{" + key + "}:items"
}

type RebuildLease struct {
	Epoch, Token string
	Generation   int64
	release      *sync.Once
}
type Projection struct {
	Epoch           string
	BatchID         uint64
	LeaseGeneration uint64
	LeaseToken      string
	// RemainingLease is calculated from SQL server time after claiming/renewing;
	// callers must subtract transport time before calling. Never reset to 30s.
	RemainingLease                    time.Duration
	StatusID, ObjectVersion, ReblogID uint64
	Remove, Aggregate                 bool
	Invalidate                        bool
}

func randomFeedToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *FeedService) eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	limited, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = limited
	var result *redis.Cmd
	_, err := s.redis.Pipelined(ctx, func(p redis.Pipeliner) error { result = p.Eval(ctx, script, keys, args...); return nil })
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("feed Redis evaluator returned no result")
	}
	return result.Result()
}

const beginRebuildScript = `
local m=KEYS[1]..':meta'
if redis.call('EXISTS',KEYS[1]..':rebuild')==1 then return {} end
if redis.call('HEXISTS',m,'epoch')==0 or not tonumber(redis.call('HGET',m,'generation')) or redis.call('HGET',KEYS[1]..':versions','_epoch')~=redis.call('HGET',m,'epoch') then
 redis.call('HSET',m,'epoch',ARGV[1],'generation',0,'ready',0)
 redis.call('HSET',KEYS[1]..':versions','_epoch',ARGV[1])
 redis.call('EXPIRE',KEYS[1]..':versions',604800)
end
redis.call('EXPIRE',m,604800)
redis.call('SET',KEYS[1]..':rebuild',ARGV[2],'PX',30000)
return {redis.call('HGET',m,'epoch'),redis.call('HGET',m,'generation')}
`

func BeginRebuild(ctx context.Context, t Timeline) (RebuildLease, error) {
	return getDefaultService().BeginRebuild(ctx, t)
}

var rebuildSlots = make(chan struct{}, 8)

func (s *FeedService) BeginRebuild(ctx context.Context, t Timeline) (RebuildLease, error) {
	select {
	case rebuildSlots <- struct{}{}:
	default:
		return RebuildLease{}, ErrFeedRegenerating
	}
	held := false
	defer func() {
		if !held {
			<-rebuildSlots
		}
	}()
	token, err := randomFeedToken()
	if err != nil {
		return RebuildLease{}, err
	}
	value, err := s.eval(ctx, beginRebuildScript, []string{TimelineKey(t)}, token, token)
	if err != nil {
		return RebuildLease{}, err
	}
	values, ok := value.([]interface{})
	if !ok || len(values) != 2 {
		return RebuildLease{}, ErrFeedRegenerating
	}
	var generation int64
	_, err = fmt.Sscan(fmt.Sprint(values[1]), &generation)
	if err != nil {
		return RebuildLease{}, err
	}
	held = true
	return RebuildLease{Epoch: fmt.Sprint(values[0]), Token: token, Generation: generation, release: &sync.Once{}}, nil
}
func ReadTimelineEpoch(ctx context.Context, t Timeline) (string, error) {
	return getDefaultService().ReadTimelineEpoch(ctx, t)
}
func (s *FeedService) ReadTimelineEpoch(ctx context.Context, t Timeline) (string, error) {
	v, err := s.eval(ctx, prepareEpochScript, []string{TimelineKey(t)}, "")
	if err != nil {
		return "", err
	}
	epoch, _ := v.(string)
	if epoch == "" {
		return "", ErrNeedsRebuild
	}
	return epoch, nil
}

const projectionScript = `
local k=KEYS[1]; local m=k..':meta'; local f=k..':fence:'..ARGV[2]
if redis.call('HGET',m,'epoch')~=ARGV[1] or not tonumber(redis.call('HGET',m,'generation')) or redis.call('HGET',k..':versions','_epoch')~=ARGV[1] then return -1 end
local tm=redis.call('TIME');local now=tonumber(tm[1])*1000+math.floor(tonumber(tm[2])/1000)
local registry=k..':fences'
for _,id in ipairs(redis.call('ZRANGEBYSCORE',registry,'-inf',now,'LIMIT',0,128)) do redis.call('ZREM',registry,id) end
if redis.call('EXISTS',f)==0 and redis.call('ZSCORE',registry,ARGV[2]) then return -1 end
local gen=tonumber(redis.call('HGET',f,'generation') or '-1')
local requested=tonumber(ARGV[3]);local ttl=tonumber(ARGV[5])
if ttl<=0 or requested<gen then return -2 end
if requested==gen and redis.call('HGET',f,'token')~=ARGV[4] then return -2 end
local deadline=now+ttl
if requested==gen then deadline=math.min(deadline,tonumber(redis.call('HGET',f,'deadline') or '0')) end
if deadline<=now then return -2 end
redis.call('HSET',f,'generation',ARGV[3],'token',ARGV[4],'deadline',deadline)
redis.call('PEXPIRE',f,604800000)
redis.call('ZADD',registry,now+604800000,ARGV[2])
redis.call('EXPIRE',registry,604800)
if ARGV[11]=='1' then
 if redis.call('HGET',f,'invalidated_epoch')==ARGV[1] then return 1 end
 redis.call('HINCRBY',m,'generation',1)
 redis.call('HSET',m,'ready',0)
 for _,id in ipairs(redis.call('ZRANGE',k..':reblogs',0,-1)) do redis.call('DEL',k..':reblogs:'..id) end
 redis.call('DEL',k,k..':reblogs',k..':empty',k..':rebuild')
 redis.call('HSET',f,'invalidated_epoch',ARGV[1])
 redis.call('EXPIRE',m,604800)
 return 1
end
local v=k..':versions';local old=redis.call('HGET',v,ARGV[6])
local version=tonumber(ARGV[7]); local removing=ARGV[8]=='1'
if old then
 local sep=string.find(old,':');local prior=tonumber(string.sub(old,1,sep-1));local deleted=string.sub(old,sep+1)=='1'
 if version<prior or (version==prior and deleted and not removing) then return -3 end
 if version==prior and deleted==removing then return 1 end
end
local expiry=k..':version_expiry'
for _,id in ipairs(redis.call('ZRANGEBYSCORE',expiry,'-inf',now,'LIMIT',0,128)) do redis.call('HDEL',v,id);redis.call('ZREM',expiry,id) end
redis.call('HSET',v,ARGV[6],ARGV[7]..':'..ARGV[8])
redis.call('ZADD',expiry,now+604800000,ARGV[6])
redis.call('EXPIRE',v,604800)
redis.call('EXPIRE',expiry,604800)
redis.call('HINCRBY',m,'generation',1)
redis.call('EXPIRE',m,604800)
if removing then
 redis.call('ZREM',k,ARGV[6])
 -- Removing a representative requires SQL re-evaluation of aggregation.
 redis.call('HSET',m,'ready',0)
else
 if ARGV[9]~='0' and ARGV[10]=='1' then
  -- SQL rebuild preserves aggregation and its falloff rather than guessing it.
  redis.call('HSET',m,'ready',0)
 else redis.call('ZADD',k,ARGV[6],ARGV[6]) end
 redis.call('ZREMRANGEBYRANK',k,0,-513)
end
redis.call('EXPIRE',k,604800)
return 1
`

func ApplyProjection(ctx context.Context, t Timeline, p Projection) error {
	return getDefaultService().ApplyProjection(ctx, t, p)
}
func (s *FeedService) ApplyProjection(ctx context.Context, t Timeline, p Projection) error {
	if p.BatchID == 0 || p.LeaseToken == "" || p.LeaseGeneration == 0 || (!p.Invalidate && (p.ObjectVersion == 0 || p.StatusID == 0)) {
		return ErrStaleProjection
	}
	remove, aggregate, invalidate := 0, 0, 0
	if p.Invalidate {
		invalidate = 1
	}
	if p.Remove {
		remove = 1
	}
	if p.Aggregate {
		aggregate = 1
	}
	result, err := s.eval(ctx, projectionScript, []string{TimelineKey(t)}, p.Epoch, p.BatchID, p.LeaseGeneration, p.LeaseToken, p.RemainingLease.Milliseconds(), p.StatusID, p.ObjectVersion, remove, p.ReblogID, aggregate, invalidate)
	if err != nil {
		return err
	}
	if result == int64(-1) {
		return ErrNeedsRebuild
	}
	if result == int64(-3) {
		return ErrSuperseded
	}
	if result != int64(1) {
		return ErrStaleProjection
	}
	return nil
}

// ReleaseRebuild never clears another process's lease, including after expiry.
func releaseRebuildSlot(lease RebuildLease) {
	if lease.release != nil {
		lease.release.Do(func() { <-rebuildSlots })
	}
}
func ReleaseRebuild(ctx context.Context, t Timeline, lease RebuildLease) {
	defer releaseRebuildSlot(lease)
	// The Redis lease already has a TTL. Once the request budget expires,
	// release the process slot without adding another network wait to fallback.
	if ctx.Err() != nil {
		return
	}
	cleanup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _ = getDefaultService().eval(cleanup, `if redis.call('GET',KEYS[1]..':rebuild')==ARGV[1] then return redis.call('DEL',KEYS[1]..':rebuild') end return 0`, []string{TimelineKey(t)}, lease.Token)
}

type ProjectionTarget struct {
	Timeline   Timeline
	Projection Projection
}

// BatchApplyProjections pipelines independent same-slot scripts. Each target
// receives its own result; callers must not acknowledge a partly failed batch.
func BatchApplyProjections(ctx context.Context, targets []ProjectionTarget) []error {
	return getDefaultService().BatchApplyProjections(ctx, targets)
}
func (s *FeedService) BatchApplyProjections(ctx context.Context, targets []ProjectionTarget) []error {
	results := make([]error, len(targets))
	started := time.Now()
	calls := make([]*scriptCall, len(targets))
	limited, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for idx, target := range targets {
		p := target.Projection
		if p.BatchID == 0 || p.LeaseToken == "" || p.LeaseGeneration == 0 || (!p.Invalidate && (p.ObjectVersion == 0 || p.StatusID == 0)) {
			results[idx] = ErrStaleProjection
			continue
		}
		remove, aggregate, invalidate := 0, 0, 0
		if p.Invalidate {
			invalidate = 1
		}
		if p.Remove {
			remove = 1
		}
		if p.Aggregate {
			aggregate = 1
		}
		calls[idx] = &scriptCall{keys: []string{TimelineKey(target.Timeline)}, args: func() ([]interface{}, error) {
			remaining := p.RemainingLease - time.Since(started)
			if remaining.Milliseconds() <= 0 {
				return nil, ErrStaleProjection
			}
			return []interface{}{p.Epoch, p.BatchID, p.LeaseGeneration, p.LeaseToken, remaining.Milliseconds(), p.StatusID, p.ObjectVersion, remove, p.ReblogID, aggregate, invalidate}, nil
		}}
	}
	commands, pipelineErr := s.pipelineScript(limited, projectionScript, projectionScriptSHA, calls)

	for idx, cmd := range commands {
		if cmd == nil {
			if results[idx] == nil {
				if pipelineErr != nil {
					results[idx] = pipelineErr
				} else {
					results[idx] = errors.New("feed Redis evaluator returned no result")
				}
			}
			continue
		}
		v, err := cmd.Result()
		if err != nil {
			results[idx] = err
		} else if v == int64(-1) {
			results[idx] = ErrNeedsRebuild
		} else if v == int64(-3) {
			results[idx] = ErrSuperseded
		} else if v != int64(1) {
			results[idx] = ErrStaleProjection
		} else if pipelineErr != nil {
			results[idx] = pipelineErr
		}
	}
	return results
}

const prepareEpochScript = `
local k=KEYS[1];local m=k..':meta';local epoch=redis.call('HGET',m,'epoch')
if epoch and tonumber(redis.call('HGET',m,'generation')) and redis.call('HGET',k..':versions','_epoch')==epoch then return epoch end
if ARGV[1]=='' then return '' end
redis.call('HSET',m,'epoch',ARGV[1],'generation',0,'ready',0,'count',0)
redis.call('HSET',k..':versions','_epoch',ARGV[1])
redis.call('EXPIRE',k..':versions',604800)
redis.call('EXPIRE',m,604800)
redis.call('DEL',k..':rebuild')
return ARGV[1]
`

// PrepareProjectionEpochs is bootstrap only, not projection. Callers must read
// current SQL truth AFTER this returns and must never replace an old attempt's
// captured epoch with these values. Incomplete timelines remain SQL-backed.
func PrepareProjectionEpochs(ctx context.Context, timelines []Timeline) ([]string, []error) {
	return getDefaultService().projectionEpochs(ctx, timelines, true)
}
func ReadTimelineEpochs(ctx context.Context, timelines []Timeline) ([]string, []error) {
	return getDefaultService().projectionEpochs(ctx, timelines, false)
}
func (s *FeedService) projectionEpochs(ctx context.Context, timelines []Timeline, bootstrap bool) ([]string, []error) {
	epochs := make([]string, len(timelines))
	failures := make([]error, len(timelines))
	calls := make([]*scriptCall, len(timelines))
	limited, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for idx, t := range timelines {
		token := ""
		if bootstrap {
			var err error
			token, err = randomFeedToken()
			if err != nil {
				failures[idx] = err
				continue
			}
		}
		calls[idx] = &scriptCall{keys: []string{TimelineKey(t)}, args: func() ([]interface{}, error) { return []interface{}{token}, nil }}
	}
	cmds, pipelineErr := s.pipelineScript(limited, prepareEpochScript, prepareEpochScriptSHA, calls)

	for idx, cmd := range cmds {
		if cmd == nil {
			if failures[idx] == nil {
				if pipelineErr != nil {
					failures[idx] = pipelineErr
				} else {
					failures[idx] = errors.New("feed Redis evaluator returned no result")
				}
			}
			continue
		}
		value, err := cmd.Text()
		if err != nil {
			failures[idx] = err
		} else if value == "" {
			failures[idx] = ErrNeedsRebuild
		} else if pipelineErr != nil {
			failures[idx] = pipelineErr
		} else {
			epochs[idx] = value
		}
	}
	return epochs, failures
}
