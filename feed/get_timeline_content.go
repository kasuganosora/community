package feed

import (
	"context"
	"errors"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"time"
)

// One shared budget bounds all optional cache work, including activation and
// a cold rebuild. SQL fallback retains the original request context.
const cacheReadBudget = 500 * time.Millisecond

type FilterCondition func(tx *gorm.DB) (query *gorm.DB, err error)
type AfterFilter func(statuses models.Statuses) models.Statuses

func GetStatusTimelineContent(timeline Timeline, params *ListParam, newFilterCondition FilterCondition, afterFilter AfterFilter) (models.Statuses, error) {
	if params == nil {
		params = &ListParam{Limit: MAX_ITEMS}
	} else {
		copyParams := *params
		params = &copyParams
	}
	limit := int(params.Limit)
	if limit <= 0 {
		limit = MAX_ITEMS
	}
	if limit > MAX_ITEMS {
		limit = MAX_ITEMS
	}
	ctx := params.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx = models.WithDomainSeveritiesMemo(ctx)
	cacheCtx, cancelCache := context.WithTimeout(ctx, cacheReadBudget)
	defer cancelCache()
	var activationErr error
	if params.Viewer != nil {
		activationErr = RecordActivity(cacheCtx, params.Viewer)
	}
	conn := db.GetDB().WithContext(ctx)
	key := TimelineKey(timeline)
	exactID, _ := params.Other["exact_id"].(uint64)
	hasExactID := exactID > 0
	forwardMin := !hasExactID && params.MinId > 0 && params.MaxId == 0
	databaseOnly, _ := params.Other["database_only"].(bool)
	databaseOnly = databaseOnly || hasExactID || ForceDatabaseReads() || activationErr != nil
	fallbackAcquired := false
	defer func() {
		if fallbackAcquired {
			<-fallbackSlots
		}
	}()
	var window []uint64
	var windowComplete bool
	if !databaseOnly {
		readIDs := func() ([]uint64, bool, error) {
			value, err := getDefaultService().eval(cacheCtx, readCandidatesScript, []string{key})
			if err != nil {
				return nil, false, err
			}
			values, ok := value.([]interface{})
			if !ok || len(values) < 2 || values[0] != "ready" {
				return nil, false, ErrNeedsRebuild
			}
			completeFlag, _ := values[1].(string)
			raw := make([]string, 0, len(values)-2)
			for _, id := range values[2:] {
				raw = append(raw, id.(string))
			}
			return utils.StringNumberArrayConvertUint64(raw), completeFlag == "1", nil
		}
		var cacheErr error
		window, windowComplete, cacheErr = readIDs()
		switch {
		case errors.Is(cacheErr, ErrNeedsRebuild):
			cacheReads.WithLabelValues("incomplete").Inc()
		case cacheErr != nil:
			cacheReads.WithLabelValues("error").Inc()
		case len(window) == 0:
			cacheReads.WithLabelValues("empty").Inc()
		default:
			cacheReads.WithLabelValues("ready").Inc()
		}
		if cacheErr == ErrNeedsRebuild {
			cacheErr = coalesceRebuildContext(cacheCtx, key, func() error {
				if contextual, ok := timeline.(interface{ RebuildContext(context.Context) error }); ok {
					// Detach SQL rebuild from caller #1's cancellation so coalesced
					// waiters do not repeat a full SQL rebuild if caller #1 disconnects.
					rebuildCtx, cancelRebuild := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
					defer cancelRebuild()
					return contextual.RebuildContext(rebuildCtx)
				}
				return ErrNeedsRebuild
			})
			if cacheErr == nil {
				window, windowComplete, cacheErr = readIDs()
			}
		}
		if cacheErr != nil || (len(window) == 0 && !windowComplete) {
			databaseOnly = true
		} else if params.MinId > 0 {
			foundMin := false
			for _, cachedID := range window {
				if cachedID == params.MinId {
					foundMin = true
					break
				}
			}
			if !foundMin {
				// MinId may precede the cached top window; query SQL so
				// intermediate posts older than the cached tail are not skipped.
				databaseOnly = true
			}
		}
	}
	if !databaseOnly && len(window) == 0 && windowComplete {
		return models.Statuses{}, nil
	}
	output := make(models.Statuses, 0, limit)
	aggregate := false
	if policy, ok := timeline.(interface{ AggregateReblogs() bool }); ok {
		aggregate = policy.AggregateReblogs()
	}
	page := newPageAggregation(aggregate)
	firstBatch := true
	hydrated := map[uint64]*models.Status{}
	// Read candidates in bounded pages. Filters and current authorization can
	// remove candidates, so continue rather than incorrectly returning an empty
	// or short page while older matching rows still exist.
	for len(output) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := limit - len(output)
		candidateLimit := min(MAX_ITEMS, max(32, remaining*2))
		if firstBatch {
			candidateLimit = remaining
			if !hasExactID && aggregate {
				candidateLimit = min(MAX_ITEMS, remaining+6)
			}
		}
		var pageHeaders models.Statuses
		var ids []uint64
		fromDatabase := databaseOnly
		if !databaseOnly {
			// The cache is the newest window. Order and cursors use
			// publication time, so a high serial id does not decide the page.
			var err error
			pageHeaders, ids, err = selectTimelinePage(conn, window, params, newFilterCondition, candidateLimit)
			if err != nil {
				return nil, err
			}
			if len(ids) == 0 {
				if windowComplete {
					break
				}
				databaseOnly = true
				fromDatabase = true
			}
		}
		if fromDatabase {
			// Exact single-row authorization lookups hit the primary key directly
			// and must not contend on the 16-slot multi-row scan semaphore.
			if !hasExactID && !fallbackAcquired {
				select {
				case fallbackSlots <- struct{}{}:
					fallbackAcquired = true
					sqlFallbacks.WithLabelValues("accepted").Inc()
				default:
					sqlFallbacks.WithLabelValues("busy").Inc()
					return nil, ErrFeedUnavailable
				}
				// Runs at most once per call (guarded by fallbackAcquired),
				// so deferring inside the loop does not accumulate.
				var fallbackCancel context.CancelFunc
				ctx, fallbackCancel = context.WithTimeout(ctx, 5*time.Second)
				defer fallbackCancel()
				conn = conn.WithContext(ctx)
			}
			var err error
			pageHeaders, ids, err = selectTimelinePage(conn, nil, params, newFilterCondition, candidateLimit)
			if err != nil {
				return nil, err
			}
			if len(ids) == 0 {
				break
			}
		}
		var statuses models.Statuses
		var err error
		if firstBatch {
			if len(pageHeaders) > remaining {
				// Pre-collapse reblog falloff duplicates on lightweight (id, reblog_id)
				// headers before running full 11-table preload hydration, keeping
				// hydration strictly bounded to `remaining` rows.
				preview := newPageAggregation(aggregate)
				trimmedHeaders := make(models.Statuses, 0, remaining)
				var lastInspectedID uint64
				for _, h := range pageHeaders {
					lastInspectedID = h.Id
					if preview.accept(h) {
						trimmedHeaders = append(trimmedHeaders, h)
						if len(trimmedHeaders) == remaining {
							break
						}
					}
				}
				pageHeaders = trimmedHeaders
				ids = pageHeaders.GetIds()
				if lastInspectedID != 0 && (len(ids) == 0 || ids[len(ids)-1] != lastInspectedID) {
					// Preserve cursor progress past any dropped trailing duplicate reblog.
					ids = append(ids, lastInspectedID)
				}
			}
			statuses, err = loadTimelineStatuses(conn, pageHeaders)
			if err == nil {
				for _, status := range statuses {
					hydrated[status.Id] = status
				}
			}
			firstBatch = false
		} else {
			statuses, err = loadTimelineCandidates(conn, ids, newFilterCondition, !fromDatabase, params.Viewer, afterFilter != nil)
		}
		if err != nil {
			return nil, err
		}
		statuses = orderStatusesByIDs(statuses, ids)
		if afterFilter != nil {
			statuses = afterFilter(statuses)
		}
		statuses, err = models.FilterVisibleStatuses(conn, params.Viewer, statuses)
		if err != nil {
			return nil, err
		}
		// A remote mirror of a local status already on this timeline is the
		// same item. Drop it here; the remote row stays stored.
		hidden, err := hiddenMirrorIDs(conn, statuses.GetIds(), newFilterCondition, statuses)
		if err != nil {
			return nil, err
		}
		filteredCandidates.Add(float64(max(0, len(ids)-len(statuses))))
		for _, status := range statuses {
			if len(output) == limit {
				break
			}
			if hidden[status.Id] {
				continue
			}
			if page.accept(status) {
				output = append(output, status)
			}
		}
		if hasExactID {
			break
		}
		next := ids[len(ids)-1]
		if forwardMin {
			// Ascending batches order oldest-to-newest above MinId; advance
			// MinId to the newest row inspected in this batch.
			if next == 0 || next == params.MinId {
				break
			}
			params.MinId = next
			continue
		}
		// Candidates are newest publication time first. The last id is the oldest
		// row of this batch; the next batch is strictly older than that status.
		if next == 0 || next == params.MaxId {
			break
		}
		params.MaxId = next
	}
	if forwardMin && len(output) > 1 {
		for i, j := 0, len(output)-1; i < j; i, j = i+1, j-1 {
			output[i], output[j] = output[j], output[i]
		}
	}
	if len(output) == 0 {
		return output, nil
	}
	missing := models.Statuses{}
	for _, status := range output {
		if hydrated[status.Id] == nil {
			missing = append(missing, status)
		}
	}
	if len(missing) > 0 {
		loaded, err := loadTimelineStatuses(conn, missing)
		if err != nil {
			return nil, err
		}
		for _, status := range loaded {
			hydrated[status.Id] = status
		}
	}
	result := make(models.Statuses, 0, len(output))
	for _, status := range output {
		if loaded := hydrated[status.Id]; loaded != nil {
			result = append(result, loaded)
		}
	}
	return result, nil
}

