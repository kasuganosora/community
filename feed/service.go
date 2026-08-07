package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
)

// 共享的 context，避免重复创建.
var sharedCtx = context.Background()

// 订阅状态缓存（使用 sync.Map 优化并发性能）.
type subscriptionCache struct {
	cache sync.Map
	ttl   time.Duration
}

var subCache = &subscriptionCache{
	ttl: 5 * time.Second,
}

func (sc *subscriptionCache) Get(key string) (bool, bool) {
	if val, ok := sc.cache.Load(key); ok {
		entry := val.(*subCacheEntry) //nolint:forcetypeassert
		if time.Now().Before(entry.expires) {
			return entry.val, true
		}

		sc.cache.Delete(key)
	}

	return false, false
}

func (sc *subscriptionCache) Set(key string, val bool) {
	entry := &subCacheEntry{
		val:     val,
		expires: time.Now().Add(sc.ttl),
	}
	sc.cache.Store(key, entry)
}

type subCacheEntry struct {
	val     bool
	expires time.Time
}

// FeedService 封装 feed 相关的 Redis 操作.
type FeedService struct {
	redis RedisClient
}

// NewFeedService 创建 FeedService 实例.
func NewFeedService(redis RedisClient) *FeedService {
	return &FeedService{redis: redis}
}

// AddToTimelineItem 将状态添加到时间轴.
func (s *FeedService) AddToTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"
	statusIdStr := strconv.FormatUint(status.Id, 10)

	// 插入一个 sorted set, 用来记录这个时间线是啥时候更新的，用于判断是否要对其进行删除释放空间
	_, err = s.redis.ZAdd(sharedCtx, "timelines", redis.Z{Score: float64(time.Now().Unix()), Member: timelineKey}).Result()
	if err != nil {
		return err
	}

	if status.ReblogId != nil && *status.ReblogId > 0 && aggregateReblogs {
		// 如果是转发的，原始状态在前 REBLOG_FALLOFF 内发生的，不再插入
		reblogIdStr := strconv.FormatUint(*status.ReblogId, 10)
		rank, redisErr := s.redis.ZRevRank(sharedCtx, timelineKey, reblogIdStr).Result()
		isRankNil := false

		if redisErr != nil {
			if errors.Is(redisErr, redis.Nil) {
				isRankNil = true
			} else {
				err = redisErr

				return err
			}
		}

		if !isRankNil && rank < REBLOG_FALLOFF {
			return err
		}

		var isNewReblog int64
		// 如果原始状态或其转发不在时间线的前 REBLOG_FALLOFF 个状态中，则将新的转发添加到 reblog_key 有序集合中，同时将转发状态添加到 timeline_key 有序集合中，返回
		isNewReblog, err = s.redis.ZAddNX(sharedCtx, reblogKey, redis.Z{Score: float64(status.Id), Member: reblogIdStr}).Result()
		if err != nil {
			return err
		}

		// 新的转发状态
		if isNewReblog > 0 {
			err = s.redis.ZAdd(sharedCtx, timelineKey, redis.Z{Score: float64(status.Id), Member: statusIdStr}).Err()
			if err != nil {
				return err
			}
		} else {
			// 在同一条状态被多次连续转发的实况下，除了第一条会被记录到时间线上（feed:home:1), 和转推集合（feed:home:1:reblogs）中，其他的都会记录在 feed:home:1:reblogs:{reblogId} 中
			// 前提是这些转发都是在 REBLOG_FALLOFF 时间内发生的
			reblogSetKey := fmt.Sprintf(reblogKey+":%d", *status.ReblogId)

			err = s.redis.SAdd(sharedCtx, reblogSetKey, statusIdStr).Err()
			if err != nil {
				return err
			}
		}

		return err
	}

	// 判断是否转发的状态比原始状态早，如果是，则不再插入,或者转发的状态已经存在，则不再插入
	score, scoreErr := s.redis.ZScore(sharedCtx, reblogKey, statusIdStr).Result()
	if scoreErr != nil && !errors.Is(scoreErr, redis.Nil) {
		return scoreErr
	}

	if score > 0 {
		return err
	}

	// 如果不是转发的，直接插入 timeline_key 有序集合中
	err = s.redis.ZAdd(sharedCtx, timelineKey, redis.Z{Score: float64(status.Id), Member: statusIdStr}).Err()

	return err
}

