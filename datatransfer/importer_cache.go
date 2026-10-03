package datatransfer

import (
	"context"
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
	clients := redisdb.GetRedises()
	if len(clients) == 0 {
		return nil
	}
	affected, err := i.cacheSelector(ctx, conn)
	if err != nil {
		return err
	}
	for _, client := range clients {
		// Honor the post-commit deadline even when the application's pooled
		// Redis client was configured to ignore context deadlines for I/O.
		options := *client.Options()
		options.ContextTimeoutEnabled = true
		bounded := redis.NewClient(&options)
		err := invalidateImportedKeys(ctx, bounded, affected)
		_ = bounded.Close()
		if err != nil {
			return err
		}
	}
	return nil
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
				tags[strings.TrimPrefix(tag, "#")] = true
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
		last := key[strings.LastIndex(key, ":")+1:]
		switch {
		case strings.HasPrefix(key, "feed:home:"), strings.HasPrefix(key, "feed:mention:"):
			return accounts[last]
		case strings.HasPrefix(key, "feed:public:"):
			return public
		case strings.HasPrefix(key, "feed:list:v2:"):
			return lists[last]
		case strings.HasPrefix(key, "feed:hashtag:"):
			return tags[last]
		}
		return false
	}, nil
}
func invalidateImportedKeys(ctx context.Context, client *redis.Client, affected func(string) bool) error {
	// Collect before mutating so SCAN's cursor isn't disrupted by our deletion.
	keys := map[string]bool{}
	var cursor uint64
	for {
		page, next, err := client.Scan(ctx, cursor, "feed:*", 128).Result()
		if err != nil {
			return err
		}
		for _, key := range page {
			key = strings.TrimSuffix(key, ":empty")
			if at := strings.Index(key, ":reblogs"); at >= 0 {
				key = key[:at]
			}
			known := false
			for _, prefix := range []string{"feed:home:", "feed:mention:", "feed:public:", "feed:list:v2:", "feed:hashtag:"} {
				if strings.HasPrefix(key, prefix) {
					known = true
					break
				}
			}
			if known && affected(key) {
				keys[key] = true
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	service := feed.NewFeedService(feed.NewRedisClient(client))
	for key := range keys {
		if err := service.InvalidateTimelineContext(ctx, importedTimelineKey(key)); err != nil {
			return err
		}
	}
	return nil
}
