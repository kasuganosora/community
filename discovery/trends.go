package discovery

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	TagTrendVersion         = "tags-v1"
	TagTrendMinAuthors      = 5
	TagTrendMaxStatuses     = 5000
	tagTrendMaxAssociations = 50000
)

// ErrTrendBudget is kept for compatibility; scans no longer fail on volume.
var ErrTrendBudget = errors.New("discovery: trend scan budget exceeded")

// ErrTrendBusy reports that the bounded scan queue or the rebuild lease is
// held by another worker. It is a transient contention signal, not a volume
// failure.
var ErrTrendBusy = errors.New("discovery: trend scan busy")

// Caps are variables only so tests can exercise the partial path cheaply.
var (
	tagTrendStatusCap      = TagTrendMaxStatuses
	tagTrendAssociationCap = tagTrendMaxAssociations
)

// SetTagTrendLimitsForTest overrides the scan caps and returns a restore func.
func SetTagTrendLimitsForTest(statuses, associations int) func() {
	ps, pa := tagTrendStatusCap, tagTrendAssociationCap
	tagTrendStatusCap, tagTrendAssociationCap = statuses, associations
	return func() { tagTrendStatusCap, tagTrendAssociationCap = ps, pa }
}

type TagHistory struct {
	Day      string `json:"day"`
	Uses     string `json:"uses"`
	Accounts string `json:"accounts"`
}

type TagTrend struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	PostsCount   int      `json:"posts_count"`
	AuthorsCount int      `json:"authors_count"`
	Growth       *float64 `json:"growth"`
	GrowthState  string   `json:"growth_state"`
	// No prior complete membership snapshot is retained, so new-entry claims
	// are deliberately not inferred from a zero activity baseline.
	NewEntry bool         `json:"new_entry"`
	History  []TagHistory `json:"history"`
	key      string
	score    float64
	last     time.Time
}

type tagAccumulator struct {
	tag      *models.Tag
	posts    map[uint64]bool
	authors  map[uint64]bool
	recent   map[uint64]bool
	previous map[uint64]bool
	days     map[int64]*tagDay
	domains  map[string]map[uint64]bool
	last     time.Time
	visible  bool
}
type tagDay struct {
	posts   map[uint64]bool
	authors map[uint64]bool
}

func trendWindow(window string) (time.Duration, bool) {
	switch window {
	case "24h":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	}
	return 0, false
}

// AggregateTagTrends is AggregateTagTrendsPartial without the partial flag.
func AggregateTagTrends(ctx context.Context, conn *gorm.DB, viewer *models.Account, window, scope string, asOf time.Time, keys []string) ([]TagTrend, error) {
	rows, _, err := AggregateTagTrendsPartial(ctx, conn, viewer, window, scope, asOf, keys)
	return rows, err
}