// RemoveTimelineItem 从时间轴移除状态.
func (s *FeedService) RemoveTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"
	statusIdStr := strconv.FormatUint(status.Id, 10)

	if status.ReblogId != nil && *status.ReblogId > 0 && aggregateReblogs {
		_, redisErr := s.redis.ZRevRank(sharedCtx, timelineKey, statusIdStr).Result()
		if redisErr != nil {
			if errors.Is(redisErr, redis.Nil) {
				return err
			} else {
				err = redisErr

				return err
			}
		}

		reblogIdStr := strconv.FormatUint(*status.ReblogId, 10)
		reblogSetKey := fmt.Sprintf(reblogKey+":%d", *status.ReblogId)

		// 使用 Pipeline 合并多个操作
		_, err = s.redis.Pipelined(sharedCtx, func(pipe redis.Pipeliner) error {
			pipe.SRem(sharedCtx, reblogSetKey, statusIdStr)
			pipe.ZRem(sharedCtx, reblogKey, reblogIdStr)

			return nil
		})
		if err != nil {
			return err
		}

		// 获取剩余的转发
		otherReblog, err := s.redis.SMembers(sharedCtx, reblogSetKey).Result()
		if err != nil {
			return err
		}

		var reblogIds []uint64

		for _, rid := range otherReblog {
			reblogIdUint, _ := strconv.ParseUint(rid, 10, 64)
			reblogIds = append(reblogIds, reblogIdUint)
		}

		minReblogId := utils.Min(reblogIds)

		if minReblogId > 0 {
			minReblogIdStr := strconv.FormatUint(minReblogId, 10)
			_, err = s.redis.Pipelined(sharedCtx, func(pipe redis.Pipeliner) error {
				pipe.ZAdd(sharedCtx, timelineKey, redis.Z{Score: float64(minReblogId), Member: minReblogIdStr})
				pipe.ZAdd(sharedCtx, reblogKey, redis.Z{Score: float64(minReblogId), Member: reblogIdStr})

				return nil
			})
		} else {
			_, err = s.redis.Pipelined(sharedCtx, func(pipe redis.Pipeliner) error {
				pipe.Del(sharedCtx, reblogSetKey)
				pipe.ZRem(sharedCtx, reblogKey, reblogIdStr)

				return nil
			})
		}

		if err != nil {
			return err
		}
	}

	err = s.redis.ZRem(sharedCtx, timelineKey, statusIdStr).Err()

	return err
}

// TrimTimeline 裁剪时间轴.
func (s *FeedService) TrimTimeline(timeline Timeline) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"

	if err = s.redis.ZRemRangeByRank(sharedCtx, timelineKey, 0, -(MAX_ITEMS + 1)).Err(); err != nil {
		return err
	}

	// 获取第 REBLOG_FALLOFF 个（从新到旧）元素，作为 reblog falloff 阈值
	// 使用 ZRevRangeWithScores 从新到旧排列，获取第 REBLOG_FALLOFF 个元素
	falloffRange, err := s.redis.ZRevRangeWithScores(sharedCtx, timelineKey, int64(REBLOG_FALLOFF), int64(REBLOG_FALLOFF)).Result()
	if err != nil {
		return err
	}

	if len(falloffRange) == 0 {
		return err
	}

	rebloggedIdsStr, err := s.redis.ZRangeByScore(sharedCtx, reblogKey, &redis.ZRangeBy{
		Min: "0",
		Max: strconv.FormatFloat(falloffRange[0].Score, 'f', -1, 64),
	}).Result()
	if err != nil {
		return err
	}

	if len(rebloggedIdsStr) == 0 {
		return err
	}

	// 使用 Pipeline 批量删除，避免 N+1 问题
	_, err = s.redis.Pipelined(sharedCtx, func(pipe redis.Pipeliner) error {
		for _, rebloggedIdStr := range rebloggedIdsStr {
			pipe.ZRem(sharedCtx, reblogKey, rebloggedIdStr)
			reblogSetKey := reblogKey + ":" + rebloggedIdStr
			pipe.Del(sharedCtx, reblogSetKey)
		}

		return nil
	})

	return err
}

// CleanFeeds 清理 feed.
func (s *FeedService) CleanFeeds(timeline Timeline, feedIds []string) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	rebloggedIdSets := make(map[any]*redis.StringSliceCmd)

	_, err = s.redis.Pipelined(sharedCtx, func(pipe redis.Pipeliner) (err error) {
		for _, feedId := range feedIds {
			timelineKey := fmt.Sprintf(timelineKeyFormat, feedId)
			reblogKey := timelineKey + ":reblogs"
			rebloggedIdSets[feedId] = pipe.ZRange(sharedCtx, reblogKey, 0, -1)
			pipe.Del(sharedCtx, timelineKey, reblogKey)
		}

		return
	})
	if err != nil {
		return err
	}

	deleteKeys := make([]string, 0)

	for feedId, rebloggedIds := range rebloggedIdSets {
		for _, rebloggedId := range rebloggedIds.Val() {
			reblogSetKey := fmt.Sprintf(timelineKeyFormat+":reblogs:%s", feedId, rebloggedId)
			deleteKeys = append(deleteKeys, reblogSetKey)
		}
	}

	if len(deleteKeys) > 0 {
		_, err = s.redis.Del(sharedCtx, deleteKeys...).Result()
		if err != nil {
			return err
		}
	}

	return err
}

