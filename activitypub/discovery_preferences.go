package activitypub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func discoveryBool(value bool) *bool { return &value }

// actorDiscoveryUpdates applies only verified actor documents, never post
// metadata. Omitted fields in an incremental Update retain their old value.
// A full fetch may reset an old allow to unknown, but cannot erase a refusal.
func actorDiscoveryUpdates(account *models.Account, actor *Actor, partial bool, now time.Time) map[string]any {
	updates := make(map[string]any)
	oldIndexable, oldCollections := account.RemoteIndexable, account.RemoteHideCollections
	// Legacy true restrictive flags are explicit (unlike default false). Keep
	// these opt-outs until the origin explicitly replaces them with a boolean.
	if oldIndexable == nil && account.NoIndex {
		oldIndexable = discoveryBool(false)
	}
	if oldCollections == nil && account.HideCollections {
		oldCollections = discoveryBool(true)
	}
	fields := []struct {
		key, column, legacy string
		incoming, old       *bool
		restrictive         bool
	}{
		{"discoverable", "remote_discoverable", "discoverable", actor.Discoverable, account.RemoteDiscoverable, false},
		{"indexable", "remote_indexable", "no_index", actor.Indexable, oldIndexable, false},
		{"hideCollections", "remote_hide_collections", "hide_collections", actor.HideCollections, oldCollections, true},
	}
	complete := true
	for _, field := range fields {
		present := field.incoming != nil || actor.discoveryPresent[field.key]
		complete = complete && present && field.incoming != nil
		if partial && !present {
			continue
		}
		value := field.incoming
		if value == nil && field.old != nil && *field.old == field.restrictive {
			value = field.old
		}
		updates[field.column] = value
		// Keep legacy API displays in step, without making their defaults proof
		// of remote consent. Recommendation reads the nullable fields instead.
		legacy := value != nil && *value
		if field.key == "indexable" {
			legacy = value != nil && !*value
		}
		updates[field.legacy] = legacy
	}
	if actor.discoveryInvalid {
		// An invalid/null field must not renew the lifetime of an old allow.
		updates["remote_discovery_checked_at"] = nil
	} else if !partial || complete {
		updates["remote_discovery_checked_at"] = &now
		updates["remote_discovery_attempted_at"] = &now
		updates["remote_discovery_source"] = "actor"
		if partial {
			updates["remote_discovery_source"] = "update"
		}
	}
	return updates
}

func applyInitialActorDiscovery(account *models.Account, actor *Actor, now time.Time) {
	account.RemoteDiscoverable = actor.Discoverable
	account.RemoteIndexable = actor.Indexable
	account.RemoteHideCollections = actor.HideCollections
	account.Discoverable = actor.Discoverable != nil && *actor.Discoverable
	account.NoIndex = actor.Indexable != nil && !*actor.Indexable
	account.HideCollections = actor.HideCollections != nil && *actor.HideCollections
	account.RemoteDiscoveryAttemptedAt = &now
	account.RemoteDiscoverySource = "actor"
	if !actor.discoveryInvalid {
		account.RemoteDiscoveryCheckedAt = &now
	}
}

// Backfill tuning. Variables so tests can shrink them.
var (
	remoteDiscoveryMaxBatch     = 48
	remoteDiscoveryWorkers      = 8
	remoteDiscoveryFetchTimeout = 3 * time.Second
	remoteDiscoveryRetryAfter   = 30 * time.Minute
)