// AggregateTagTrendsPartial produces counts from a bounded, consistent current
// database snapshot. When the window holds more statuses (or tag
// associations) than the caps, it aggregates the newest statuses up to the
// cap and reports partial=true instead of failing. The local scope queries
// only local statuses (domain IS NULL), so federated volume never affects it.
// Viewer filtering decides inclusion; counts describe the shared public set.
func AggregateTagTrendsPartial(ctx context.Context, conn *gorm.DB, viewer *models.Account, window, scope string, asOf time.Time, keys []string) ([]TagTrend, bool, error) {
	partial := false
	duration, valid := trendWindow(window)
	if !valid || (scope != "local" && scope != "federated") {
		return nil, false, errors.New("invalid trend window or scope")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Waiting consumes the same deadline as the SQL snapshot. Brief bursts
	// can queue, but cannot create unbounded waiters or extend database work.
	release, err := tagScans.acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	defer release()
	var result []TagTrend
	err = conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Fetch at most cap+1 lightweight roots. Eligibility reloads bounded
		// chunks, including keyword and embedded-object checks, within this tx.
		query := models.RecommendationStatusScope(tx, asOf, duration).Select("statuses.*")
		if scope == "local" {
			query = query.Where("statuses.domain IS NULL")
		}
		if keys != nil {
			if len(keys) == 0 {
				result = []TagTrend{}
				return nil
			}
			query = query.Where("id IN (?)", tx.Table("status_tags st").Select("st.status_id").Joins("JOIN tags t ON t.id = st.tag_id").Where("t.name_key IN ?", keys))
		}
		var roots models.Statuses
		if err := query.Order("statuses.created_at DESC, statuses.id DESC").Limit(tagTrendStatusCap + 1).Find(&roots).Error; err != nil {
			return err
		}
		if len(roots) > tagTrendStatusCap {
			// Newest-first order: keep the most recent statuses only.
			roots = roots[:tagTrendStatusCap]
			partial = true
		}
		eligible := make(models.Statuses, 0, len(roots))
		visible := map[uint64]bool{}
		opts := models.RecommendationEligibilityOptions{Context: "public", AsOf: asOf, Window: duration, RootWindow: true, AggregateOnly: true}
		for start := 0; start < len(roots); start += 200 {
			batch, err := models.FilterRecommendableStatuses(tx, nil, roots[start:min(start+200, len(roots))], opts)
			if err != nil {
				return err
			}
			eligible = append(eligible, batch...)
			if viewer != nil {
				batch, err = models.FilterRecommendableStatuses(tx, viewer, batch, opts)
				if err != nil {
					return err
				}
			}
			for _, s := range batch {
				visible[s.Id] = true
			}
		}
		if len(eligible) == 0 {
			result = []TagTrend{}
			return nil
		}
		byID := make(map[uint64]*models.Status, len(eligible))
		ids := make([]uint64, 0, len(eligible))
		for _, s := range eligible {
			byID[s.Id] = s
			ids = append(ids, s.Id)
		}
		// Only tag associations are loaded here, not attachments/media/polls.
		type association struct {
			StatusID uint64
			TagID    uint64
			Name     string
			NameKey  string
		}
		associations := make([]association, 0)
		for start := 0; start < len(ids); start += 400 {
			var rows []association
			q := tx.Table("status_tags st").Select("st.status_id, t.id AS tag_id, t.name, t.name_key").Joins("JOIN tags t ON t.id = st.tag_id").Joins("JOIN statuses s ON s.id = st.status_id").Where("st.status_id IN ?", ids[start:min(start+400, len(ids))]).Order("s.created_at DESC, s.id DESC, t.id ASC")
			if keys != nil {
				q = q.Where("t.name_key IN ?", keys)
			}
			if err := q.Limit(tagTrendAssociationCap - len(associations) + 1).Scan(&rows).Error; err != nil {
				return err
			}
			associations = append(associations, rows...)
			if len(associations) > tagTrendAssociationCap {
				associations = associations[:tagTrendAssociationCap]
				partial = true
				break
			}
		}
		filters, err := models.GetFilters(tx, func() uint64 {
			if viewer != nil {
				return viewer.Id
			}
			return 0
		}())
		if err != nil {
			return err
		}
		acc := map[string]*tagAccumulator{}
		for _, row := range associations {
			s := byID[row.StatusID]
			if s == nil || s.CreatedAt == nil {
				continue
			}
			key := models.TagKey(row.Name)
			// A hidden tag name must not bypass public-context keyword rules
			// merely because its source's body did not spell out that hashtag.
			probe := &models.Status{Content: models.StatusContent{Content: row.Name}}
			hidden := false
			for _, f := range filters.GetStatusFiltersIn(probe, viewer, "public") {
				if f.FilterAction == models.FilterActionHide {
					hidden = true
				}
			}
			if hidden {
				continue
			}
			a := acc[key]
			if a == nil {
				a = &tagAccumulator{tag: &models.Tag{Id: row.TagID, Name: row.Name, NameKey: key}, posts: map[uint64]bool{}, authors: map[uint64]bool{}, recent: map[uint64]bool{}, previous: map[uint64]bool{}, days: map[int64]*tagDay{}, domains: map[string]map[uint64]bool{}}
				acc[key] = a
			}
			a.posts[s.Id] = true
			a.authors[s.AccountId] = true
			a.visible = a.visible || visible[s.Id]
			if s.CreatedAt.After(a.last) {
				a.last = *s.CreatedAt
			}
			if !s.CreatedAt.Before(asOf.Add(-6 * time.Hour)) {
				a.recent[s.AccountId] = true
			} else if !s.CreatedAt.Before(asOf.Add(-12 * time.Hour)) {
				a.previous[s.AccountId] = true
			}
			day := s.CreatedAt.UTC().Truncate(24 * time.Hour).Unix()
			d := a.days[day]
			if d == nil {
				d = &tagDay{posts: map[uint64]bool{}, authors: map[uint64]bool{}}
				a.days[day] = d
			}
			d.posts[s.Id] = true
			d.authors[s.AccountId] = true
			domain := "local"
			if s.Domain != nil {
				domain = *s.Domain
			}
			if a.domains[domain] == nil {
				a.domains[domain] = map[uint64]bool{}
			}
			a.domains[domain][s.AccountId] = true
		}
		result = make([]TagTrend, 0, len(acc))
		for key, a := range acc {
			if !a.visible {
				continue
			}
			result = append(result, finishTagTrend(key, a, asOf, duration))
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].score != result[j].score {
				return result[i].score > result[j].score
			}
			return result[i].key < result[j].key
		})
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	if partial {
		logrus.WithFields(logrus.Fields{"window": window, "scope": scope, "status_cap": tagTrendStatusCap, "association_cap": tagTrendAssociationCap}).Debug("discovery: tag trends computed from newest statuses only (partial)")
	}
	return result, partial, nil
}

