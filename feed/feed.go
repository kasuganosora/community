package feed

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

const (
	MAX_ITEMS      = 512
	REBLOG_FALLOFF = 40
	MAX_AGE_DAYS   = 7
)

var ErrFeedRegenerating = errors.New("feed is regenerating")

// defaultService 是默认的 FeedService 实例.
var defaultService *FeedService
var defaultServiceOnce sync.Once
var defaultServiceOverride bool

// getDefaultService 获取默认的 FeedService 实例（延迟初始化）.
func getDefaultService() *FeedService {
	if defaultServiceOverride {
		return defaultService
	}

	defaultServiceOnce.Do(func() {
		if defaultService == nil {
			defaultService = NewFeedService(NewRedisClient(db.GetUniversalRedis()))
		}
	})

	return defaultService
}

// SetDefaultService 设置默认的 FeedService 实例（用于测试）.
func SetDefaultService(service *FeedService) {
	defaultService = service
	defaultServiceOverride = true
}

// defaultDBService 是默认的 DBService 实例.
var defaultDBService *DBService
var defaultDBServiceOnce sync.Once
var defaultDBServiceOverride bool

// getDefaultDBService 获取默认的 DBService 实例（延迟初始化）.
func getDefaultDBService() *DBService {
	if defaultDBServiceOverride {
		return defaultDBService
	}

	defaultDBServiceOnce.Do(func() {
		if defaultDBService == nil {
			defaultDBService = NewDBService(db.GetDB())
		}
	})

	return defaultDBService
}

// SetDefaultDBService 设置默认的 DBService 实例（用于测试）.
func SetDefaultDBService(service *DBService) {
	defaultDBService = service
	defaultDBServiceOverride = true
}

type Timeline interface {
	GetKeyFormat() string
	GetTimelineId() string
	Rebuild() error
}

type ListParam struct {
	Context context.Context
	Viewer  *models.Account
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
		end = "(" + strconv.FormatUint(p.MaxId, 10)
	} else {
		end = "+inf"
	}

	lower := max(p.MinId, p.SinceId)
	if lower > 0 {
		start = "(" + strconv.FormatUint(lower, 10)
	} else {
		start = "-inf"
	}

	return
}

func (p *ListParam) GetRedisRangeOffsetCount() (offset, count int64) {
	// ZRevRange uses rank-based offsets (0=newest), not score-based
	offset = 0

	if p.Limit > 0 {
		count = offset + int64(p.Limit) - 1 //nolint:gosec // G115: reviewed and safe
	} else {
		count = -1
	}

	return
}

// DBClient 定义数据库操作接口.
type DBClient interface {
	Model(value any) *gorm.DB
	Where(query any, args ...any) *gorm.DB
	Find(out any, where ...any) *gorm.DB
	Preload(query string, args ...any) *gorm.DB
	Order(value string) *gorm.DB
	Limit(limit int) *gorm.DB
	Offset(offset int) *gorm.DB
	Association(column string) *gorm.Association
}

// GetAccountFollowersId 获取账号的粉丝 ID 列表（向后兼容）.
func GetAccountFollowersId(accountId uint64) (followers []uint64, err error) {
	return getDefaultDBService().GetAccountFollowersId(accountId)
}

// GetAccountRecentActiveLocalFollowersId 获取账号最近活跃的本地粉丝 ID 列表（向后兼容）.
func GetAccountRecentActiveLocalFollowersId(accountId uint64) (followers []uint64, err error) {
	return getDefaultDBService().GetAccountRecentActiveLocalFollowersId(accountId)
}

// GetAccountFollowingAccounts 获取账号关注的所有账号（向后兼容）.
func GetAccountFollowingAccounts(accountId uint64) (accounts models.Accounts, err error) {
	return getDefaultDBService().GetAccountFollowingAccounts(accountId)
}

// GetAccountRemoteFollowers 获取账号的远程粉丝（向后兼容）.
func GetAccountRemoteFollowers(accountId uint64) (followers []*models.Account, err error) {
	return getDefaultDBService().GetAccountRemoteFollowers(accountId)
}

// 以下函数委托给 defaultService，保持向后兼容

func AddToTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	return getDefaultService().AddToTimelineItem(timeline, status, aggregateReblogs)
}

func BatchAddToTimelineItems(timelines []Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	return getDefaultService().BatchAddToTimelineItems(timelines, status, aggregateReblogs)
}

func RemoveTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	return getDefaultService().RemoveTimelineItem(timeline, status, aggregateReblogs)
}

func TrimTimeline(timeline Timeline) (err error) {
	return getDefaultService().TrimTimeline(timeline)
}

func CleanFeeds(timeline Timeline, feedIds []string) (err error) {
	return getDefaultService().CleanFeeds(timeline, feedIds)
}

func PushToClient(timeline Timeline, eventType string, eventPayload any) (err error) {
	return getDefaultService().PushToClient(timeline, eventType, eventPayload)
}

func PushUpdateRequired(timeline Timeline) (ret bool) {
	return getDefaultService().PushUpdateRequired(timeline)
}

func GetRedisChannelName(timeline Timeline) string {
	return fmt.Sprintf("subscribed:"+timeline.GetKeyFormat(), timeline.GetTimelineId())
}

func GcTimelines() (err error) {
	return getDefaultService().GcTimelines()
}

// ResetSubscriptionCache 重置订阅缓存（用于测试）.
func ResetSubscriptionCache() {
	subCache = &subscriptionCache{ttl: 5 * time.Second}
}
