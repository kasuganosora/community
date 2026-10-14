package discovery

import (
	"context"
	"sort"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

type AccountSuggestion struct {
	Account *models.Account
	Source  string
}

// SuggestAccounts uses at most 400 candidates and 2,000 public social edges.
// Generic/off never read the viewer's graph for ranking. Relationship reads
// in the final eligibility check only exclude follows and pending requests.
func SuggestAccounts(ctx context.Context, conn *gorm.DB, viewer *models.Account, limit int) ([]AccountSuggestion, error) {
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
	var recent []*models.Account
	if err = conn.Model(&models.Account{}).Where("discoverable = ? AND is_ban = ?", true, false).Order("last_status_at IS NULL, last_status_at DESC, id DESC").Limit(400).Find(&recent).Error; err != nil {
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
	if len(ids) > 400 {
		ids = ids[:400]
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

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if scores[a.Id] != scores[b.Id] {
			return scores[a.Id] > scores[b.Id]
		}
		if a.LastStatusAt != nil && b.LastStatusAt != nil && !a.LastStatusAt.Equal(*b.LastStatusAt) {
			return a.LastStatusAt.After(*b.LastStatusAt)
		}
		return a.Id > b.Id
	})
	domains := map[string]int{}
	for _, a := range candidates {
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