// Roots and originals share each association preload query. Candidate selection
// already supplied their identities, so no recursive preload tree is necessary.
func loadTimelineStatuses(conn *gorm.DB, selected models.Statuses) (models.Statuses, error) {
	if len(selected) == 0 {
		return models.Statuses{}, nil
	}
	ids := make([]uint64, 0, len(selected)*2)
	seen := map[uint64]bool{}
	for _, status := range selected {
		if !seen[status.Id] {
			ids = append(ids, status.Id)
			seen[status.Id] = true
		}
		if status.ReblogId != nil && *status.ReblogId > 0 && !seen[*status.ReblogId] {
			ids = append(ids, *status.ReblogId)
			seen[*status.ReblogId] = true
		}
	}
	flat := models.Statuses{}
	err := conn.Model(&models.Status{}).Preload("Account").Preload("Account.Extra").
		Preload("Content").Preload("Application").Preload("Attachments").Preload("ReplyAccount").
		Preload("Emojis").Preload("ToAccounts").Preload("Poll").Preload("Poll.Options").Preload("Tags").
		Where("statuses.id IN ?", ids).Find(&flat).Error
	if err != nil {
		return nil, err
	}
	byID := make(map[uint64]*models.Status, len(flat))
	for _, status := range flat {
		status.SetAllExtendFieldsLoaded()
		byID[status.Id] = status
	}
	result := make(models.Statuses, 0, len(selected))
	for _, candidate := range selected {
		status := byID[candidate.Id]
		if status == nil {
			continue
		}
		if status.ReblogId != nil && *status.ReblogId > 0 {
			status.Reblog = byID[*status.ReblogId]
			if status.Reblog == nil {
				continue
			}
		}
		result = append(result, status)
	}
	return result, nil
}

