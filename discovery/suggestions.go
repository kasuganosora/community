package discovery

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/kasuganosora/community/defaultavatar"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// suggestionCandidateLimit caps accounts scored for one suggestion page.
// Default avatars are removed before this cap so they cannot crowd out
// accounts that have a real upload.
const suggestionCandidateLimit = 400

type AccountSuggestion struct {
	Account *models.Account
	Source  string
}

// SuggestOptions carries request context used only for ranking.
type SuggestOptions struct {
	AcceptLanguage string
}

// SuggestAccounts uses at most 400 candidates and 2,000 public social edges.
// Generic/off never read the viewer's graph for ranking. Relationship reads
// in the final eligibility check only exclude follows and pending requests.
// Accounts without a custom avatar are removed before that cap and skipped
// again while the page is filled, so a default avatar does not leave a hole.
// Same-language candidates rank first (exact, then the zh/ja CJK group),
// then everything else; primary languages come from the account_languages
// cache with a bounded inline refresh.
func SuggestAccounts(ctx context.Context, conn *gorm.DB, viewer *models.Account, limit int, opts ...SuggestOptions) ([]AccountSuggestion, error) {
	var opt SuggestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	result := make([]AccountSuggestion, 0)
	if viewer == nil {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn = conn.WithContext(ctx)
	settings, err := systemsettings.ReadDiscoveryConfiguration(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return result, nil
	}

	pref, err := models.GetRecommendationPreference(conn, viewer.UserId)
	if err != nil {
		return nil, err
	}
	if pref.Preference == models.RecommendationOff {
		return result, nil
	}
	limit = max(1, min(limit, 40))
	scores := map[uint64]float64{}
	sources := map[uint64]string{}
	if pref.Preference == models.RecommendationPersonalized {
		var intermediates []uint64
		err = conn.Table("follows f").Select("f.follow_id").Joins("JOIN accounts a ON a.id = f.follow_id").Where("f.account_id = ? AND f.status = ?", viewer.Id, models.FollowStatusFollowed).Where("a.hide_collections = ? AND a.is_ban = ? AND a.deleted_at IS NULL", false, false).Order("f.id DESC").Limit(100).Scan(&intermediates).Error
		if err != nil {
			return nil, err
		}
		if len(intermediates) > 0 {
			intermediatesModels := make([]*models.Account, 0, len(intermediates))
			for _, id := range intermediates {
				intermediatesModels = append(intermediatesModels, &models.Account{Id: id})
			}
			publicIntermediates, e := models.FilterRecommendableAccounts(conn, viewer, intermediatesModels, false)
			if e != nil {
				return nil, e
			}
			intermediates = intermediates[:0]
			for _, a := range publicIntermediates {
				if !a.HideCollections && (a.Domain == nil || a.RemoteHideCollections != nil && !*a.RemoteHideCollections) {
					intermediates = append(intermediates, a.Id)
				}
			}

			var edges []struct {
				AccountID uint64
				FollowID  uint64
			}
			if err = conn.Table("follows").Select("account_id,follow_id").Where("account_id IN ? AND status = ?", intermediates, models.FollowStatusFollowed).Order("id DESC").Limit(2000).Scan(&edges).Error; err != nil {
				return nil, err
			}
			seen := map[[2]uint64]bool{}
			for _, edge := range edges {
				pair := [2]uint64{edge.AccountID, edge.FollowID}
				if !seen[pair] {
					scores[edge.FollowID]++
					sources[edge.FollowID] = "past_interactions"
					seen[pair] = true
				}
			}
		}
	}
	for id, value := range scores {
		scores[id] = value / (3 + value)
	}
	// Public recent activity supplies a bounded cold-start pool, without
	// trusting a remote server's self-reported total follower count.
	// Placeholder avatars are left out of this pool; the assembly step
	// checks again so a full page can be filled from later candidates.
	var recent []*models.Account
	if err = customAvatarQuery(conn).Order("last_status_at IS NULL, last_status_at DESC, id DESC").Limit(suggestionCandidateLimit).Find(&recent).Error; err != nil {
		return nil, err
	}
	for _, a := range recent {
		if _, exists := scores[a.Id]; !exists {
			scores[a.Id] = 0
			sources[a.Id] = "global"
		}
	}
	ids := make([]uint64, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		return ids[i] > ids[j]
	})
	ids, err = retainCustomAvatars(conn, ids, scores, sources, len(recent) >= suggestionCandidateLimit)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return result, nil
	}
	// Popularity is observed here: distinct recently active local users
	// whose accepted follows are public, restricted to the candidate set.
	var support []struct {
		AccountID uint64
		FollowID  uint64
	}
	if err = conn.Table("follows f").Select("f.account_id, f.follow_id").Joins("JOIN accounts a ON a.id = f.account_id").Joins("JOIN users u ON u.id = a.user_id").Where("f.follow_id IN ? AND f.status = ?", ids, models.FollowStatusFollowed).Where("a.domain IS NULL AND a.deleted_at IS NULL AND a.is_ban = ? AND a.hide_collections = ? AND u.deleted_at IS NULL AND u.status = ? AND u.is_ban = ?", false, false, models.UserStatusNormal, false).Where("a.feed_last_seen_at >= ?", time.Now().Add(-30*24*time.Hour)).Order("f.id DESC").Limit(2000).Scan(&support).Error; err != nil {
		return nil, err
	}
	supporters := map[uint64]map[uint64]bool{}
	for _, edge := range support {
		if supporters[edge.FollowID] == nil {
			supporters[edge.FollowID] = map[uint64]bool{}
		}
		supporters[edge.FollowID][edge.AccountID] = true
	}
	// A bounded set of recent public favourites supplements follows. Private
	// posts and bookmarks never enter this weak, shared popularity signal.
	asOf := time.Now().UTC()
	var publicRoots models.Statuses
	if err = models.RecommendationStatusScope(conn, asOf, 30*24*time.Hour).Where("account_id IN ?", ids).Order("created_at DESC, id DESC").Limit(400).Find(&publicRoots).Error; err != nil {
		return nil, err
	}
	publicRoots, err = models.FilterRecommendableStatuses(conn, nil, publicRoots, models.RecommendationEligibilityOptions{Context: "public", AsOf: asOf, Window: 30 * 24 * time.Hour, RootWindow: true})
	if err != nil {
		return nil, err
	}
	publicIDs := make([]uint64, 0, len(publicRoots))
	rootAuthors := map[uint64]uint64{}
	for _, root := range publicRoots {
		publicIDs = append(publicIDs, root.Id)
		rootAuthors[root.Id] = root.AccountId
	}
	if len(publicIDs) > 0 {
		var likes []struct {
			AccountID uint64
			StatusID  uint64
		}
		if err = conn.Table("favorite_statuses f").Select("f.account_id,f.status_id").Joins("JOIN accounts a ON a.id=f.account_id").Joins("JOIN users u ON u.id=a.user_id").Where("f.status_id IN ? AND f.created_at >= ?", publicIDs, asOf.Add(-30*24*time.Hour)).Where("a.domain IS NULL AND a.deleted_at IS NULL AND a.is_ban = ? AND u.deleted_at IS NULL AND u.is_ban = ? AND u.status = ?", false, false, models.UserStatusNormal).Where("a.feed_last_seen_at >= ?", asOf.Add(-30*24*time.Hour)).Order("f.id DESC").Limit(2000).Scan(&likes).Error; err != nil {
			return nil, err
		}
		for _, like := range likes {
			author := rootAuthors[like.StatusID]
			if author == like.AccountID {
				continue
			}
			if supporters[author] == nil {
				supporters[author] = map[uint64]bool{}
			}
			supporters[author][like.AccountID] = true
		}
	}
	for id, unique := range supporters {
		n := float64(len(unique))
		scores[id] += n / (5 + n)
	}

	var candidates []*models.Account
	if err = conn.Where("id IN ?", ids).Find(&candidates).Error; err != nil {
		return nil, err
	}
	candidates, err = models.FilterRecommendableAccounts(conn, viewer, candidates, true)
	if err != nil {
		return nil, err
	}
	selectedIDs := make([]uint64, 0, len(candidates))
	byCurrent := map[uint64]*models.Account{}
	for _, a := range candidates {
		selectedIDs = append(selectedIDs, a.Id)
		byCurrent[a.Id] = a
	}
	if len(selectedIDs) > 0 {
		var extras []*models.AccountExtra
		if err = conn.Where("account_id IN ?", selectedIDs).Find(&extras).Error; err != nil {
			return nil, err
		}
		for _, extra := range extras {
			if a := byCurrent[extra.AccountId]; a != nil {
				a.Extra = extra
			}
		}
	}

	viewerLangs := viewerLanguages(conn, viewer, opt.AcceptLanguage)
	tiers := map[uint64]int{}
	if len(viewerLangs) > 0 && len(selectedIDs) > 0 {
		langs, e := models.AccountLanguages(conn, selectedIDs, 100)
		if e != nil {
			return nil, e
		}
		for _, id := range selectedIDs {
			tiers[id] = languageTier(langs[id], viewerLangs)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if tiers[a.Id] != tiers[b.Id] {
			return tiers[a.Id] < tiers[b.Id]
		}
		if scores[a.Id] != scores[b.Id] {
			return scores[a.Id] > scores[b.Id]
		}
		if a.LastStatusAt != nil && b.LastStatusAt != nil && !a.LastStatusAt.Equal(*b.LastStatusAt) {
			return a.LastStatusAt.After(*b.LastStatusAt)
		}
		return a.Id > b.Id
	})
	baseURL := systemsettings.GetBaseURL()
	domains := map[string]int{}
	for _, a := range candidates {
		if !defaultavatar.HasCustom(a.Avatar, baseURL) {
			continue
		}
		domain := ""
		if a.Domain != nil {
			domain = *a.Domain
		}
		if domain != "" && domains[domain] >= max(2, (limit*2)/5) {
			continue
		}
		domains[domain]++
		result = append(result, AccountSuggestion{Account: a, Source: sources[a.Id]})
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

// customAvatarQuery is the cold-start pool. It excludes the stored forms of
// a missing avatar, the legacy default asset, and a generated identicon URL.
// HasCustom remains the exact check: this predicate only keeps those rows
// from using up the candidate cap.
func customAvatarQuery(conn *gorm.DB) *gorm.DB {
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	q := conn.Model(&models.Account{}).Where("discoverable = ? AND is_ban = ?", true, false).
		Where("TRIM(avatar) <> ''").
		Where("avatar <> ?", "/default-assets/avatar.png").
		Where("avatar NOT LIKE ?", "%/api/v1/avatars/default/"+strings.Repeat("_", 64)+".png")
	if base != "" {
		q = q.Where("avatar <> ?", base+"/default-assets/avatar.png")
	}
	return q
}

// retainCustomAvatars drops default-avatar ids before the candidate cap, then
// keeps reading older custom-avatar accounts until the cap is full or the
// pool is exhausted. Callers still stop at the requested page size only
// after this set is ranked.
func retainCustomAvatars(conn *gorm.DB, ids []uint64, scores map[uint64]float64, sources map[uint64]string, coldStartFull bool) ([]uint64, error) {
	base := systemsettings.GetBaseURL()
	kept := make([]uint64, 0, len(ids))
	if len(ids) > 0 {
		var rows []models.Account
		if err := conn.Model(&models.Account{}).Select("id", "avatar").Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return nil, err
		}
		custom := make(map[uint64]bool, len(rows))
		for _, row := range rows {
			if defaultavatar.HasCustom(row.Avatar, base) {
				custom[row.Id] = true
			}
		}
		for _, id := range ids {
			if custom[id] {
				kept = append(kept, id)
			}
		}
	}
	// A short cold-start page already holds every row the SQL predicate can
	// see, so another pass cannot add any. A full page can still hide older
	// uploads behind rows HasCustom rejects; those are dropped below.
	if len(kept) >= suggestionCandidateLimit || !coldStartFull {
		if len(kept) > suggestionCandidateLimit {
			return kept[:suggestionCandidateLimit], nil
		}
		return kept, nil
	}
	seen := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for page := 0; page < 3 && len(kept) < suggestionCandidateLimit; page++ {
		known := make([]uint64, 0, len(seen))
		for id := range seen {
			known = append(known, id)
		}
		var more []models.Account
		q := customAvatarQuery(conn).Select("id", "avatar")
		if len(known) > 0 {
			q = q.Where("id NOT IN ?", known)
		}
		if err := q.Order("last_status_at IS NULL, last_status_at DESC, id DESC").Limit(suggestionCandidateLimit).Find(&more).Error; err != nil {
			return nil, err
		}
		if len(more) == 0 {
			break
		}
		for _, row := range more {
			seen[row.Id] = true
			if !defaultavatar.HasCustom(row.Avatar, base) {
				continue
			}
			if _, exists := scores[row.Id]; !exists {
				scores[row.Id] = 0
				sources[row.Id] = "global"
			}
			kept = append(kept, row.Id)
			if len(kept) == suggestionCandidateLimit {
				break
			}
		}
		if len(more) < suggestionCandidateLimit {
			break
		}
	}
	return kept, nil
}
