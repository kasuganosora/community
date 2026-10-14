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
		updates["remote_discovery_source"] = "invalid"
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
	query := conn.WithContext(ctx).Where("domain IS NOT NULL AND origin_id <> '' AND is_ban = ?", false).
		// Keep inactive historical imports from starving authors with content
		// that can actually enter the bounded recommendation/hotspot windows.
		Where("accounts.last_activity_at >= ? OR accounts.created_at >= ? OR "+recent, now.Add(-30*24*time.Hour), now.Add(-7*24*time.Hour), models.StatusVisibilityPublic, now.Add(-30*24*time.Hour), now).
		Where("remote_discovery_checked_at IS NULL OR remote_discovery_checked_at < ?", now.Add(-models.RemoteDiscoveryMaxAge)).
		Where("remote_discovery_attempted_at IS NULL OR remote_discovery_attempted_at < ?", now.Add(-remoteDiscoveryRetryAfter)).
		// One ORDER BY expression: recent posters first, then never attempted,
		// then oldest attempt. (gorm drops an Expr order when Order() follows.)
		Clauses(clause.OrderBy{Expression: clause.Expr{SQL: "CASE WHEN " + recent + " THEN 0 ELSE 1 END ASC, CASE WHEN remote_discovery_attempted_at IS NULL THEN 0 ELSE 1 END ASC, remote_discovery_attempted_at ASC, id ASC", Vars: []any{models.StatusVisibilityPublic, now.Add(-7 * 24 * time.Hour), now}, WithoutParentheses: true}})
	// The first bounded read can consist entirely of one large instance.
	// Refill from unseen domains rather than repeatedly starving everything
	// beyond that prefix. Both rounds and rows stay bounded by the batch size;
	// every read also shares the overall cycle deadline.
	perDomain := max(1, remoteDiscoveryPerDomain)
	var accounts []models.Account
	var seenDomains []string
	for range (limit + perDomain - 1) / perDomain {
		batchQuery := query.Session(&gorm.Session{})
		if len(seenDomains) != 0 {
			batchQuery = batchQuery.Where("domain NOT IN ?", seenDomains)
		}
		var candidates []models.Account
		if err := batchQuery.Limit(limit * 4).Find(&candidates).Error; err != nil {
			return 0, err
		}
		accounts = append(accounts, spreadRemoteDiscoveryByDomain(candidates, limit-len(accounts), perDomain)...)
		if len(accounts) == limit || len(candidates) < limit*4 {
			break
		}
		seen := map[string]bool{}
		for _, account := range candidates {
			if account.Domain != nil && !seen[*account.Domain] {
				seen[*account.Domain] = true
				seenDomains = append(seenDomains, *account.Domain)
			}
		}
	}
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
		// failed fetches, backed off together after the cycle
		attemptedIDs []uint64
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
					if outcome.markAttempted {
						attemptedIDs = append(attemptedIDs, account.Id)
					}
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
	if err := markRemoteDiscoveryAttempted(ctx, conn, attemptedIDs, time.Now()); err != nil {
		failures = append(failures, err)
	}
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
	checked, invalid, transient, superseded bool
	reason                                  string
	// markAttempted: the fetch failed and the row still needs its
	// attempted_at bumped (successful fetches store it with the result).
	markAttempted bool
}

// markRemoteDiscoveryAttempted backs off every failed row of a cycle with one
// narrow statement: UPDATE accounts SET remote_discovery_attempted_at = ?
// WHERE id IN (...). UpdateColumn skips hooks and does not touch updated_at,
// so only that one (unindexed) column changes and PostgreSQL can use a HOT
// update. It runs after all network fetches, outside any transaction.
func markRemoteDiscoveryAttempted(ctx context.Context, conn *gorm.DB, ids []uint64, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return conn.WithContext(ctx).Model(&models.Account{}).Where("id IN ?", ids).UpdateColumn("remote_discovery_attempted_at", now).Error
}

type remoteDiscoveryStats struct {
	attempted, checked, invalid, failed, superseded int
	failedDomains                                   map[string]string
}

func (s *remoteDiscoveryStats) add(o remoteDiscoveryOutcome) {
	s.attempted++
	switch {
	case o.checked:
		s.checked++
	case o.invalid:
		s.invalid++
	case o.superseded:
		s.superseded++
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
	log.Info("discovery: remote preference backfill selected=%d attempted=%d checked=%d invalid=%d failed=%d superseded=%d backed_off_domains=%v", selected, s.attempted, s.checked, s.invalid, s.failed, s.superseded, domains)
}

// refreshOneRemoteDiscovery fetches one row's actor. It writes nothing before
// the fetch: a successful fetch records attempted_at together with the result
// in one UPDATE, and a failed one is reported back (outcome.markAttempted) so
// the caller backs off all failed rows of the cycle with a single statement. Remote
// failures are expected (slow or offline instances) and only reported in the
// outcome; the returned error is for database failures.
func refreshOneRemoteDiscovery(ctx context.Context, conn *gorm.DB, client ActorFetcher, account *models.Account) (remoteDiscoveryOutcome, error) {
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
		return remoteDiscoveryOutcome{transient: isTransientFetchError(err), reason: reason, markAttempted: true}, nil
	}
	if actor == nil || actor.ID != account.OriginId || !actorTypes[actor.Type] {
		log.Debug("discovery: actor refresh for account %d returned an invalid identity", account.Id)
		return remoteDiscoveryOutcome{reason: "identity", markAttempted: true}, nil
	}
	var outcome remoteDiscoveryOutcome
	err = conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, account.Id).Error; err != nil {
			return err
		}
		// A verified inbox Update or profile refresh may finish while this
		// network request is in flight. Its newer privacy choice wins over
		// the stale document, including partial Updates which do not renew
		// checked_at. The attempt marker itself must not change updated_at.
		if !sameRemoteDiscoveryState(account, &current) {
			outcome.superseded = true
			outcome.markAttempted = true
			return nil
		}
		now := time.Now()
		updates := actorDiscoveryUpdates(&current, actor, false, now)
		// Store the attempt with the result, including invalid documents.
		updates["remote_discovery_attempted_at"] = &now
		if actor.discoveryInvalid {
			// Distinguish a fetched-but-invalid document from a failed fetch.
			updates["remote_discovery_source"] = "invalid"
			outcome.invalid = true
		} else {
			outcome.checked = true
		}
		return tx.Model(&current).Updates(updates).Error
	})
	if err != nil {
		return remoteDiscoveryOutcome{reason: "db", markAttempted: true}, err
	}
	return outcome, nil
}

// Compare nullable values rather than pointer identities. Attempt timestamps
// are deliberately excluded: claiming the row is not a privacy change.
func sameRemoteDiscoveryState(before, current *models.Account) bool {
	return before.OriginId == current.OriginId && before.IsBan == current.IsBan &&
		sameDiscoveryTime(before.UpdatedAt, current.UpdatedAt) &&
		sameDiscoveryTime(before.RemoteDiscoveryCheckedAt, current.RemoteDiscoveryCheckedAt) &&
		before.RemoteDiscoverySource == current.RemoteDiscoverySource &&
		sameDiscoveryBool(before.RemoteDiscoverable, current.RemoteDiscoverable) &&
		sameDiscoveryBool(before.RemoteIndexable, current.RemoteIndexable) &&
		sameDiscoveryBool(before.RemoteHideCollections, current.RemoteHideCollections) &&
		before.Discoverable == current.Discoverable && before.NoIndex == current.NoIndex &&
		before.HideCollections == current.HideCollections
}

func sameDiscoveryTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func sameDiscoveryBool(a, b *bool) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
