package feed

import (
	"context"
	"errors"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
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
	cacheCtx, cancelCache := context.WithTimeout(ctx, cacheReadBudget)
	defer cancelCache()
	var activationErr error
	if params.Viewer != nil {
		activationErr = RecordActivity(cacheCtx, params.Viewer)
	}
	conn := db.GetDB().WithContext(ctx)
	key := TimelineKey(timeline)
	databaseOnly, _ := params.Other["database_only"].(bool)
	databaseOnly = databaseOnly || ForceDatabaseReads() || activationErr != nil
	fallbackAcquired := false
	defer func() {
		if fallbackAcquired {
			<-fallbackSlots
		}
	}()
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
		}
		var ids []uint64
		fromDatabase := databaseOnly
		if !databaseOnly {
			readIDs := func() ([]uint64, error) {
				min, max := params.GetRedisRangeMinMax()
				value, err := getDefaultService().eval(cacheCtx, readCandidatesScript, []string{key}, min, max, candidateLimit)
				if err != nil {
					return nil, err
				}
				values, ok := value.([]interface{})
				if !ok || len(values) == 0 || values[0] != "ready" {
					return nil, ErrNeedsRebuild
				}
				raw := make([]string, 0, len(values)-1)
				for _, id := range values[1:] {
					raw = append(raw, id.(string))
				}
				return utils.StringNumberArrayConvertUint64(raw), nil
			}
			var cacheErr error
			ids, cacheErr = readIDs()
			switch {
			case errors.Is(cacheErr, ErrNeedsRebuild):
				cacheReads.WithLabelValues("incomplete").Inc()
			case cacheErr != nil:
				cacheReads.WithLabelValues("error").Inc()
			case len(ids) == 0:
				cacheReads.WithLabelValues("empty").Inc()
			default:
				cacheReads.WithLabelValues("ready").Inc()
			}
			if cacheErr == ErrNeedsRebuild {
				cacheErr = coalesceRebuildContext(cacheCtx, key, func() error {
					if contextual, ok := timeline.(interface{ RebuildContext(context.Context) error }); ok {
						return contextual.RebuildContext(cacheCtx)
					}
					return ErrNeedsRebuild
				})
				if cacheErr == nil {
					ids, cacheErr = readIDs()
				}
			}
			if cacheErr != nil || len(ids) == 0 {
				databaseOnly = true
				fromDatabase = true
			}
		}
		if fromDatabase {
			if !fallbackAcquired {
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
			query, err := newFilterCondition(conn.Model(&models.Status{}))
			if err != nil {
				return nil, err
			}
			if params.MaxId > 0 {
				query = query.Where("statuses.id < ?", params.MaxId)
			}
			if params.MinId > 0 {
				query = query.Where("statuses.id > ?", params.MinId)
			}
			if params.SinceId > 0 {
				query = query.Where("statuses.id > ?", params.SinceId)
			}
			if err := timelineMuteFilter(query, params.Viewer).Order("statuses.id desc").Limit(candidateLimit).Pluck("statuses.id", &ids).Error; err != nil {
				return nil, err
			}
			if len(ids) == 0 {
				break
			}
		}
		var statuses models.Statuses
		var err error
		if firstBatch {
			headers := conn.Model(&models.Status{})
			if !fromDatabase {
				headers, err = newFilterCondition(headers)
			}
			if err != nil {
				return nil, err
			}
			err = timelineMuteFilter(headers, params.Viewer).Select("statuses.id", "statuses.reblog_id").Where("statuses.id IN ?", ids).Order("statuses.id DESC").Find(&statuses).Error
			if err == nil {
				statuses, err = loadTimelineStatuses(conn, statuses)
			}
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
		if afterFilter != nil {
			statuses = afterFilter(statuses)
		}
		statuses, err = models.FilterVisibleStatuses(conn, params.Viewer, statuses)
		if err != nil {
			return nil, err
		}
		filteredCandidates.Add(float64(max(0, len(ids)-len(statuses))))
		for _, status := range statuses {
			if len(output) == limit {
				break
			}
			if page.accept(status) {
				output = append(output, status)
			}
		}
		// IDs are descending in both stores. Advancing by the last candidate rather
		// than last visible result guarantees progress through inaccessible entries.
		params.MaxId = ids[len(ids)-1]
		if params.MaxId == 0 {
			break
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
	err := query.Preload("Reblog").Where("statuses.id IN ?", ids).Order("statuses.id DESC").Find(&statuses).Error
	return statuses, err
}

// A Redis outage cannot produce an unbounded number of concurrent SQL scans.
var fallbackSlots = make(chan struct{}, 16)
var ErrFeedUnavailable = errors.New("feed database fallback is at capacity")

const readCandidatesScript = `
local k=KEYS[1];local m=k..':meta'
if redis.call('HGET',m,'ready')~='1' or redis.call('HEXISTS',m,'epoch')==0 or redis.call('HEXISTS',m,'generation')==0 or redis.call('HGET',k..':versions','_epoch')~=redis.call('HGET',m,'epoch') then return {} end
if tonumber(redis.call('HGET',m,'count') or '0')>0 and redis.call('EXISTS',k)==0 then return {} end
local result={'ready'}
for _,id in ipairs(redis.call('ZREVRANGEBYSCORE',k,ARGV[2],ARGV[1],'LIMIT',0,ARGV[3])) do table.insert(result,id) end
return result
`
