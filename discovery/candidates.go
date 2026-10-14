package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	feedPoolKey          = "community:discovery:{feed-pool}:current"
	feedPoolLeaseKey     = "community:discovery:{feed-pool}:lease"
	feedPoolMaximumBytes = 4 << 20
)

type candidatePool struct {
	Generation        string        `json:"generation"`
	AsOf              time.Time     `json:"as_of"`
	Mode              string        `json:"mode"`
	Candidates        []Candidate   `json:"candidates"`
	Lists             []ContentList `json:"lists"`
	Truncated         bool          `json:"truncated"`
	PolicyVersion     string        `json:"policy_version"`
	ContentDataCutoff time.Time     `json:"content_data_cutoff,omitempty"`
}

var publishFeedPoolScript = redis.NewScript(`
if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
redis.call('SET',KEYS[2],ARGV[2],'PX',ARGV[3])
redis.call('DEL',KEYS[1])
return 1`)
var releaseFeedLeaseScript = redis.NewScript(`if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`)
var acquireRemotePreferenceScript = redis.NewScript(`
if redis.call('EXISTS',KEYS[1])==1 or redis.call('EXISTS',KEYS[2])==1 then return 0 end
redis.call('SET',KEYS[1],ARGV[1],'PX',25000)
redis.call('SET',KEYS[2],ARGV[1],'PX',120000)
return 1`)

func refreshFeedRemotePreferences(parent context.Context, refresh func(context.Context) error) error {
	client := feedRedis()
	if client == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	token, err := feedGeneration()
	if err != nil {
		return err
	}
	const lease = "community:discovery:{remote-preferences}:lease"
	const cadence = "community:discovery:{remote-preferences}:cadence"
	acquired, err := acquireRemotePreferenceScript.Run(ctx, client, []string{lease, cadence}, token).Int()
	if err != nil {
		return err
	}
	if acquired != 1 {
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = releaseFeedLeaseScript.Run(cleanup, client, []string{lease}, token).Err()
	}()
	return refresh(ctx)
}