// EligibleTimelineStatuses excludes unpublished and banned-author content before
// LIMIT in both cache snapshots and database pagination.
func EligibleTimelineStatuses(tx *gorm.DB) *gorm.DB {
	return tx.Where("statuses.scheduled_at IS NULL AND statuses.deleted_at IS NULL").Where("EXISTS (SELECT 1 FROM accounts WHERE accounts.id = statuses.account_id AND accounts.is_ban = ?)", false)
}

type pageAggregation struct {
	enabled  bool
	position int
	seen     map[uint64]int
}

// hiddenMirrorIDs reports remote rows in statuses that duplicate a local status
// this same timeline would also return. The remote row is not deleted.
func hiddenMirrorIDs(conn *gorm.DB, ids []uint64, filter FilterCondition, loaded ...models.Statuses) (map[uint64]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var preloaded models.Statuses
	if len(loaded) > 0 {
		preloaded = loaded[0]
	}
	mirrors, err := models.MirrorLocalIDsLoaded(conn, preloaded, ids, systemsettings.GetBaseURL())
	if err != nil || len(mirrors) == 0 {
		return nil, err
	}
	localIDs := make([]uint64, 0, len(mirrors))
	seen := map[uint64]bool{}
	for _, localID := range mirrors {
		if localID != 0 && !seen[localID] {
			seen[localID] = true
			localIDs = append(localIDs, localID)
		}
	}
	eligible, err := timelineIncludesIDs(conn, filter, localIDs)
	if err != nil {
		return nil, err
	}
	hidden := make(map[uint64]bool, len(mirrors))
	for mirrorID, localID := range mirrors {
		if eligible[localID] {
			hidden[mirrorID] = true
		}
	}
	return hidden, nil
}

