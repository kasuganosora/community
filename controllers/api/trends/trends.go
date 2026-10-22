package trends

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

func viewer(c *gin.Context) *models.Account {
	raw, _ := c.Get("account")
	account, _ := raw.(*models.Account)
	return account
}
func pagination(c *gin.Context) (int, int) {
	limit, offset := 10, 0
	if n, e := strconv.Atoi(c.Query("limit")); e == nil && n > 0 && n <= 20 {
		limit = n
	}
	if n, e := strconv.Atoi(c.Query("offset")); e == nil && n >= 0 {
		offset = min(n, 200)
	}
	return limit, offset
}
func unavailable(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Header("Retry-After", "5")
	c.AbortWithStatusJSON(503, gin.H{"error": "trends temporarily unavailable", "code": "unavailable"})
}

func enabled(c *gin.Context) bool {
	settings, err := systemsettings.ReadDiscoveryConfiguration(c.Request.Context(), db.GetDB())
	if err != nil {
		unavailable(c, err)
		return false
	}
	if !settings.TrendsEnabled {
		c.JSON(200, []any{})
		return false
	}
	return true
}

// ListTags returns real UTC calendar-day history, never cumulative counters
// disguised as a day. The compatibility API cannot label a recent-topic
// fallback, so it only includes tags supported by at least five authors.
func ListTags(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !enabled(c) {
		return
	}
	window, scope := c.DefaultQuery("window", "7d"), c.DefaultQuery("scope", "federated")
	if (window != "24h" && window != "7d") || (scope != "local" && scope != "federated") {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid window or scope", "code": "invalid_request"})
		return
	}
	limit, offset := pagination(c)
	var rows []discovery.TagTrend
	var partial bool
	var err error
	if v := viewer(c); v == nil {
		rows, partial, err = discovery.AnonymousTagTrendsPartial(c.Request.Context(), db.GetDB(), window, scope, time.Now().UTC())
	} else {
		rows, partial, err = discovery.AggregateTagTrendsPartial(c.Request.Context(), db.GetDB(), v, window, scope, time.Now().UTC(), nil)
	}
	if err != nil {
		unavailable(c, err)
		return
	}
	if partial {
		c.Header("X-Trends-Partial", "true")
	}
	result := []gin.H{}
	qualified := 0
	for _, row := range rows {
		if row.AuthorsCount < discovery.TagTrendMinAuthors {
			continue
		}
		qualified++
		if qualified <= offset {
			continue
		}
		history := row.History
		if len(history) > 7 {
			history = history[:7]
		}
		result = append(result, gin.H{"name": row.Name, "url": row.URL, "history": history})
		if len(result) == limit {
			break
		}
	}
	common.SetOffsetLinks(c, offset, len(result), limit)
	c.JSON(200, result)
}

func trendRoots(ctx context.Context, conn *gorm.DB, account *models.Account) (models.Statuses, error) {
	asOf := time.Now().UTC()
	var candidates models.Statuses
	if err := models.RecommendationStatusScope(conn, asOf, 7*24*time.Hour).Order("(reblogs_count + favourites_count) DESC, created_at DESC, id DESC").Limit(400).Find(&candidates).Error; err != nil {
		return nil, err
	}
	return models.FilterRecommendableStatuses(conn, account, candidates, models.RecommendationEligibilityOptions{Context: "public", AsOf: asOf, Window: 7 * 24 * time.Hour, RootWindow: true})
}
func ListStatuses(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !enabled(c) {
		return
	}
	limit, offset := pagination(c)
	ctx, cancel := context.WithTimeout(models.WithFilterContext(c.Request.Context(), "public"), 3*time.Second)
	defer cancel()
	statuses, err := trendRoots(ctx, db.GetDB().WithContext(ctx), viewer(c))
	if err != nil {
		unavailable(c, err)
		return
	}
	if offset > len(statuses) {
		offset = len(statuses)
	}
	statuses = statuses[offset:min(offset+limit, len(statuses))]
	result, err := serializer.ToStatusesContext(ctx, statuses, viewer(c))
	if err != nil {
		unavailable(c, err)
		return
	}
	common.SetOffsetLinks(c, offset, len(result), limit)
	c.JSON(200, result)
}

// ListLinks never trusts status_cards by itself: private/unlisted cards are
// valid previews but are not valid public trend sources. Current sources are
// authorized before even their URLs/titles enter the aggregation.
func ListLinks(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !enabled(c) {
		return
	}
	limit, offset := pagination(c)
	ctx, cancel := context.WithTimeout(models.WithFilterContext(c.Request.Context(), "public"), 3*time.Second)
	defer cancel()
	conn := db.GetDB().WithContext(ctx)
	if !conn.Migrator().HasTable(&models.StatusCard{}) {
		c.JSON(200, []any{})
		return
	}
	statuses, err := trendRoots(ctx, conn, viewer(c))
	if err != nil {
		unavailable(c, err)
		return
	}
	ids := make([]uint64, 0, len(statuses))
	byID := map[uint64]*models.Status{}
	for _, s := range statuses {
		ids = append(ids, s.Id)
		byID[s.Id] = s
	}
	if len(ids) == 0 {
		c.JSON(200, []any{})
		return
	}
	var cards []*models.StatusCard
	if err = conn.Where("status_id IN ?", ids).Order("status_id DESC").Limit(400).Find(&cards).Error; err != nil {
		unavailable(c, err)
		return
	}
	type aggregate struct {
		card    *models.StatusCard
		posts   map[uint64]bool
		authors map[uint64]bool
		days    map[int64]map[uint64]uint64
	}
	links := map[string]*aggregate{}
	for _, card := range cards {
		s := byID[card.StatusId]
		if s == nil || s.CreatedAt == nil || card.Url == "" {
			continue
		}
		a := links[card.Url]
		if a == nil {
			a = &aggregate{card: card, posts: map[uint64]bool{}, authors: map[uint64]bool{}, days: map[int64]map[uint64]uint64{}}
			links[card.Url] = a
		}
		a.posts[s.Id] = true
		a.authors[s.AccountId] = true
		day := s.CreatedAt.UTC().Truncate(24 * time.Hour).Unix()
		if a.days[day] == nil {
			a.days[day] = map[uint64]uint64{}
		}
		a.days[day][s.Id] = s.AccountId
	}
	ordered := make([]*aggregate, 0, len(links))
	for _, a := range links {
		ordered = append(ordered, a)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i].authors) != len(ordered[j].authors) {
			return len(ordered[i].authors) > len(ordered[j].authors)
		}
		return ordered[i].card.Url < ordered[j].card.Url
	})
	result := []map[string]any{}
	for i := offset; i < min(offset+limit, len(ordered)); i++ {
		a := ordered[i]
		row := a.card.APIMap()
		history := []gin.H{}
		for day := time.Now().UTC().Truncate(24 * time.Hour); len(history) < 7; day = day.Add(-24 * time.Hour) {
			authors := map[uint64]bool{}
			for _, author := range a.days[day.Unix()] {
				authors[author] = true
			}
			history = append(history, gin.H{"day": strconv.FormatInt(day.Unix(), 10), "uses": strconv.Itoa(len(a.days[day.Unix()])), "accounts": strconv.Itoa(len(authors))})
		}
		row["history"] = history
		row["published_at"] = nil
		row["language"] = nil
		row["authors"] = []any{}
		row["image_description"] = ""
		result = append(result, row)
	}
	common.SetOffsetLinks(c, offset, len(result), limit)
	c.JSON(200, result)
}
