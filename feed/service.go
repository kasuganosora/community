package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
)

// FeedService 封装 feed 相关的 Redis 操作
type FeedService struct {
	redis RedisClient
}

// NewFeedService 创建 FeedService 实例
func NewFeedService(redis RedisClient) *FeedService {
	return &FeedService{redis: redis}
}

// AddToTimelineItem 将状态添加到时间轴
func (s *FeedService) AddToTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"

	ctx := context.Background()

	// 插入一个 sorted set, 用来记录这个时间线是啥时候更新的，用于判断是否要对其进行删除释放空间
	_, err = s.redis.ZAdd(ctx, "timelines", redis.Z{Score: float64(time.Now().Unix()), Member: timelineKey}).Result()
	if err != nil {
		return
	}

	if status.ReblogId != nil && *status.ReblogId > 0 && aggregateReblogs {
		// 如果是转发的，原始状态在前 REBLOG_FALLOFF 内发生的，不再插入
		rank, redisErr := s.redis.ZRevRank(ctx, timelineKey, strconv.FormatUint(*status.ReblogId, 10)).Result()
		isRankNil := false
		if redisErr != nil {
			if errors.Is(redisErr, redis.Nil) {
				isRankNil = true
			} else {
				err = redisErr
				return
			}
		}

		if !isRankNil && rank < REBLOG_FALLOFF {
			return
		}

		var isNewRebolog int64
		// 如果原始状态或其转发不在时间线的前 REBLOG_FALLOFF 个状态中，则将新的转发添加到 reblog_key 有序集合中，同时将转发状态添加到 timeline_key 有序集合中，返回
		isNewRebolog, err = s.redis.ZAddNX(ctx, reblogKey, redis.Z{Score: float64(status.Id), Member: strconv.FormatUint(*status.ReblogId, 10)}).Result()
		if err != nil {
			return
		}

		// 新的转发状态
		if isNewRebolog > 0 {
			err = s.redis.ZAdd(ctx, timelineKey, redis.Z{Score: float64(status.Id), Member: strconv.FormatUint(status.Id, 10)}).Err()
			if err != nil {
				return
			}
		} else {
			// 在同一条状态被多次连续转发的实况下，除了第一条会被记录到时间线上（feed:home:1), 和转推集合（feed:home:1:reblogs）中，其他的都会记录在 feed:home:1:reblogs:{reblogId} 中
			// 前提是这些转发都是在 REBLOG_FALLOFF 时间内发生的
			reblogSetKey := fmt.Sprintf(reblogKey+":%d", *status.ReblogId)
			err = s.redis.SAdd(ctx, reblogSetKey, strconv.FormatUint(status.Id, 10)).Err()
			if err != nil {
				return
			}
		}

		return
	}

	// 判断是否转发的状态比原始状态早，如果是，则不再插入,或者转发的状态已经存在，则不再插入
	isExists := s.redis.ZScore(ctx, reblogKey, strconv.FormatUint(status.Id, 10)).Val()
	if isExists > 0 {
		return
	}

	// 如果不是转发的，直接插入 timeline_key 有序集合中
	err = s.redis.ZAdd(ctx, timelineKey, redis.Z{Score: float64(status.Id), Member: strconv.FormatUint(status.Id, 10)}).Err()
	return
}

// RemoveTimelineItem 从时间轴移除状态
func (s *FeedService) RemoveTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"
	ctx := context.Background()

	if status.ReblogId != nil && *status.ReblogId > 0 && aggregateReblogs {
		_, redisErr := s.redis.ZRevRank(ctx, timelineKey, strconv.FormatUint(status.Id, 10)).Result()

		if redisErr != nil {
			if errors.Is(redisErr, redis.Nil) {
				return
			} else {
				err = redisErr
				return
			}
		}

		reblogSetKey := fmt.Sprintf(reblogKey+":%d", *status.ReblogId)
		if err = s.redis.SRem(ctx, reblogSetKey, strconv.FormatUint(status.Id, 10)).Err(); err != nil {
			return
		}

		if err = s.redis.ZRem(ctx, reblogKey, strconv.FormatUint(*status.ReblogId, 10)).Err(); err != nil {
			return
		}

		otherReblog := s.redis.SMembers(ctx, reblogSetKey).Val()
		var reblogIds []uint64
		for _, reblogId := range otherReblog {
			reblogIdUint, _ := strconv.ParseUint(reblogId, 10, 64)
			reblogIds = append(reblogIds, reblogIdUint)
		}

		minReblogId := utils.Min(reblogIds)

		if minReblogId > 0 {
			if err = s.redis.ZAdd(ctx, timelineKey, redis.Z{Score: float64(minReblogId), Member: strconv.FormatUint(minReblogId, 10)}).Err(); err != nil {
				return
			}

			if err = s.redis.ZAdd(ctx, reblogKey, redis.Z{Score: float64(minReblogId), Member: strconv.FormatUint(*status.ReblogId, 10)}).Err(); err != nil {
				return
			}
		} else {
			if err = s.redis.Del(ctx, reblogSetKey).Err(); err != nil {
				return
			}

			if err = s.redis.ZRem(ctx, reblogKey, strconv.FormatUint(*status.ReblogId, 10)).Err(); err != nil {
				return
			}
		}
	}

	if err = s.redis.ZRem(ctx, timelineKey, strconv.FormatUint(status.Id, 10)).Err(); err != nil {
		return
	}

	return
}

