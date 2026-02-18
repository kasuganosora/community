package feed

import (
	"fmt"
	"strconv"
	"sync"

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

// defaultService 是默认的 FeedService 实例
var defaultService *FeedService
var defaultServiceOnce sync.Once
var defaultServiceOverride bool

// getDefaultService 获取默认的 FeedService 实例（延迟初始化）
func getDefaultService() *FeedService {
	if defaultServiceOverride {
		return defaultService
	}
	defaultServiceOnce.Do(func() {
		if defaultService == nil {
			defaultService = NewFeedService(NewRedisClient(db.GetRedis()))
		}
	})
	return defaultService
}

// SetDefaultService 设置默认的 FeedService 实例（用于测试）
func SetDefaultService(service *FeedService) {
	defaultService = service
	defaultServiceOverride = true
}

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

// DBClient 定义数据库操作接口
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

// 以下函数保持向后兼容，使用默认的 db.GetDB()

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

// 以下函数委托给 defaultService，保持向后兼容

func AddToTimelineItem(timeline Timeline, status *models.Status, aggregateReblogs bool) (err error) {
	return getDefaultService().AddToTimelineItem(timeline, status, aggregateReblogs)
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
