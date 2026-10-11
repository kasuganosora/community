package datatransfer

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type importedTimelineKey string

func (k importedTimelineKey) GetKeyFormat() string  { return "%s" }
func (k importedTimelineKey) GetTimelineId() string { return string(k) }
func (k importedTimelineKey) Rebuild() error        { return nil }

// Invalidate existing caches only: historical data never becomes an event
// broadcast. Permission decisions remain request-local in the feed reader.
func (i *importer) invalidateCaches(ctx context.Context, conn *gorm.DB) error {
	if len(redisdb.GetRedises()) == 0 && !redisdb.HasUniversalRedis() {
		return nil
	}
	affected, err := i.cacheSelector(ctx, conn)
	if err != nil {
		return err
	}
	return invalidateImportedKeys(ctx, redisdb.GetUniversalRedis(), affected)
}
func (i *importer) cacheSelector(ctx context.Context, conn *gorm.DB) (func(string) bool, error) {
	if !i.personal() {
		return func(string) bool { return true }, nil
	}
	accounts := map[string]bool{strconv.FormatUint(i.options.OwnerAccountID, 10): true}
	authors := []uint64{}
	tags := map[string]bool{}
	public := false
	for _, p := range i.bundle.Posts {
		if i.ids["post"][p.ID] == 0 {
			continue
		}
		if id := i.ids["account"][p.AuthorID]; id != 0 {
			authors = append(authors, id)
		}
		for _, sourceID := range p.RecipientIDs {
			if id := i.ids["account"][sourceID]; id != 0 {
				accounts[strconv.FormatUint(id, 10)] = true
			}
		}
		if p.Visibility == "public" {
			public = true
			for _, tag := range p.Tags {
				tags[models.TagKey(tag)] = true
			}
		}
	}
	lists := map[string]bool{}
	for _, id := range i.ids["list"] {
		lists[strconv.FormatUint(id, 10)] = true
	}
	if len(authors) > 0 {
		var followers []uint64
		if err := conn.WithContext(ctx).Model(&models.Follow{}).Where("follow_id IN ? AND status = ?", authors, models.FollowStatusFollowed).Pluck("account_id", &followers).Error; err != nil {
			return nil, err
		}
		for _, id := range followers {
			accounts[strconv.FormatUint(id, 10)] = true
		}
		var ids []uint64
		if err := conn.WithContext(ctx).Model(&models.ListAccount{}).Where("account_id IN ?", authors).Distinct().Pluck("list_id", &ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			lists[strconv.FormatUint(id, 10)] = true
		}
	}
	return func(key string) bool {
		if strings.HasPrefix(key, "feed:v3:{") && strings.HasSuffix(key, "}:items") {
			body := strings.TrimSuffix(strings.TrimPrefix(key, "feed:v3:{"), "}:items")
			decoded, err := url.PathUnescape(body)
			if err != nil {
				return false
			}
			key = "feed:" + decoded
		}
		last := key[strings.LastIndex(key, ":")+1:]
		switch {
		case strings.HasPrefix(key, "feed:home:"), strings.HasPrefix(key, "feed:mention:"):
			return accounts[last]
		case strings.HasPrefix(key, "feed:public:"):
			return public
		case strings.HasPrefix(key, "feed:list:"):
			return lists[last]
		case strings.HasPrefix(key, "feed:hashtag:"):
			return tags[models.TagKey(last)]
		}
		return false
	}, nil
}
func invalidateImportedKeys(ctx context.Context, client redis.UniversalClient, affected func(string) bool) error {
	service := feed.NewFeedService(feed.NewRedisClient(client))
	return feed.ScanTimelineMetadata(ctx, client, "feed:v3:{*}:items:meta", func(meta string) error {
		key := strings.TrimSuffix(meta, ":meta")
		if !affected(key) {
			return nil
		}
		return service.InvalidateTimelineContext(ctx, importedTimelineKey(key))
	})
}