// TrimTimeline 裁剪时间轴
func (s *FeedService) TrimTimeline(timeline Timeline) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"
	ctx := context.Background()

	if err = s.redis.ZRemRangeByRank(ctx, timelineKey, 0, -(MAX_ITEMS + 1)).Err(); err != nil {
		return
	}

	falloffRange := s.redis.ZRangeWithScores(ctx, timelineKey, REBLOG_FALLOFF, REBLOG_FALLOFF).Val()

	if len(falloffRange) == 0 {
		return
	}

	rebloggedIdsStr := s.redis.ZRangeByScore(ctx, reblogKey, &redis.ZRangeBy{
		Min: "0",
		Max: strconv.FormatFloat(falloffRange[0].Score, 'f', -1, 64),
	}).Val()

	for _, rebloggedIdStr := range rebloggedIdsStr {
		rebloggedId, _ := strconv.ParseUint(rebloggedIdStr, 10, 64)
		if err = s.redis.ZRem(ctx, reblogKey, strconv.FormatUint(rebloggedId, 10)).Err(); err != nil {
			return
		}

		reblogSetKey := fmt.Sprintf(reblogKey+":%d", rebloggedId)
		if err = s.redis.Del(ctx, reblogSetKey).Err(); err != nil {
			return
		}
	}

	return
}

// CleanFeeds 清理 feed
func (s *FeedService) CleanFeeds(timeline Timeline, feedIds []string) (err error) {
	ctx := context.Background()
	timelineKeyFormat := timeline.GetKeyFormat()
	rebloggedIdSets := make(map[any]*redis.StringSliceCmd)

	_, err = s.redis.Pipelined(ctx, func(pipe redis.Pipeliner) (err error) {
		for _, feedId := range feedIds {
			timelineKey := fmt.Sprintf(timelineKeyFormat, feedId)
			reblogKey := timelineKey + ":reblogs"
			rebloggedIdSets[feedId] = pipe.ZRange(ctx, reblogKey, 0, -1)
			pipe.Del(ctx, timelineKey, reblogKey)
		}
		return
	})

	if err != nil {
		return
	}

	deleteKeys := make([]string, 0)
	for feedId, rebloggedIds := range rebloggedIdSets {
		for _, rebloggedId := range rebloggedIds.Val() {
			reblogSetKey := fmt.Sprintf(timelineKeyFormat+":reblogs:%s", feedId, rebloggedId)
			deleteKeys = append(deleteKeys, reblogSetKey)
		}
	}

	if len(deleteKeys) > 0 {
		_, err = s.redis.Del(ctx, deleteKeys...).Result()
		if err != nil {
			return
		}
	}
	return
}

// PushToClient 推送到客户端
func (s *FeedService) PushToClient(timeline Timeline, eventType string, eventPayload any) (err error) {
	msg := map[string]any{
		"type":    eventType,
		"payload": eventPayload,
	}

	msgJson, _ := json.Marshal(msg)
	key := GetRedisChannelName(timeline)
	ctx := context.Background()
	_, err = s.redis.Publish(ctx, key, msgJson).Result()
	return
}

// PushUpdateRequired 检查是否需要推送更新
func (s *FeedService) PushUpdateRequired(timeline Timeline) (ret bool) {
	key := fmt.Sprintf("subscribed:"+timeline.GetKeyFormat(), timeline.GetTimelineId())
	ctx := context.Background()
	channels, err := s.redis.PubSubChannels(ctx, key).Result()
	if err != nil {
		return false
	}

	return len(channels) > 0
}

// GcTimelines 回收过期的时间轴
func (s *FeedService) GcTimelines() (err error) {
	ctx := context.Background()
	deleteRange := time.Now().Add(-1 * time.Hour * 24 * MAX_AGE_DAYS).Unix()
	max := strconv.FormatInt(deleteRange, 10)
	timelines := s.redis.ZRevRangeByScoreWithScores(ctx, "timelines", &redis.ZRangeBy{Max: max, Min: "-inf"}).Val()

	if len(timelines) == 0 {
		return
	}

	batchSize := 1000 // 每批次删除的键的数量
	keysToDelete := make([]string, 0)

	for _, timeline := range timelines {
		key := timeline.Member.(string)
		keysToDelete = append(keysToDelete, key, key+":reblogs")

		// 获取所有匹配 key+":reblogs:*" 的键
		matchingKeys, err := s.redis.Keys(ctx, key+":reblogs:*").Result()
		if err != nil {
			return err
		}

		keysToDelete = append(keysToDelete, matchingKeys...)
	}

	// 分批删除 keysToDelete
	for i := 0; i < len(keysToDelete); i += batchSize {
		end := i + batchSize
		if end > len(keysToDelete) {
			end = len(keysToDelete)
		}

		_, err = s.redis.Pipelined(ctx, func(pipe redis.Pipeliner) (err error) {
			pipe.Unlink(ctx, keysToDelete[i:end]...)
			return
		})

		if err != nil {
			return err
		}
	}

	// 删除过期的timelines
	_, err = s.redis.ZRemRangeByScore(ctx, "timelines", "-inf", max).Result()

	return
}