// PushToClient 推送到客户端.
func (s *FeedService) PushToClient(timeline Timeline, eventType string, eventPayload any) (err error) {
	msg := map[string]any{
		"type":    eventType,
		"payload": eventPayload,
	}

	msgJson, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	key := GetRedisChannelName(timeline)
	_, err = s.redis.Publish(sharedCtx, key, msgJson).Result()

	return
}

// PushUpdateRequired 检查是否需要推送更新（带缓存）.
func (s *FeedService) PushUpdateRequired(timeline Timeline) (ret bool) {
	key := fmt.Sprintf("subscribed:"+timeline.GetKeyFormat(), timeline.GetTimelineId())

	// 先检查缓存
	if val, ok := subCache.Get(key); ok {
		return val
	}

	channels, err := s.redis.PubSubChannels(sharedCtx, key).Result()
	if err != nil {
		// Redis 错误时不缓存结果，避免临时故障导致持续丢失推送
		return false
	}

	result := len(channels) > 0
	// 只在成功查询时缓存结果
	subCache.Set(key, result)

	return result
}

// BatchAddToTimelineItems 批量向多个时间线添加状态（使用 Pipeline 优化）.
func (s *FeedService) BatchAddToTimelineItems(timelines []Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	if len(timelines) == 0 {
		return nil
	}

	// 对于单个时间线，直接使用单次操作
	if len(timelines) == 1 {
		return s.AddToTimelineItem(timelines[0], status, aggregateReblogs)
	}

	// 如果需要处理 reblog 聚合，使用逐个处理的方式（保证正确性）
	if aggregateReblogs && status.ReblogId != nil && *status.ReblogId > 0 {
		for _, timeline := range timelines {
			err = s.AddToTimelineItem(timeline, status, true)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// 使用 Pipeline 批量操作（适用于非 reblog 或不需要聚合的情况）
	nowScore := float64(status.Id)
	now := float64(time.Now().Unix())
	statusIdStr := strconv.FormatUint(status.Id, 10)

	_, err = s.redis.Pipelined(sharedCtx, func(pipe redis.Pipeliner) error {
		for _, timeline := range timelines {
			timelineKey := fmt.Sprintf(timeline.GetKeyFormat(), timeline.GetTimelineId())

			// 记录时间线更新
			pipe.ZAdd(sharedCtx, "timelines", redis.Z{Score: now, Member: timelineKey})

			// 如果不是转发的，或者不需要聚合，直接添加到时间线
			if status.ReblogId == nil || *status.ReblogId == 0 || !aggregateReblogs {
				pipe.ZAdd(sharedCtx, timelineKey, redis.Z{Score: nowScore, Member: statusIdStr})
			}
		}

		return nil
	})

	return err
}

// GcTimelines 回收过期的时间轴.
func (s *FeedService) GcTimelines() (err error) {
	deleteRange := time.Now().Add(-1 * time.Hour * 24 * MAX_AGE_DAYS).Unix()
	max := strconv.FormatInt(deleteRange, 10)
	timelines, err := s.redis.ZRevRangeByScoreWithScores(sharedCtx, "timelines", &redis.ZRangeBy{Max: max, Min: "-inf"}).Result()
	if err != nil {
		return err
	}

	if len(timelines) == 0 {
		return err
	}

	batchSize := 1000 // 每批次删除的键的数量
	keysToDelete := make([]string, 0)

	for _, timeline := range timelines {
		key := timeline.Member.(string) //nolint:forcetypeassert
		keysToDelete = append(keysToDelete, key, key+":reblogs")

		// 使用 SCAN 替代 KEYS（非阻塞）
		var cursor uint64

		pattern := key + ":reblogs:*"

		for {
			var scanKeys []string

			scanCmd := s.redis.Scan(sharedCtx, cursor, pattern, 100)

			scanKeys, cursor = scanCmd.Val()

			if scanCmd.Err() != nil {
				return scanCmd.Err()
			}

			keysToDelete = append(keysToDelete, scanKeys...)

			if cursor == 0 {
				break
			}
		}
	}

	// 分批删除 keysToDelete
	for i := 0; i < len(keysToDelete); i += batchSize {
		end := min(i+batchSize, len(keysToDelete))

		// 优先使用 Unlink（非阻塞），如果失败则回退到 Del
		_, err = s.redis.Pipelined(sharedCtx, func(pipe redis.Pipeliner) (err error) {
			pipe.Unlink(sharedCtx, keysToDelete[i:end]...)

			return
		})
		if err != nil {
			// Unlink 失败（可能是旧版 Redis），尝试使用 Del
			_, err = s.redis.Del(sharedCtx, keysToDelete[i:end]...).Result()
			if err != nil {
				return err
			}
		}
	}

	// 删除过期的timelines
	_, err = s.redis.ZRemRangeByScore(sharedCtx, "timelines", "-inf", max).Result()

	return err
}
