package feed

import (
	"context"
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type FilterCondition func(tx *gorm.DB) (query *gorm.DB, err error)
type AfterFilter func(statuses models.Statuses) models.Statuses

func GetStatusTimelineContent(timeline Timeline, params *ListParam, newFilterCondition FilterCondition, afterFilter AfterFilter) (statuses models.Statuses, err error) {
	statusesId := make([]uint64, 0)

	// 判断是否有缓存, 没有缓存则重建
	redisConn := db.GetRedis()
	ctx := context.Background()
	timelineKey := fmt.Sprintf(timeline.GetKeyFormat(), timeline.GetTimelineId())

	if redisConn.Exists(ctx, timelineKey).Val() == 0 {
		err = timeline.Rebuild()
		if err != nil {
			return statuses, err
		}
	}

	if params == nil {
		// 从缓存中取出timeline status id
		var statusesIdStr []string

		statusesIdStr, err = redisConn.ZRangeArgs(ctx, redis.ZRangeArgs{Key: timelineKey, Start: 0, Stop: -1, Rev: true}).Result()
		if err != nil {
			return statuses, err
		}

		statusesId = utils.StringNumberArrayConvertUint64(statusesIdStr)
	} else {
		var statusesIdStr []string

		if params.MaxId > 0 || params.MinId > 0 || params.SinceId > 0 {
			min, max := params.GetRedisRangeMinMax()
			statusesIdStr, err = redisConn.ZRangeArgs(ctx, redis.ZRangeArgs{Key: timelineKey, Start: min, Stop: max, ByScore: true, Rev: true}).Result()
			if err != nil {
				return statuses, err
			}
		} else {
			start, end := params.GetRedisRangeOffsetCount()
			statusesIdStr, err = redisConn.ZRangeArgs(ctx, redis.ZRangeArgs{Key: timelineKey, Start: start, Stop: end, Rev: true}).Result()
			if err != nil {
				return statuses, err
			}
		}

		statusesId = utils.StringNumberArrayConvertUint64(statusesIdStr)
	}

	// 如果这时候一个都没有，就肯能存在以下情况：
	// 1. 真的什么没有
	// 2. 可能翻页的时候超过 redis 缓存的范围了（MAX_ITEMS) , 例如 redis 里 只有id 为 1-100 的数据，但是你要翻到 101-200 的数据
	// 还有种可能是 redis找出来的结果未能完全覆盖，例如 redis 里只有 1-100 的数据，但是你要翻到 1-200 的数据 (这种不支持，让用户/客户端调整参数/max_id/min_id/since_id)
	// 因为她可能获取了一小段数据，但是不知道数据库里还有更多的数据，所以这种情况下，让其多拉取1一次 如果下次为空的话就会跑到逻辑 2 里

	if len(statusesId) == 0 {
		// 如果是第一页，那么就是真的没有数据
		if params == nil || (params.MaxId == 0 && params.MinId == 0 && params.SinceId == 0) {
			return statuses, err
		}

		// 如果不是一页就是超过redis缓存的范围了，这时候就需要从数据库里取数据了
		query := db.GetDB().Model(&models.Status{})

		query, err = newFilterCondition(query)
		if err != nil {
			return statuses, err
		}

		if params.MaxId > 0 {
			query = query.Where("id <= ?", params.MaxId)
		}

		if params.MinId > 0 {
			query = query.Where("id >= ?", params.MinId)
		}

		if params.SinceId > 0 {
			query = query.Where("id > ?", params.SinceId)
		}

		// 按 ID 倒序排列，与 Redis 缓存中的顺序一致
		query = query.Order("id desc")

		if params.Limit > 0 {
			query = query.Limit(int(params.Limit)) //nolint:gosec // G115: reviewed and safe
		}

		err = query.Pluck("statuses.id", &statusesId).Error
		if err != nil {
			return statuses, err
		}
	}

	// 还是没有找到，返回空
	if len(statusesId) == 0 {
		return statuses, err
	}

	if params != nil && params.Limit > 0 && len(statusesId) > int(params.Limit) { //nolint:gosec // G115: reviewed and safe
		statusesId = statusesId[:params.Limit]
	}

	statuses = make([]*models.Status, 0)
	query := db.GetDB().Model(&models.Status{}).Preload("Account").
		Preload("Account.Extra").
		Preload("Content").
		Preload("Application").
		Preload("Attachments").
		Preload("ReplyAccount").
		Preload("Emojis").
		Preload("ToAccounts").
		Preload("Poll").
		Preload("Poll.Options").
		Preload("Tags").
		Preload("Reblog").
		Preload("Reblog.Account").
		Preload("Reblog.Account.Extra").
		Preload("Reblog.Content").
		Preload("Reblog.Application").
		Preload("Reblog.Attachments").
		Preload("Reblog.ReplyAccount").
		Preload("Reblog.Emojis").
		Preload("Reblog.ToAccounts").
		Preload("Reblog.Poll").
		Preload("Reblog.Poll.Options").
		Preload("Reblog.Tags")

	err = query.Where("id in (?)", statusesId).Order("statuses.id desc").Find(&statuses).Error
	if err != nil {
		return statuses, err
	}

	// 如果有设置后置过滤器，那么就过滤一下
	if afterFilter != nil {
		statuses = afterFilter(statuses)
	}

	for _, status := range statuses {
		status.SetAllExtendFieldsLoaded()
	}

	return statuses, err
}