func finishTagTrend(key string, a *tagAccumulator, asOf time.Time, duration time.Duration) TagTrend {
	t := serializer.ToTag(a.tag)
	row := TagTrend{Name: a.tag.Name, URL: t.Url, PostsCount: len(a.posts), AuthorsCount: len(a.authors), GrowthState: "no_previous", History: []TagHistory{}, key: key, last: a.last}
	// Count one contribution per author per comparison window. This is
	// stricter than an hourly cap and prevents repeated posts from boosting
	// the growth score. A domain saturates independently in the scale term.
	scale := 0.0
	for _, authors := range a.domains {
		scale += float64(len(authors)) / (10 + float64(len(authors)))
	}
	growth := math.Max(-1, math.Min(1, math.Log((float64(len(a.recent))+3)/(float64(len(a.previous))+3))))
	confidence := math.Min(1, float64(len(a.recent)+len(a.previous))/10)
	row.score = math.Log1p(scale) + 0.3*growth*confidence + 0.2*math.Exp(-asOf.Sub(a.last).Hours()/24)
	if len(a.previous) > 0 {
		row.Growth = &growth
		row.GrowthState = "steady"
		if growth > 0.1 {
			row.GrowthState = "rising"
		} else if growth < -0.1 {
			row.GrowthState = "falling"
		}
	}
	for day := asOf.UTC().Truncate(24 * time.Hour); !day.Before(asOf.Add(-duration).UTC().Truncate(24 * time.Hour)); day = day.Add(-24 * time.Hour) {
		d := a.days[day.Unix()]
		uses, authors := 0, 0
		if d != nil {
			uses = len(d.posts)
			authors = len(d.authors)
		}
		row.History = append(row.History, TagHistory{Day: strconv.FormatInt(day.Unix(), 10), Uses: strconv.Itoa(uses), Accounts: strconv.Itoa(authors)})
	}
	return row
}
