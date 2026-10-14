package discovery

import (
	"context"
	"errors"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

var feedReadSlots = make(chan struct{}, 8)

// StatusPage never calls Meilisearch or a remote federation server. New windows
// merge bounded ID sources; subsequent pages scan only one frozen logical block.
func StatusPage(parent context.Context, viewer *models.Account, cursor string, limit int) (page Page, err error) {
	started := time.Now()
	defer func() { observeFeedRead(started, page, err) }()
	page = Page{Items: []Item{}}
	if viewer == nil || viewer.Id == 0 || viewer.UserId == 0 || limit < 1 || limit > 20 {
		return page, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	select {
	case feedReadSlots <- struct{}{}:
		defer func() { <-feedReadSlots }()
	default:
		return page, ErrUnavailable
	}
	ctx = models.WithFilterContext(ctx, "home")
	conn := db.GetDB().WithContext(ctx)
	preference, err := models.GetRecommendationPreference(conn, viewer.UserId)
	if err != nil {
		return page, err
	}
	page.Preference = preference.Preference
	page.PreferenceRevision = preference.Revision
	enabled, err := EnabledContext(ctx)
	if err != nil {
		return page, err
	}
	if !enabled || preference.Preference == "off" {
		page.Disabled = true
		page.Mode = "disabled"
		return page, nil
	}
	policy, err := feedPolicy(ctx)
	if err != nil {
		return page, err
	}
	now := time.Now().UTC()
	var snapshot Snapshot
	block, offset := 0, 0
	if cursor != "" {
		decoded, err := decodeFeedCursor(cursor, viewer.Id, preference.Revision, now)
		if err != nil {
			return page, err
		}
		snapshot, err = loadSnapshot(ctx, decoded, now)
		if err != nil {
			return page, err
		}
		if snapshot.Preference != preference.Preference {
			return page, ErrResetRequired
		}
		if snapshot.PolicyVersion != policy {
			return page, ErrResetRequired
		}
		block, offset = decoded.Block, decoded.Offset
	} else {
		pool, poolErr := loadFeedPool(ctx, now)
		if poolErr != nil || len(CursorSigningKeys()) == 0 {
			return chronologicalFeedFallback(ctx, conn, viewer, preference, limit, now)
		}
		if pool.PolicyVersion != policy {
			pool.Mode = "rules"
			pool.Lists = nil
		}
		candidates, err := selectFeedCandidates(ctx, viewer, preference.Preference == "personalized", pool, preference.Languages)
		if err != nil {
			return page, err
		}
		generation, err := feedGeneration()
		if err != nil {
			return page, err
		}
		snapshot = Snapshot{Generation: generation, UserID: viewer.Id, Revision: preference.Revision, Preference: preference.Preference, Algorithm: AlgorithmVersion, Mode: "rules", PoolGeneration: pool.Generation, PolicyVersion: policy, CreatedAt: now, ExpiresAt: now.Add(SnapshotTTL)}
		snapshot.Blocks = RankCandidates(candidates, pool.AsOf, preference.Preference == "personalized", generation)
		// Report the sources actually retained in this window, independently of
		// the shared pool/index capabilities. Generic does not consume Meili.
		if preference.Preference == "personalized" && (pool.Mode == "meili_lexical" || pool.Mode == "meili_hybrid") {
			selected := map[uint64]bool{}
			for _, block := range snapshot.Blocks {
				for _, item := range block {
					selected[item.ID] = true
				}
			}
			lexical, semantic := false, false
			for _, candidate := range candidates {
				if selected[candidate.StatusID] {
					for _, source := range candidate.Sources {
						lexical = lexical || source.Family == "lexical"
						semantic = semantic || source.Family == "semantic"
					}
				}
			}
			if lexical {
				snapshot.Mode = "meili_lexical"
			}
			if semantic && pool.Mode == "meili_hybrid" {
				snapshot.Mode = "meili_hybrid"
			}
		}
		saved, err := saveSnapshot(ctx, snapshot)
		if err != nil {
			return chronologicalFeedFallback(ctx, conn, viewer, preference, limit, now)
		}
		snapshot = saved
	}
	page.Generation = snapshot.Generation
	page.Mode = snapshot.Mode
	if len(snapshot.Blocks) == 0 {
		return page, nil
	}
	if block >= len(snapshot.Blocks) || offset >= len(snapshot.Blocks[block]) {
		return page, ErrResetRequired
	}
	// Load the remaining block once. Page size changes merely stop scanning at
	// a different slot; hidden/deleted slots always advance the signed cursor.
	slots := snapshot.Blocks[block][offset:]
	stubs := make(models.Statuses, 0, len(slots))
	for _, slot := range slots {
		stubs = append(stubs, &models.Status{Id: slot.ID})
	}
	visible, err := models.FilterRecommendableStatuses(conn, viewer, stubs, models.RecommendationEligibilityOptions{Context: "home", AsOf: now, Window: CandidateWindow, RootWindow: true})
	if err != nil {
		return page, err
	}
	visible = filterFeedLanguages(visible, preference.Languages)
	visible, err = hydrateFeedStatuses(conn, visible)
	if err != nil {
		return page, err
	}
	byID := map[uint64]*models.Status{}
	for _, status := range visible {
		byID[status.Id] = status
	}
	topics := map[string]bool{}
	if preference.Preference == "personalized" {
		currentTopics, err := feedFollowedTopics(conn, viewer.Id)
		if err != nil {
			return page, err
		}
		for _, topic := range currentTopics {
			topics[topic] = true
		}
	}
	for _, slot := range slots {
		offset++
		if status := byID[slot.ID]; status != nil {
			page.Items = append(page.Items, Item{Status: status, Reason: feedReason(slot, topics, status)})
		}
		if len(page.Items) >= limit {
			break
		}
	}
	page.NextCursor, err = nextFeedCursor(snapshot, block, offset)
	return page, err
}

func feedReason(item RankedItem, topics map[string]bool, status *models.Status) *Reason {
	switch item.ReasonCode {
	case "followed_topic":
		if topics[item.ReasonTag] && status != nil {
			for _, tag := range status.Tags {
				if tag != nil && models.TagKey(tag.Name) == item.ReasonTag {
					return &Reason{Code: "followed_topic", Label: "你关注的话题"}
				}
			}
		}
	case "discover_author":
		return &Reason{Code: "discover_author", Label: "发现近期作者"}
	case "recent_public":
		return &Reason{Code: "recent_public", Label: "近期公开内容"}
	}
	return nil
}

func chronologicalFeedFallback(ctx context.Context, conn *gorm.DB, viewer *models.Account, preference models.RecommendationPreference, limit int, asOf time.Time) (Page, error) {
	page := Page{Items: []Item{}, Mode: "chronological_fallback", Preference: preference.Preference, PreferenceRevision: preference.Revision}
	if err := ctx.Err(); err != nil {
		return page, ErrUnavailable
	}
	var candidates models.Statuses
	if err := models.RecommendationStatusScope(conn, asOf, CandidateWindow).Order("statuses.created_at DESC, statuses.id DESC").Limit(80).Find(&candidates).Error; err != nil {
		return page, err
	}
	visible, err := models.FilterRecommendableStatuses(conn, viewer, candidates, models.RecommendationEligibilityOptions{Context: "home", AsOf: asOf, Window: CandidateWindow, RootWindow: true})
	if err != nil {
		return page, err
	}
	visible = filterFeedLanguages(visible, preference.Languages)
	if len(visible) > limit {
		visible = visible[:limit]
	}
	visible, err = hydrateFeedStatuses(conn, visible)
	if err != nil {
		return page, err
	}
	for _, status := range visible {
		page.Items = append(page.Items, Item{Status: status})
	}
	return page, nil
}

func filterFeedLanguages(statuses models.Statuses, languages []string) models.Statuses {
	filtered := statuses[:0]
	for _, status := range statuses {
		if models.RecommendationLanguageAllowed(status.Language, languages) {
			filtered = append(filtered, status)
		}
	}
	return filtered
}

func hydrateFeedStatuses(conn *gorm.DB, statuses models.Statuses) (models.Statuses, error) {
	if len(statuses) == 0 {
		return statuses, nil
	}
	ids := make([]uint64, 0, len(statuses))
	flat := make(models.Statuses, 0, len(statuses))
	seen := map[uint64]bool{}
	var collect func(*models.Status)
	collect = func(status *models.Status) {
		if status == nil || seen[status.Id] {
			return
		}
		seen[status.Id] = true
		ids = append(ids, status.Id)
		flat = append(flat, status)
		collect(status.Quote)
		collect(status.Reblog)
	}
	for _, status := range statuses {
		collect(status)
	}
	var loaded models.Statuses
	// Keep the exact objects whose content/quotes just passed eligibility. Only
	// attach missing extension rows; do not replace roots with another read.
	err := conn.Model(&models.Status{}).Select("id", "application_id").Preload("Application").Preload("Attachments").Preload("Emojis").Preload("ToAccounts").Preload("Poll").Preload("Poll.Options").Where("id IN ?", ids).Find(&loaded).Error
	if err != nil {
		return nil, err
	}
	byID := map[uint64]*models.Status{}
	for _, status := range loaded {
		byID[status.Id] = status
	}
	authorIDs := []uint64{}
	for _, status := range flat {
		authorIDs = append(authorIDs, status.AccountId)
	}
	var extras []*models.AccountExtra
	if err := conn.Where("account_id IN ?", authorIDs).Find(&extras).Error; err != nil {
		return nil, err
	}
	extraByID := map[uint64]*models.AccountExtra{}
	for _, extra := range extras {
		extraByID[extra.AccountId] = extra
	}
	for _, status := range flat {
		extensions := byID[status.Id]
		if extensions == nil {
			return nil, errors.New("recommendation status disappeared while hydrating")
		}
		status.Application = extensions.Application
		status.Attachments = extensions.Attachments
		status.Emojis = extensions.Emojis
		status.ToAccounts = extensions.ToAccounts
		status.Poll = extensions.Poll
		if status.Account != nil {
			status.Account.Extra = extraByID[status.AccountId]
		}
		status.SetAllExtendFieldsLoaded()
	}
	return statuses, nil
}
