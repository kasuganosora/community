package models

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

var ErrRecommendationEligibilityBudget = errors.New("recommendation eligibility budget exceeded")

type RecommendationEligibilityOptions struct {
	Context    string
	AsOf       time.Time
	Window     time.Duration
	RootWindow bool
	// MetadataOnly is for ID candidate selection. It deliberately omits
	// keyword filtering; every serialized page must call the full check.
	MetadataOnly bool
	// AggregateOnly is for anonymous aggregates such as tag trend counts,
	// where no individual post or author is surfaced. Remote authors whose
	// discovery preference has never been read are counted there (see
	// Account.AllowsTrendAggregation); explicit refusals are still excluded.
	AggregateOnly bool
}

// RecommendationStatusScope is a bounded caller's initial SQL preselection,
// not an authorization decision. Apply FilterRecommendableStatuses before use.
func RecommendationStatusScope(conn *gorm.DB, asOf time.Time, window time.Duration) *gorm.DB {
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	backfill := dialect.JSONText(conn, "statuses.properties", StatusPropBackfilled)
	if dialect.IsMySQL(conn) {
		backfill = "JSON_UNQUOTE(" + backfill + ")"
	}
	return conn.Model(&Status{}).
		Where("statuses.visibility = ? AND statuses.deleted_at IS NULL AND statuses.scheduled_at IS NULL", StatusVisibilityPublic).
		Where("(statuses.reblog_id IS NULL OR statuses.reblog_id = 0) AND (statuses.in_reply_to_id IS NULL OR statuses.in_reply_to_id = 0)").
		Where("statuses.created_at >= ? AND statuses.created_at < ?", asOf.Add(-window), asOf).
		Where("COALESCE("+backfill+", '') <> ?", "1")
}

// FilterRecommendableAccounts reloads candidates from SQL so stale search or
// Redis models cannot grant recommendation eligibility. excludeFollowed is
// used only for account suggestions, never for authors of recommended posts.
func FilterRecommendableAccounts(conn *gorm.DB, viewer *Account, accounts []*Account, excludeFollowed bool) ([]*Account, error) {
	return filterRecommendableAccountsAt(conn, viewer, accounts, excludeFollowed, time.Now().UTC(), false)
}