// StartBackground does not claim Feed delivery work or share its ACK/lease keys.
// A failed refresh leaves the preceding pool's original hard expiry unchanged.
func StartBackground(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			config := feedConfig()
			trendsEnabled := config.TrendsEnabled == nil || config.TrendsEnabled()
			if (Enabled() || trendsEnabled) && config.RefreshRemotePreferences != nil {
				if err := refreshFeedRemotePreferences(ctx, config.RefreshRemotePreferences); err != nil && !errors.Is(err, context.Canceled) {
					logrus.WithError(err).Warn("discovery remote preference refresh failed")
				}
			}
			if Enabled() {
				if err := RefreshFeedCandidates(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logrus.WithError(err).Warn("discovery candidate refresh failed")
				}
			}
			if trendsEnabled {
				if err := RefreshTagTrends(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logrus.WithError(err).Warn("discovery topic refresh failed")
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func RefreshFeedCandidates(parent context.Context) (refreshErr error) {
	defer func() {
		outcome := "success"
		if refreshErr != nil {
			outcome = "error"
		}
		feedRefreshOutcome.WithLabelValues(outcome).Inc()
	}()
	if !Enabled() {
		return nil
	}
	client := feedRedis()
	if client == nil {
		return ErrUnavailable
	}
	token, err := feedGeneration()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	owned, err := client.SetNX(ctx, feedPoolLeaseKey, token, 40*time.Second).Result()
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = releaseFeedLeaseScript.Run(cleanup, client, []string{feedPoolLeaseKey}, token).Err()
	}()
	conn := db.GetDB().WithContext(ctx)
	policy, err := feedPolicy(ctx)
	if err != nil {
		return err
	}
	asOf := time.Now().UTC()
	var statuses models.Statuses
	// A hard row cap bounds work even on a small deployment without specialized
	// recommendation indexes. This is a recent candidate sample, not all posts.
	if err := models.RecommendationStatusScope(conn, asOf, CandidateWindow).Order("statuses.created_at DESC, statuses.id DESC").Limit(2001).Find(&statuses).Error; err != nil {
		return err
	}
	truncated := len(statuses) > 2000
	if truncated {
		statuses = statuses[:2000]
	}
	eligible := make(models.Statuses, 0, len(statuses))
	for offset := 0; offset < len(statuses); offset += CandidateLimit {
		end := offset + CandidateLimit
		if end > len(statuses) {
			end = len(statuses)
		}
		part, err := models.FilterRecommendableStatuses(conn, nil, statuses[offset:end], models.RecommendationEligibilityOptions{Context: "public", AsOf: asOf, Window: CandidateWindow, RootWindow: true, MetadataOnly: true})
		if err != nil {
			return err
		}
		eligible = append(eligible, part...)
	}
	candidates, err := candidateMetadata(conn, eligible)
	if err != nil {
		return err
	}
	pool := candidatePool{Generation: token, AsOf: asOf, Mode: "rules", Candidates: candidates, Truncated: truncated, Lists: []ContentList{}, PolicyVersion: policy}
	buckets := feedActiveBuckets(candidates)
	feedRuntime.RLock()
	provider := feedRuntime.provider
	feedRuntime.RUnlock()
	if provider != nil && len(buckets) > 0 {
		batch, providerErr := provider.BuildBuckets(ctx, asOf, buckets)
		if providerErr == nil && (batch.Mode == "meili_lexical" || batch.Mode == "meili_hybrid") && !batch.DataCutoff.IsZero() && !batch.DataCutoff.After(time.Now()) && time.Since(batch.DataCutoff) < PoolMaxAge {
			pool.Mode = batch.Mode
			pool.ContentDataCutoff = batch.DataCutoff
			allowed := map[string]bool{}
			counts := map[string]int{}
			for _, bucket := range buckets {
				allowed[bucket.Key] = true
			}
			for _, list := range batch.Lists {
				if !allowed[list.BucketKey] || counts[list.BucketKey] >= 3 || list.Family == "" {
					continue
				}
				if len(list.IDs) > 80 {
					list.IDs = list.IDs[:80]
				}
				list.Generation = batch.Generation
				pool.Lists = append(pool.Lists, list)
				counts[list.BucketKey]++
			}
		} else if providerErr != nil {
			logrus.WithError(providerErr).Warn("discovery content provider unavailable; publishing rules candidates")
		}
	}
	if !Enabled() {
		return nil
	}
	currentPolicy, err := feedPolicy(ctx)
	if err != nil {
		return err
	}
	if currentPolicy != policy {
		return ErrUnavailable
	}
	data, err := json.Marshal(pool)
	if err != nil {
		return err
	}
	if len(data) > feedPoolMaximumBytes {
		return ErrUnavailable
	}
	// Effective lifetime is measured from as_of, never completion time.
	ttl := time.Until(asOf.Add(PoolMaxAge))
	if ttl <= 0 {
		return ErrUnavailable
	}
	published, err := publishFeedPoolScript.Run(ctx, client, []string{feedPoolLeaseKey, feedPoolKey}, token, string(data), ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if published != 1 {
		return ErrUnavailable
	}
	feedCandidateRows.Set(float64(len(candidates)))
	if truncated {
		feedPoolTruncation.Inc()
	}
	return nil
}

func feedActiveBuckets(candidates []Candidate) []ContentBucket {
	counts := map[string]int{}
	for _, candidate := range candidates {
		for _, tag := range candidate.Tags {
			counts[tag]++
		}
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] == counts[keys[j]] {
			return keys[i] < keys[j]
		}
		return counts[keys[i]] > counts[keys[j]]
	})
	if len(keys) > 32 {
		keys = keys[:32]
	}
	buckets := make([]ContentBucket, 0, len(keys))
	for _, key := range keys {
		buckets = append(buckets, ContentBucket{Key: key, Query: key})
	}
	return buckets
}

func loadFeedPool(ctx context.Context, now time.Time) (candidatePool, error) {
	var pool candidatePool
	client := feedRedis()
	if client == nil {
		return pool, ErrUnavailable
	}
	data, err := client.Get(ctx, feedPoolKey).Bytes()
	if err != nil || len(data) > feedPoolMaximumBytes {
		return pool, ErrUnavailable
	}
	if json.Unmarshal(data, &pool) != nil || pool.AsOf.After(now) || now.Sub(pool.AsOf) >= PoolMaxAge || len(pool.Candidates) > 2000 || len(pool.Lists) > 128*3 {
		return pool, ErrUnavailable
	}
	if pool.Mode != "rules" && (pool.ContentDataCutoff.IsZero() || pool.ContentDataCutoff.After(now) || now.Sub(pool.ContentDataCutoff) >= PoolMaxAge) {
		pool.Mode = "rules"
		pool.Lists = nil
	}
	return pool, nil
}

func candidateMetadata(conn *gorm.DB, statuses models.Statuses) ([]Candidate, error) {
	ids := make([]uint64, 0, len(statuses))
	for _, status := range statuses {
		ids = append(ids, status.Id)
	}
	if len(ids) == 0 {
		return []Candidate{}, nil
	}
	var tagRows []struct {
		StatusID uint64
		NameKey  string
	}
	if err := conn.Table("status_tags").Select("status_tags.status_id,tags.name_key").Joins("JOIN tags ON tags.id = status_tags.tag_id").Where("status_tags.status_id IN ?", ids).Order("status_tags.status_id, tags.name_key").Limit(len(ids)*16 + 1).Scan(&tagRows).Error; err != nil {
		return nil, err
	}
	tags := map[uint64][]string{}
	for _, row := range tagRows {
		if len(tags[row.StatusID]) < 16 {
			tags[row.StatusID] = append(tags[row.StatusID], row.NameKey)
		}
	}
	// This bounded observed engagement feature never uses remote counters.
	// UNION deduplicates actor/status pairs across favourites, reactions, boosts.
	var interactions []struct {
		StatusID  uint64
		AccountID uint64
		Domain    string
		Bot       bool
		CreatedAt time.Time
	}
	query := `SELECT activity.status_id,activity.account_id,COALESCE(accounts.domain,'') AS domain,accounts.bot,accounts.created_at
FROM (SELECT status_id,account_id FROM favorite_statuses WHERE status_id IN ?
UNION SELECT status_id,account_id FROM status_reactions WHERE status_id IN ?
UNION SELECT reblog_id AS status_id,account_id FROM statuses WHERE reblog_id IN ? AND visibility = ? AND deleted_at IS NULL AND scheduled_at IS NULL) activity
JOIN accounts ON accounts.id = activity.account_id AND accounts.is_ban = ? AND accounts.deleted_at IS NULL
WHERE accounts.domain IS NOT NULL OR EXISTS (SELECT 1 FROM users WHERE users.id=accounts.user_id AND users.is_ban=? AND users.status=? AND users.deleted_at IS NULL)
ORDER BY activity.status_id DESC, activity.account_id LIMIT 20000`
	if err := conn.Raw(query, ids, ids, ids, models.StatusVisibilityPublic, false, false, models.UserStatusNormal).Scan(&interactions).Error; err != nil {
		return nil, err
	}
	heat := map[uint64]float64{}
	domainContribution := map[string]float64{}
	for _, row := range interactions {
		weight := 1.0
		if row.Bot {
			weight = .2
		}
		if !row.CreatedAt.IsZero() && time.Since(row.CreatedAt) < 7*24*time.Hour {
			weight *= .5
		}
		key := strconv.FormatUint(row.StatusID, 10) + ":" + row.Domain
		if domainContribution[key] >= 8 {
			continue
		}
		weight = math.Min(weight, 8-domainContribution[key])
		domainContribution[key] += weight
		heat[row.StatusID] += weight
	}
	candidates := make([]Candidate, 0, len(statuses))
	for _, status := range statuses {
		if status.CreatedAt == nil || status.Account == nil {
			continue
		}
		domain := ""
		if status.Account.Domain != nil {
			domain = *status.Account.Domain
		}
		count := heat[status.Id]
		candidates = append(candidates, Candidate{StatusID: status.Id, AuthorID: status.AccountId, Domain: domain, CreatedAt: *status.CreatedAt, Language: status.Language, Tags: tags[status.Id], Heat: count / (8 + count), Explore: count <= 2})
	}
	return candidates, nil
}

func feedFollowedTopics(conn *gorm.DB, accountID uint64) ([]string, error) {
	var tags []string
	err := conn.Table("followed_tags").Select("tags.name_key").Joins("JOIN tags ON tags.id = followed_tags.tag_id").Where("followed_tags.account_id = ?", accountID).Order("followed_tags.created_at DESC, followed_tags.id DESC").Limit(4).Scan(&tags).Error
	return tags, err
}

// Explicit follows take priority. Remaining slots may use the reader's recent
// public participation; neither bookmarks nor private/Matrix activity enter it.
// This bounded signal query only runs after opting in to personalized mode.
func feedInterestTopics(conn *gorm.DB, viewer *models.Account, asOf time.Time) ([]string, error) {
	topics, err := feedFollowedTopics(conn, viewer.Id)
	if err != nil || len(topics) >= 4 {
		return topics, err
	}
	var ids []uint64
	query := `SELECT status_id FROM (
SELECT status_id,created_at FROM favorite_statuses WHERE account_id=? AND created_at>=? AND created_at<?
UNION ALL SELECT status_id,created_at FROM status_reactions WHERE account_id=? AND created_at>=? AND created_at<?
) observed GROUP BY status_id ORDER BY MAX(created_at) DESC,status_id DESC LIMIT 100`
	start := asOf.Add(-30 * 24 * time.Hour)
	if err := conn.Raw(query, viewer.Id, start, asOf, viewer.Id, start, asOf).Scan(&ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return topics, nil
	}
	stubs := make(models.Statuses, 0, len(ids))
	for _, id := range ids {
		stubs = append(stubs, &models.Status{Id: id})
	}
	statuses, err := models.FilterRecommendableStatuses(conn, viewer, stubs, models.RecommendationEligibilityOptions{Context: "home", AsOf: asOf, Window: CandidateWindow, RootWindow: false})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	existing := map[string]bool{}
	for _, topic := range topics {
		existing[topic] = true
	}
	for _, status := range statuses {
		for _, tag := range status.Tags {
			if tag != nil {
				key := models.TagKey(tag.Name)
				if !existing[key] {
					counts[key]++
				}
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] == counts[keys[j]] {
			return keys[i] < keys[j]
		}
		return counts[keys[i]] > counts[keys[j]]
	})
	for _, key := range keys {
		if len(topics) >= 4 {
			break
		}
		topics = append(topics, key)
	}
	return topics, nil
}

func feedSocialIDs(conn *gorm.DB, viewer *models.Account, asOf time.Time) ([]uint64, error) {
	var followed []uint64
	if err := conn.Table("follows").Select("follows.follow_id").Joins("JOIN accounts ON accounts.id = follows.follow_id").Where("follows.account_id = ? AND follows.status = ? AND accounts.hide_collections = ? AND accounts.is_ban = ? AND accounts.deleted_at IS NULL", viewer.Id, models.FollowStatusFollowed, false, false).Order("follows.id DESC").Limit(200).Scan(&followed).Error; err != nil {
		return nil, err
	}
	if len(followed) == 0 {
		return []uint64{}, nil
	}
	stubs := make([]*models.Account, 0, len(followed))
	for _, id := range followed {
		stubs = append(stubs, &models.Account{Id: id})
	}
	current, err := models.FilterRecommendableAccounts(conn, viewer, stubs, false)
	if err != nil {
		return nil, err
	}
	followed = followed[:0]
	for _, account := range current {
		if !account.HideCollections && (account.Domain == nil || account.RemoteHideCollections != nil && !*account.RemoteHideCollections) {
			followed = append(followed, account.Id)
		}
	}
	if len(followed) == 0 {
		return []uint64{}, nil
	}
	var ids []uint64
	err = conn.Table("statuses").Select("reblog_id").Where("account_id IN ? AND reblog_id IS NOT NULL AND reblog_id > 0 AND visibility = ? AND deleted_at IS NULL AND scheduled_at IS NULL AND created_at >= ? AND created_at < ?", followed, models.StatusVisibilityPublic, asOf.Add(-CandidateWindow), asOf).Order("created_at DESC, id DESC").Limit(100).Scan(&ids).Error
	return ids, err
}

func selectFeedCandidates(ctx context.Context, viewer *models.Account, personalized bool, pool candidatePool, readingLanguages ...[]string) ([]Candidate, error) {
	conn := db.GetDB().WithContext(ctx)
	var languages []string
	if len(readingLanguages) > 0 {
		languages = readingLanguages[0]
	}
	byID := make(map[uint64]Candidate, len(pool.Candidates))
	for _, candidate := range pool.Candidates {
		byID[candidate.StatusID] = candidate
	}
	sources := map[uint64][]Source{}
	interestIDs := []uint64{}
	socialIDs := []uint64{}
	if personalized {
		topics, err := feedInterestTopics(conn, viewer, pool.AsOf)
		if err != nil {
			return nil, err
		}
		for _, topic := range topics {
			lists := 0
			for _, list := range pool.Lists {
				if list.BucketKey != topic || lists >= 3 {
					continue
				}
				lists++
				for index, id := range list.IDs {
					if index >= 80 {
						break
					}
					sources[id] = append(sources[id], Source{Family: list.Family, Key: topic, Rank: index + 1, Generation: list.Generation})
				}
			}
			// The exact SQL path also covers long-tail topics not in the shared
			// bounded bucket roster; arbitrary user input creates no Redis keys.
			if lists == 0 {
				var ids []uint64
				err := models.RecommendationStatusScope(conn, pool.AsOf, CandidateWindow).Select("statuses.id").Joins("JOIN status_tags ON status_tags.status_id = statuses.id").Joins("JOIN tags ON tags.id = status_tags.tag_id").Where("tags.name_key = ?", topic).Order("statuses.created_at DESC, statuses.id DESC").Limit(80).Scan(&ids).Error
				if err != nil {
					return nil, err
				}
				for index, id := range ids {
					sources[id] = append(sources[id], Source{Family: "exact", Key: topic, Rank: index + 1, Generation: pool.Generation})
				}
			}
		}
		for id := range sources {
			interestIDs = append(interestIDs, id)
		}
		sort.Slice(interestIDs, func(i, j int) bool {
			a, b := ReciprocalRankFusion(sources[interestIDs[i]]), ReciprocalRankFusion(sources[interestIDs[j]])
			if a == b {
				return interestIDs[i] > interestIDs[j]
			}
			return a > b
		})
		if len(interestIDs) > 150 {
			interestIDs = interestIDs[:150]
		}
		var socialErr error
		socialIDs, socialErr = feedSocialIDs(conn, viewer, pool.AsOf)
		if socialErr != nil {
			return nil, socialErr
		}
	}
	selected := make([]uint64, 0, CandidateLimit)
	seen := map[uint64]bool{}
	add := func(ids []uint64, limit int) {
		if len(ids) > limit {
			ids = ids[:limit]
		}
		for _, id := range ids {
			if id > 0 && !seen[id] && len(selected) < CandidateLimit {
				selected = append(selected, id)
				seen[id] = true
			}
		}
	}
	add(interestIDs, 150)
	add(socialIDs, 100)
	hot := append([]Candidate(nil), pool.Candidates...)
	sort.Slice(hot, func(i, j int) bool {
		if hot[i].Heat == hot[j].Heat {
			if hot[i].CreatedAt.Equal(hot[j].CreatedAt) {
				return hot[i].StatusID > hot[j].StatusID
			}
			return hot[i].CreatedAt.After(hot[j].CreatedAt)
		}
		return hot[i].Heat > hot[j].Heat
	})
	hotIDs := []uint64{}
	exploreIDs := []uint64{}
	for _, candidate := range hot {
		if len(hotIDs) < 100 && models.RecommendationLanguageAllowed(candidate.Language, languages) {
			hotIDs = append(hotIDs, candidate.StatusID)
		}
	}
	for _, candidate := range pool.Candidates {
		if candidate.Explore && len(exploreIDs) < 50 && models.RecommendationLanguageAllowed(candidate.Language, languages) {
			exploreIDs = append(exploreIDs, candidate.StatusID)
		}
	}
	add(hotIDs, 100)
	add(exploreIDs, 50)
	if len(selected) == 0 {
		return []Candidate{}, nil
	}
	stubs := make(models.Statuses, 0, len(selected))
	for _, id := range selected {
		stubs = append(stubs, &models.Status{Id: id})
	}
	eligible, err := models.FilterRecommendableStatuses(conn, viewer, stubs, models.RecommendationEligibilityOptions{Context: "home", AsOf: pool.AsOf, Window: CandidateWindow, RootWindow: true, MetadataOnly: true})
	if err != nil {
		return nil, err
	}
	// Current SQL supplies features as well as eligibility; a stale shared pool
	// never makes an old author or removed tag authoritative for ranking.
	current, err := candidateMetadata(conn, eligible)
	if err != nil {
		return nil, err
	}
	filtered := current[:0]
	for _, candidate := range current {
		if models.RecommendationLanguageAllowed(candidate.Language, languages) {
			filtered = append(filtered, candidate)
		}
	}
	current = filtered
	social := map[uint64]bool{}
	for _, id := range socialIDs {
		social[id] = true
	}
	exploration := map[uint64]bool{}
	for _, id := range exploreIDs {
		exploration[id] = true
	}
	for index := range current {
		candidate := &current[index]
		candidate.Explore = candidate.Explore && exploration[candidate.StatusID]
		candidate.Sources = sources[candidate.StatusID]
		if len(candidate.Sources) > 12 {
			candidate.Sources = candidate.Sources[:12]
		}
		candidate.Interest = ReciprocalRankFusion(candidate.Sources)
		if social[candidate.StatusID] {
			candidate.Social = 1
		}
	}
	return current, nil
}