func timelineIncludesIDs(conn *gorm.DB, filter FilterCondition, ids []uint64) (map[uint64]bool, error) {
	query, err := filter(conn.Session(&gorm.Session{NewDB: true}).Model(&models.Status{}))
	if err != nil {
		return nil, err
	}
	var found []uint64
	if err := query.Where("statuses.id IN ?", ids).Pluck("statuses.id", &found).Error; err != nil {
		return nil, err
	}
	out := make(map[uint64]bool, len(found))
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

func newPageAggregation(enabled bool) *pageAggregation {
	return &pageAggregation{enabled: enabled, seen: make(map[uint64]int)}
}
func (p *pageAggregation) accept(status *models.Status) bool {
	if !p.enabled {
		return true
	}
	origin := status.Id
	if status.ReblogId != nil && *status.ReblogId > 0 {
		origin = *status.ReblogId
	}
	if previous, ok := p.seen[origin]; ok && p.position-previous <= REBLOG_FALLOFF {
		return false
	}
	p.seen[origin] = p.position
	p.position++
	return true
}

// Mutes are a feed preference, not object authorization. Always evaluate them
// against current rows, including the original author of a reblog.
func timelineMuteFilter(tx *gorm.DB, viewer *models.Account) *gorm.DB {
	if viewer == nil {
		return tx
	}
	return tx.Where(`NOT EXISTS (SELECT 1 FROM mute_accounts WHERE mute_accounts.account_id = ? AND (mute_accounts.expired_at IS NULL OR mute_accounts.expired_at > ?) AND (mute_accounts.target_id = statuses.account_id OR mute_accounts.target_id = (SELECT original.account_id FROM statuses AS original WHERE original.id = statuses.reblog_id)))`, viewer.Id, time.Now())
}

func loadTimelineCandidates(conn *gorm.DB, ids []uint64, filter FilterCondition, recheck bool, viewer *models.Account, legacyFilter bool) (models.Statuses, error) {
	query := conn.Model(&models.Status{})
	if recheck {
		var err error
		query, err = filter(query)
		if err != nil {
			return nil, err
		}
	}
	query = timelineMuteFilter(query, viewer)
	statuses := models.Statuses{}
	// Only fields needed for visibility and legacy post-filter predicates are
	// loaded here. All presentation associations are hydrated once, after filling.
	if legacyFilter {
		query = query.Preload("ToAccounts").Preload("Attachments")
	}
	err := query.Preload("Reblog").Where("statuses.id IN ?", ids).Order(StatusTimelineOrder).Find(&statuses).Error
	return statuses, err
}

// A Redis outage cannot produce an unbounded number of concurrent SQL scans.
var fallbackSlots = make(chan struct{}, 16)
var ErrFeedUnavailable = errors.New("feed database fallback is at capacity")

const readCandidatesScript = `
local k=KEYS[1];local m=k..':meta'
if redis.call('HGET',m,'ready')~='1' or redis.call('HGET',m,'rank')~='ca1' or redis.call('HEXISTS',m,'epoch')==0 or redis.call('HEXISTS',m,'generation')==0 or redis.call('HGET',k..':versions','_epoch')~=redis.call('HGET',m,'epoch') then return {} end
local expected=tonumber(redis.call('HGET',m,'count') or '0')
if expected>0 and redis.call('EXISTS',k)==0 then return {} end
local members=redis.call('ZREVRANGE',k,0,-1)
local complete='0'
if redis.call('HGET',m,'complete')=='1' and #members>=expected then complete='1' end
local result={'ready',complete}
for _,id in ipairs(members) do table.insert(result,id) end
return result
`

// selectTimelinePage returns status headers (id + reblog_id) and ids ordered by
// publication time (newest-first by default, or oldest-first above min_id when
// paging upward without max_id). cacheIDs, when set, restricts the page to the
// Redis window. Cursors are status ids resolved to (created_at, id).
func selectTimelinePage(conn *gorm.DB, cacheIDs []uint64, params *ListParam, filter FilterCondition, limit int) (models.Statuses, []uint64, error) {
	query, err := filter(conn.Model(&models.Status{}))
	if err != nil {
		return nil, nil, err
	}
	if params != nil {
		if exactID, _ := params.Other["exact_id"].(uint64); exactID > 0 {
			headers := models.Statuses{}
			if err := timelineMuteFilter(query.Where("statuses.id = ?", exactID), params.Viewer).Select("statuses.id", "statuses.reblog_id").Limit(1).Find(&headers).Error; err != nil {
				return nil, nil, err
			}
			return headers, headers.GetIds(), nil
		}
	}
	if len(cacheIDs) > 0 {
		query = query.Where("statuses.id IN ?", cacheIDs)
	}
	cursors, err := loadStatusCursors(conn, cursorIDs(params)...)
	if err != nil {
		return nil, nil, err
	}
	query = applyTimelineCursors(query, params, cursors)
	order := StatusTimelineOrder
	if params != nil && params.MinId > 0 && params.MaxId == 0 {
		order = StatusTimelineOrderAsc
	}
	headers := models.Statuses{}
	if err := timelineMuteFilter(query, params.Viewer).Select("statuses.id", "statuses.reblog_id").Order(order).Limit(limit).Find(&headers).Error; err != nil {
		return nil, nil, err
	}
	return headers, headers.GetIds(), nil
}