// RefreshRemoteDiscoveryPreferences is background-only, bounded maintenance for
// stale or unknown remote consent. It fetches only the canonical actor: no
// collection totals, media, or recursive resolution. Accounts that posted
// publicly in the last 7 days are checked first. Fetches run on a small fixed
// worker pool with a short per-request timeout; a remote failure (timeout,
// 5xx, invalid actor) is non-fatal: it is logged at debug, only bumps
// remote_discovery_attempted_at so the account is retried after
// remoteDiscoveryRetryAfter, and never extends the checked-at deadline. Only
// database errors are returned. Callers coordinate the worker across replicas.
func RefreshRemoteDiscoveryPreferences(ctx context.Context, conn *gorm.DB, client ActorFetcher, limit int) (int, error) {
	if conn == nil || client == nil {
		return 0, errors.New("discovery refresher requires a database and actor fetcher")
	}
	if limit <= 0 {
		limit = 8
	}
	limit = min(limit, remoteDiscoveryMaxBatch)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	now := time.Now()
	recent := "EXISTS (SELECT 1 FROM statuses WHERE statuses.account_id = accounts.id AND statuses.deleted_at IS NULL AND statuses.visibility = ? AND statuses.scheduled_at IS NULL AND statuses.created_at >= ? AND statuses.created_at <= ?)"
	var accounts []models.Account
	err := conn.WithContext(ctx).Where("domain IS NOT NULL AND origin_id <> '' AND is_ban = ?", false).
		// Keep inactive historical imports from starving authors with content
		// that can actually enter the bounded recommendation/hotspot windows.
		Where("accounts.last_activity_at >= ? OR accounts.created_at >= ? OR "+recent, now.Add(-30*24*time.Hour), now.Add(-7*24*time.Hour), models.StatusVisibilityPublic, now.Add(-30*24*time.Hour), now).
		Where("remote_discovery_checked_at IS NULL OR remote_discovery_checked_at < ?", now.Add(-models.RemoteDiscoveryMaxAge)).
		Where("remote_discovery_attempted_at IS NULL OR remote_discovery_attempted_at < ?", now.Add(-remoteDiscoveryRetryAfter)).
		// One ORDER BY expression: recent posters first, then never attempted,
		// then oldest attempt. (gorm drops an Expr order when Order() follows.)
		Clauses(clause.OrderBy{Expression: clause.Expr{SQL: "CASE WHEN " + recent + " THEN 0 ELSE 1 END ASC, CASE WHEN remote_discovery_attempted_at IS NULL THEN 0 ELSE 1 END ASC, remote_discovery_attempted_at ASC, id ASC", Vars: []any{models.StatusVisibilityPublic, now.Add(-7 * 24 * time.Hour), now}, WithoutParentheses: true}}).
		Limit(limit * 4).Find(&accounts).Error
	if err != nil {
		return 0, err
	}
	accounts = spreadRemoteDiscoveryByDomain(accounts, limit, remoteDiscoveryPerDomain)
	if len(accounts) == 0 {
		return 0, nil
	}
	// Each domain is served by one worker, in order, so one slow or
	// rate-limiting instance cannot occupy every worker or be hit in parallel.
	// Rows are claimed (attempted_at) only right before their own fetch: rows
	// left over when the cycle deadline hits, or skipped after their domain
	// failed transiently, stay eligible for the next cycle instead of being
	// pushed into the 30-minute backoff.
	byDomain := map[string][]*models.Account{}
	domains := []string{}
	for idx := range accounts {
		d := ""
		if accounts[idx].Domain != nil {
			d = *accounts[idx].Domain
		}
		if _, ok := byDomain[d]; !ok {
			domains = append(domains, d)
		}
		byDomain[d] = append(byDomain[d], &accounts[idx])
	}
	var (
		mu       sync.Mutex
		failures []error
		wg       sync.WaitGroup
		stats    remoteDiscoveryStats
	)
	stats.failedDomains = map[string]string{}
	jobs := make(chan string)
	for range max(1, remoteDiscoveryWorkers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for domain := range jobs {
				for _, account := range byDomain[domain] {
					if ctx.Err() != nil {
						return
					}
					outcome, err := refreshOneRemoteDiscovery(ctx, conn, client, account)
					mu.Lock()
					stats.add(outcome)
					if err != nil {
						failures = append(failures, err)
					}
					if outcome.transient {
						stats.failedDomains[domain] = outcome.reason
					}
					mu.Unlock()
					if err != nil || outcome.transient {
						break // leave the rest of this domain unclaimed for later
					}
				}
			}
		}()
	}
	for _, domain := range domains {
		select {
		case jobs <- domain:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	stats.log(len(accounts))
	return stats.attempted, errors.Join(failures...)
}

var remoteDiscoveryPerDomain = 6

// spreadRemoteDiscoveryByDomain keeps the priority order but caps rows per
// domain so a single large instance cannot fill the whole batch.
func spreadRemoteDiscoveryByDomain(accounts []models.Account, limit, perDomain int) []models.Account {
	out := make([]models.Account, 0, min(limit, len(accounts)))
	counts := map[string]int{}
	for _, a := range accounts {
		d := ""
		if a.Domain != nil {
			d = *a.Domain
		}
		if counts[d] >= perDomain {
			continue
		}
		counts[d]++
		out = append(out, a)
		if len(out) == limit {
			break
		}
	}
	return out
}

type remoteDiscoveryOutcome struct {
	checked, invalid, transient bool
	reason                      string
}

type remoteDiscoveryStats struct {
	attempted, checked, invalid, failed int
	failedDomains                       map[string]string
}

func (s *remoteDiscoveryStats) add(o remoteDiscoveryOutcome) {
	s.attempted++
	switch {
	case o.checked:
		s.checked++
	case o.invalid:
		s.invalid++
	default:
		s.failed++
	}
}

// log emits one summary per cycle so a stalled backfill is visible without
// enabling debug logging.
func (s *remoteDiscoveryStats) log(selected int) {
	if s.attempted == 0 && selected == 0 {
		return
	}
	domains := make([]string, 0, len(s.failedDomains))
	for d, reason := range s.failedDomains {
		if len(domains) == 8 {
			break
		}
		domains = append(domains, d+"("+reason+")")
	}
	log.Info("discovery: remote preference backfill selected=%d attempted=%d checked=%d invalid=%d failed=%d backed_off_domains=%v", selected, s.attempted, s.checked, s.invalid, s.failed, domains)
}

// refreshOneRemoteDiscovery claims one row and fetches its actor. Remote
// failures are expected (slow or offline instances) and only reported in the
// outcome; the returned error is for database failures.
func refreshOneRemoteDiscovery(ctx context.Context, conn *gorm.DB, client ActorFetcher, account *models.Account) (remoteDiscoveryOutcome, error) {
	if err := conn.WithContext(ctx).Model(&models.Account{}).Where("id = ?", account.Id).Update("remote_discovery_attempted_at", time.Now()).Error; err != nil {
		return remoteDiscoveryOutcome{reason: "db"}, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, remoteDiscoveryFetchTimeout)
	actor, err := client.FetchActor(fetchCtx, account.OriginId)
	cancel()
	if err != nil {
		reason := "error"
		if status := fetchStatusCode(err); status != 0 {
			reason = fmt.Sprintf("http_%d", status)
		} else if errors.Is(err, context.DeadlineExceeded) {
			reason = "timeout"
		}
		log.Debug("discovery: actor refresh for account %d failed (%s), will retry later: %v", account.Id, reason, err)
		return remoteDiscoveryOutcome{transient: isTransientFetchError(err), reason: reason}, nil
	}
	if actor == nil || actor.ID != account.OriginId || !actorTypes[actor.Type] {
		log.Debug("discovery: actor refresh for account %d returned an invalid identity", account.Id)
		return remoteDiscoveryOutcome{reason: "identity"}, nil
	}
	var outcome remoteDiscoveryOutcome
	err = conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(account, account.Id).Error; err != nil {
			return err
		}
		updates := actorDiscoveryUpdates(account, actor, false, time.Now())
		if actor.discoveryInvalid {
			// Distinguish a fetched-but-invalid document from a failed fetch.
			updates["remote_discovery_source"] = "invalid"
			outcome.invalid = true
		} else {
			outcome.checked = true
		}
		return tx.Model(account).Updates(updates).Error
	})
	return outcome, err
}
