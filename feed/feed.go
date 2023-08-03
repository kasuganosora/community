package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"strconv"
)

const (
	MAX_ITEMS      = 512
	REBLOG_FALLOFF = 40
)

var ErrFeedRegenerating = errors.New("feed is regenerating")

type Timeline interface {
	GetKeyFormat() string
	GetTimelineId() string
	Rebuild() error
}

type ListParam struct {
	MaxId   uint64
	MinId   uint64
	SinceId uint64
	Limit   uint64
	Other   map[string]any
}

func (p *ListParam) SetMaxId(id uint64) {
	p.MaxId = id
}

func (p *ListParam) SetMinId(id uint64) {
	p.MinId = id
}

func (p *ListParam) SetSinceId(id uint64) {
	p.SinceId = id
}

func (p *ListParam) SetLimit(limit uint64) {
	p.Limit = limit
}

func (p *ListParam) GetRedisRangeMinMax() (start, end string) {
	if p.MaxId > 0 {
		end = strconv.FormatUint(p.MaxId, 10)
	} else {
		end = "+inf"
	}

	if p.MinId > 0 {
		start = strconv.FormatUint(p.MinId, 10)
	} else {
		start = "-inf"
	}

	return
}

func (p *ListParam) GetRedisRangeOffsetCount() (offset, count int64) {
	if p.SinceId > 0 {
		offset = int64(p.SinceId)
	} else {
		offset = 0
	}

	if p.Limit > 0 {
		count = int64(p.SinceId + p.Limit)
	} else {
		count = -1
	}

	return
}

func GetAccountFollowersId(accountId uint64) (followers []uint64, err error) {
	followers = make([]uint64, 0)

	err = db.GetDB().Model(&models.Follow{}).Where("follow_id = ?", accountId).Pluck("account_id", &followers).Error
	return
}

func GetAccountRecentActiveLocalFollowersId(accountId uint64) (followers []uint64, err error) {
	followers, err = GetAccountFollowersId(accountId)
	if err != nil {
		return
	}

	err = db.GetDB().Model(&models.Account{}).Where("id in (?) and last_activity_at > DATE_SUB(NOW(), INTERVAL 10 DAY) and domain is null", followers).Pluck("id", &followers).Error

	return
}

func GetAccountFollowingAccounts(accountId uint64) (accounts models.Accounts, err error) {
	followingIds := make([]uint64, 0)
	err = db.GetDB().Model(models.Follows{}).Where("account_id = ?", accountId).Pluck("follow_id", &followingIds).Error
	if err != nil {
		return
	}

	accounts = make([]*models.Account, 0)
	err = db.GetDB().Where("id in (?)", followingIds).Find(&accounts).Error
	return
}

func GetAccountRemoteFollowers(accountId uint64) (followers []*models.Account, err error) {
	followersId, err := GetAccountFollowersId(accountId)
	if err != nil {
		return
	}
	followers = make([]*models.Account, 0)
	err = db.GetDB().Model(&models.Account{}).Where("id in (?) and domain is not null", followersId).Find(&followers).Error
	return
}

// AddToTimelineItem 将状态添加到时间轴
// aggregateReblogs 在时间轴中合并转推
func AddToTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"

	ctx := context.Background()
	redisConn := db.GetRedis()

	if status.ReblogId != nil && *status.ReblogId > 0 && aggregateReblogs {
		// 如果是转发的，原始状态在前 REBLOG_FALLOFF 内发生的，不再插入
		rank, redisErr := redisConn.ZRevRank(ctx, timelineKey, strconv.FormatUint(*status.ReblogId, 10)).Result()
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
		isNewRebolog, err = redisConn.ZAddNX(ctx, reblogKey, redis.Z{float64(status.Id), strconv.FormatUint(*status.ReblogId, 10)}).Result()
		if err != nil {
			return
		}

		// 新的转发状态
		if isNewRebolog > 0 {
			err = redisConn.ZAdd(ctx, timelineKey, redis.Z{float64(status.Id), strconv.FormatUint(status.Id, 10)}).Err()
			if err != nil {
				return
			}
		} else {
			// 在同一条状态被多次连续转发的实况下，除了第一条会被记录到时间线上（feed:home:1), 和转推集合（feed:home:1:reblogs）中，其他的都会记录在 feed:home:1:reblogs:{reblogId} 中
			// 前提是这些转发都是在 REBLOG_FALLOFF 时间内发生的
			reblogSetKey := fmt.Sprintf(reblogKey+":%d", *status.ReblogId)
			err = redisConn.SAdd(ctx, reblogSetKey, strconv.FormatUint(status.Id, 10)).Err()
			if err != nil {
				return
			}
		}

		return
	}

	// 判断是否转发的状态比原始状态早，如果是，则不再插入,或者转发的状态已经存在，则不再插入
	isExists := redisConn.ZScore(ctx, reblogKey, strconv.FormatUint(status.Id, 10)).Val()
	if isExists > 0 {
		return
	}

	// 如果不是转发的，直接插入 timeline_key 有序集合中
	err = redisConn.ZAdd(ctx, timelineKey, redis.Z{float64(status.Id), strconv.FormatUint(status.Id, 10)}).Err()
	return
}

func RemoveTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"
	ctx := context.Background()
	redisConn := db.GetRedis()

	if status.ReblogId != nil && *status.ReblogId > 0 && aggregateReblogs {
		_, redisErr := redisConn.ZRevRank(ctx, timelineKey, strconv.FormatUint(status.Id, 10)).Result()

		if redisErr != nil {
			if errors.Is(redisErr, redis.Nil) {
				return
			} else {
				err = redisErr
				return
			}
		}

		reblogSetKey := fmt.Sprintf(reblogKey+":%d", *status.ReblogId)
		if err = redisConn.SRem(ctx, reblogSetKey, strconv.FormatUint(status.Id, 10)).Err(); err != nil {
			return
		}

		if err = redisConn.ZRem(ctx, reblogKey, strconv.FormatUint(*status.ReblogId, 10)).Err(); err != nil {
			return
		}

		otherReblog := redisConn.SMembers(ctx, reblogSetKey).Val()
		var reblogIds []uint64
		for _, reblogId := range otherReblog {
			reblogIdUint, _ := strconv.ParseUint(reblogId, 10, 64)
			reblogIds = append(reblogIds, reblogIdUint)
		}

		minReblogId := utils.Min(reblogIds)

		if minReblogId > 0 {
			if err = redisConn.ZAdd(ctx, timelineKey, redis.Z{float64(minReblogId), strconv.FormatUint(minReblogId, 10)}).Err(); err != nil {
				return
			}

			if err = redisConn.ZAdd(ctx, reblogKey, redis.Z{float64(minReblogId), strconv.FormatUint(*status.ReblogId, 10)}).Err(); err != nil {
				return
			}
		} else {
			if err = redisConn.Del(ctx, reblogSetKey).Err(); err != nil {
				return
			}

			if err = redisConn.ZRem(ctx, reblogKey, strconv.FormatUint(*status.ReblogId, 10)).Err(); err != nil {
				return
			}
		}
	}

	if err = redisConn.ZRem(ctx, timelineKey, strconv.FormatUint(status.Id, 10)).Err(); err != nil {
		return
	}

	return
}

func TrimTimeline(timeline Timeline) (err error) {
	timelineKeyFormat := timeline.GetKeyFormat()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timeline.GetTimelineId())
	reblogKey := timelineKey + ":reblogs"
	ctx := context.Background()
	redisConn := db.GetRedis()

	if err = redisConn.ZRemRangeByRank(ctx, timelineKey, 0, -(MAX_ITEMS + 1)).Err(); err != nil {
		return
	}

	falloffRange := redisConn.ZRangeWithScores(ctx, timelineKey, REBLOG_FALLOFF, REBLOG_FALLOFF).Val()

	if len(falloffRange) == 0 {
		return
	}

	rebloggedIdsStr := redisConn.ZRangeByScore(ctx, reblogKey, &redis.ZRangeBy{
		Min: "0",
		Max: strconv.FormatFloat(falloffRange[0].Score, 'f', -1, 64),
	}).Val()

	for _, rebloggedIdStr := range rebloggedIdsStr {
		rebloggedId, _ := strconv.ParseUint(rebloggedIdStr, 10, 64)
		if err = redisConn.ZRem(ctx, reblogKey, strconv.FormatUint(rebloggedId, 10)).Err(); err != nil {
			return
		}

		reblogSetKey := fmt.Sprintf(reblogKey+":%d", rebloggedId)
		if err = redisConn.Del(ctx, reblogSetKey).Err(); err != nil {
			return
		}
	}

	return
}

func CleanFeeds(timeline Timeline, feedIds []string) (err error) {
	ctx := context.Background()
	redisConn := db.GetRedis()
	timelineKeyFormat := timeline.GetKeyFormat()
	rebloggedIdSets := make(map[any]*redis.StringSliceCmd)

	_, err = redisConn.Pipelined(ctx, func(pipe redis.Pipeliner) (err error) {
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
		_, err = redisConn.Del(ctx, deleteKeys...).Result()
		if err != nil {
			return
		}
	}
	return
}

func PushToClient(timeline Timeline, eventType string, eventPayload any) (err error) {
	msg := map[string]any{
		"type":    eventType,
		"payload": eventPayload,
	}

	msgJson, _ := json.Marshal(msg)
	redisConn := db.GetRedis()
	key := GetRedisChannelName(timeline)
	ctx := context.Background()
	_, err = redisConn.Publish(ctx, key, msgJson).Result()
	return
}

func PushUpdateRequired(timeline Timeline) (ret bool) {
	key := fmt.Sprintf("subscribed:"+timeline.GetKeyFormat(), timeline.GetTimelineId())
	ctx := context.Background()
	channels, err := db.GetRedis().PubSubChannels(ctx, key).Result()
	if err != nil {
		return false
	}

	return len(channels) > 0
}

func GetRedisChannelName(timeline Timeline) string {
	return fmt.Sprintf("subscribed:"+timeline.GetKeyFormat(), timeline.GetTimelineId())
}