func filterRecommendableAccountsAt(conn *gorm.DB, viewer *Account, accounts []*Account, excludeFollowed bool, now time.Time, aggregateOnly bool) ([]*Account, error) {
	result := make([]*Account, 0, len(accounts))
	if len(accounts) == 0 {
		return result, nil
	}
	if len(accounts) > 2000 {
		return nil, ErrRecommendationEligibilityBudget
	}
	ids := make([]uint64, 0, len(accounts))
	seen := map[uint64]bool{}
	for _, a := range accounts {
		if a != nil && a.Id != 0 && !seen[a.Id] {
			seen[a.Id] = true
			ids = append(ids, a.Id)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	var current []*Account
	if err := conn.Where("id IN ?", ids).Preload("User").Find(&current).Error; err != nil {
		return nil, err
	}
	blocked := map[uint64]bool{}
	blockedDomains := map[string]bool{}
	domains := []string{}
	domainSeen := map[string]bool{}
	for _, a := range current {
		if a.Domain != nil {
			domain := strings.ToLower(strings.TrimSpace(*a.Domain))
			if domain != "" && !domainSeen[domain] {
				domainSeen[domain] = true
				domains = append(domains, domain)
			}
		}
	}
	var policies []DomainBlock
	if err := conn.Select("domain", "severity").Where("LOWER(domain) IN ?", domains).Find(&policies).Error; err != nil {
		return nil, err
	}
	for _, p := range policies {
		severity := strings.ToLower(strings.TrimSpace(p.Severity))
		if severity == "" || severity == "suspend" || severity == "silence" {
			blockedDomains[strings.ToLower(strings.TrimSpace(p.Domain))] = true
		}
	}
	ownerID := uint64(0)
	viewerDomain := ""
	if viewer != nil {
		ownerID = viewer.Id
		if viewer.Domain != nil {
			viewerDomain = strings.ToLower(*viewer.Domain)
		}
	}
	var blocks []Block
	q := conn.Where("account_id = ? AND ((type = ? AND target_account_id IN ?) OR (type = ? AND LOWER(target_domain) IN ?))", 0, BlockTypeAccount, ids, BlockTypeDomain, domains)
	if ownerID != 0 {
		q = conn.Where("(account_id IN ? AND ((type = ? AND target_account_id IN ?) OR (type = ? AND LOWER(target_domain) IN ?))) OR (account_id IN ? AND ((type = ? AND target_account_id = ?) OR (type = ? AND LOWER(target_domain) = ? AND target_domain <> '')))", []uint64{0, ownerID}, BlockTypeAccount, ids, BlockTypeDomain, domains, ids, BlockTypeAccount, ownerID, BlockTypeDomain, viewerDomain)
	}
	if err := q.Find(&blocks).Error; err != nil {
		return nil, err
	}
	for _, b := range blocks {
		if b.AccountId == 0 || b.AccountId == ownerID {
			if b.Type == BlockTypeAccount {
				blocked[b.TargetAccountId] = true
			} else if b.Type == BlockTypeDomain {
				blockedDomains[strings.ToLower(strings.TrimSpace(b.TargetDomain))] = true
			}
		} else {
			blocked[b.AccountId] = true
		}
	}
	if ownerID != 0 {
		var muted []MuteAccount
		if err := conn.Where("account_id = ? AND target_id IN ? AND (expired_at IS NULL OR expired_at > ?)", ownerID, ids, now).Find(&muted).Error; err != nil {
			return nil, err
		}
		for _, m := range muted {
			blocked[m.TargetId] = true
		}
		if excludeFollowed {
			blocked[ownerID] = true
			var follows []Follow
			if err := conn.Select("follow_id").Where("account_id = ? AND follow_id IN ? AND status IN ?", ownerID, ids, []int{FollowStatusFollowed, FollowStatusRequested}).Find(&follows).Error; err != nil {
				return nil, err
			}
			for _, f := range follows {
				blocked[f.FollowId] = true
			}
		}
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, strconv.FormatUint(id, 10))
	}
	var feedback []RecommendationFeedback
	if err := recommendationFeedbackScope(conn, ownerID, now).Where("target_kind = ? AND target_key IN ?", "account", keys).Find(&feedback).Error; err != nil {
		return nil, err
	}
	for _, f := range feedback {
		if f.Action == "suppress" || f.Action == "hide_author" || (excludeFollowed && f.Action == "dismiss_suggestion") {
			id, _ := strconv.ParseUint(f.TargetKey, 10, 64)
			blocked[id] = true
		}
	}
	// The same remote actor can exist as several account rows (refetches,
	// moved/renamed actors, acct case drift). An author exclusion targets the
	// person, so it also covers every row sharing the hidden account's acct.
	hiddenAccts, err := hiddenRecommendationAccts(conn, ownerID, now)
	if err != nil {
		return nil, err
	}
	if len(hiddenAccts) > 0 {
		for _, a := range current {
			if hiddenAccts[strings.ToLower(strings.TrimSpace(a.Acct))] {
				blocked[a.Id] = true
			}
		}
	}
	byID := map[uint64]*Account{}
	for _, a := range current {
		if a.IsBan || a.DeletedAt.Valid || blocked[a.Id] || (!aggregateOnly && !a.AllowsRecommendation(now)) || (aggregateOnly && !a.AllowsTrendAggregation(now)) {
			continue
		}
		if a.Domain != nil {
			if blockedDomains[strings.ToLower(*a.Domain)] {
				continue
			}
		} else if a.User == nil || a.User.IsBan || a.User.Status != UserStatusNormal || a.User.DeletedAt.Valid {
			continue
		}
		byID[a.Id] = a
	}
	for _, id := range ids {
		if a := byID[id]; a != nil {
			result = append(result, a)
		}
	}
	return result, nil
}

// hiddenRecommendationAccts returns the lower-cased acct of every account
// hidden by the viewer (hide_author) or by site governance (suppress).
func hiddenRecommendationAccts(conn *gorm.DB, ownerID uint64, now time.Time) (map[string]bool, error) {
	var keys []string
	if err := recommendationFeedbackScope(conn, ownerID, now).Where("target_kind = ? AND action IN ?", "account", []string{"suppress", "hide_author"}).Limit(5000).Pluck("target_key", &keys).Error; err != nil {
		return nil, err
	}
	result := map[string]bool{}
	if len(keys) == 0 {
		return result, nil
	}
	ids := make([]uint64, 0, len(keys))
	for _, key := range keys {
		if id, err := strconv.ParseUint(key, 10, 64); err == nil && id != 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	var accts []string
	if err := conn.Model(&Account{}).Where("id IN ?", ids).Pluck("acct", &accts).Error; err != nil {
		return nil, err
	}
	for _, acct := range accts {
		if acct = strings.ToLower(strings.TrimSpace(acct)); acct != "" {
			result[acct] = true
		}
	}
	return result, nil
}

func recommendationFeedbackScope(conn *gorm.DB, ownerID uint64, now time.Time) *gorm.DB {
	q := conn.Model(&RecommendationFeedback{}).Where("scope = ? AND owner_id = ?", "site", 0)
	if ownerID != 0 {
		q = conn.Model(&RecommendationFeedback{}).Where("(scope = ? AND owner_id = ?) OR (scope = ? AND owner_id = ?)", "site", 0, "user", ownerID)
	}
	return q.Where("expires_at IS NULL OR expires_at > ?", now)
}

// FilterRecommendableStatuses validates both roots and every embedded quote
// using current SQL state. It returns newly loaded models in input ID order;
// callers must never serialize the input models after this function returns.
// There are no attachment/poll loads here, and no per-status SQL operations.
func FilterRecommendableStatuses(conn *gorm.DB, viewer *Account, statuses Statuses, opts RecommendationEligibilityOptions) (Statuses, error) {
	result := make(Statuses, 0, len(statuses))
	if len(statuses) == 0 {
		return result, nil
	}
	if len(statuses) > 400 {
		return nil, ErrRecommendationEligibilityBudget
	}
	if opts.Context != "home" && opts.Context != "public" {
		return nil, errors.New("recommendation filter context must be home or public")
	}
	if opts.AsOf.IsZero() {
		opts.AsOf = time.Now().UTC()
	}
	if opts.Window <= 0 {
		opts.Window = 7 * 24 * time.Hour
	}
	now := time.Now().UTC()
	rootIDs := make([]uint64, 0, len(statuses))
	seen := map[uint64]bool{}
	for _, s := range statuses {
		if s != nil && s.Id != 0 && !seen[s.Id] {
			seen[s.Id] = true
			rootIDs = append(rootIDs, s.Id)
		}
	}
	byID := map[uint64]*Status{}
	pending := append([]uint64(nil), rootIDs...)
	queried := map[uint64]bool{}
	for depth := 0; depth < 4 && len(pending) > 0; depth++ {
		if len(queried)+len(pending) > 2000 {
			return nil, ErrRecommendationEligibilityBudget
		}
		var rows Statuses
		q := conn.Where("id IN ?", pending).Preload("Tags")
		if !opts.MetadataOnly {
			q = q.Preload("Content")
		}
		if err := q.Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, id := range pending {
			queried[id] = true
		}
		pending = nil
		for _, s := range rows {
			byID[s.Id] = s
		}
		nextSeen := map[uint64]bool{}
		for _, s := range rows {
			for _, ref := range []*uint64{s.QuoteId, s.ReblogId} {
				if ref != nil && *ref != 0 && !queried[*ref] && !nextSeen[*ref] {
					nextSeen[*ref] = true
					pending = append(pending, *ref)
				}
			}
		}
	}
	allIDs := make([]uint64, 0, len(byID))
	keys := make([]string, 0, len(byID))
	authorIDs := map[uint64]bool{}
	authors := []*Account{}
	tagKeys := map[string]bool{}
	for id, s := range byID {
		allIDs = append(allIDs, id)
		keys = append(keys, strconv.FormatUint(id, 10))
		if !authorIDs[s.AccountId] {
			authorIDs[s.AccountId] = true
			authors = append(authors, &Account{Id: s.AccountId})
		}
		for _, tag := range s.Tags {
			if tag != nil {
				tagKeys[TagKey(tag.Name)] = true
			}
		}
	}
	if len(allIDs) == 0 {
		return result, nil
	}
	allowedAuthors, err := filterRecommendableAccountsAt(conn, viewer, authors, false, now, opts.AggregateOnly)
	if err != nil {
		return nil, err
	}
	authorsByID := map[uint64]*Account{}
	for _, a := range allowedAuthors {
		authorsByID[a.Id] = a
	}
	ownerID := uint64(0)
	if viewer != nil {
		ownerID = viewer.Id
	}
	blockedStatuses := map[uint64]bool{}
	blockedTags := map[string]bool{}
	if ownerID != 0 {
		var muted []MuteStatus
		if err := conn.Where("account_id = ? AND status_id IN ?", ownerID, allIDs).Find(&muted).Error; err != nil {
			return nil, err
		}
		for _, m := range muted {
			blockedStatuses[m.StatusId] = true
		}
	}
	tags := make([]string, 0, len(tagKeys))
	for key := range tagKeys {
		tags = append(tags, key)
	}
	var feedback []RecommendationFeedback
	q := recommendationFeedbackScope(conn, ownerID, now)
	if len(tags) > 0 {
		q = q.Where("(target_kind = ? AND target_key IN ?) OR (target_kind = ? AND target_key IN ?)", "status", keys, "tag", tags)
	} else {
		q = q.Where("target_kind = ? AND target_key IN ?", "status", keys)
	}
	if err := q.Find(&feedback).Error; err != nil {
		return nil, err
	}
	for _, f := range feedback {
		if f.TargetKind == "status" && (f.Action == "suppress" || f.Action == "hide_status") {
			id, _ := strconv.ParseUint(f.TargetKey, 10, 64)
			blockedStatuses[id] = true
		}
		if f.TargetKind == "tag" && (f.Action == "suppress" || f.Action == "hide_tag") {
			blockedTags[TagKey(f.TargetKey)] = true
		}
	}
	var filters Filters
	if !opts.MetadataOnly {
		filters, err = GetFilters(conn, ownerID)
		if err != nil {
			return nil, err
		}
	}
	var allowed func(uint64, bool, map[uint64]bool) bool
	allowed = func(id uint64, root bool, chain map[uint64]bool) bool {
		s := byID[id]
		if s == nil || chain[id] || len(chain) >= 4 || s.DeletedAt != nil || s.Visibility != StatusVisibilityPublic || s.ScheduledAt != nil || s.IsBackfilled() || blockedStatuses[id] {
			return false
		}
		if s.CreatedAt == nil || !s.CreatedAt.Before(opts.AsOf) {
			return false
		}
		if root && ((s.InReplyToId != nil && *s.InReplyToId != 0) || (s.ReblogId != nil && *s.ReblogId != 0) || (opts.RootWindow && s.CreatedAt.Before(opts.AsOf.Add(-opts.Window)))) {
			return false
		}
		a := authorsByID[s.AccountId]
		if a == nil {
			return false
		}
		if s.LocalOnly && viewer != nil && viewer.Domain != nil {
			return false
		}
		for _, tag := range s.Tags {
			if tag != nil && blockedTags[TagKey(tag.Name)] {
				return false
			}
		}
		if !opts.MetadataOnly {
			// An absent content row is unverified, not an empty harmless body.
			if s.Content.StatusId != s.Id {
				return false
			}
			for _, f := range filters.GetStatusFiltersIn(s, viewer, opts.Context) {
				if f.FilterAction == FilterActionHide {
					return false
				}
			}
		}
		chain[id] = true
		defer delete(chain, id)
		for _, ref := range []*uint64{s.QuoteId, s.ReblogId} {
			if ref != nil && *ref != 0 && !allowed(*ref, false, chain) {
				return false
			}
		}
		s.Account = a
		if s.QuoteId != nil {
			s.Quote = byID[*s.QuoteId]
		}
		if s.ReblogId != nil {
			s.Reblog = byID[*s.ReblogId]
		}
		return true
	}
	for _, id := range rootIDs {
		if allowed(id, true, map[uint64]bool{}) {
			result = append(result, byID[id])
		}
	}
	return result, nil
}
