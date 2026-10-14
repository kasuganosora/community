package activitypub

import (
	"context"
	"errors"
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
	remoteDiscoveryMaxBatch     = 32
	remoteDiscoveryWorkers      = 4
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
		Limit(limit).Find(&accounts).Error
	if err != nil {
		return 0, err
	}
	// Claim the whole batch first so a concurrent or crashed run backs off.
	ids := make([]uint64, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.Id)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := conn.WithContext(ctx).Model(&models.Account{}).Where("id IN ?", ids).Update("remote_discovery_attempted_at", time.Now()).Error; err != nil {
		return 0, err
	}
	var (
		mu       sync.Mutex
		failures []error
		wg       sync.WaitGroup
	)
	jobs := make(chan *models.Account)
	for range max(1, remoteDiscoveryWorkers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for account := range jobs {
				if err := refreshOneRemoteDiscovery(ctx, conn, client, account); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}
		}()
	}
	processed := 0
	for idx := range accounts {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- &accounts[idx]:
			processed++
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return processed, errors.Join(failures...)
}

// refreshOneRemoteDiscovery returns only database errors; remote failures are
// expected (slow or offline instances) and are retried on a later run.
func refreshOneRemoteDiscovery(ctx context.Context, conn *gorm.DB, client ActorFetcher, account *models.Account) error {
	fetchCtx, cancel := context.WithTimeout(ctx, remoteDiscoveryFetchTimeout)
	actor, err := client.FetchActor(fetchCtx, account.OriginId)
	cancel()
	if err != nil {
		log.Debug("discovery: actor refresh for account %d failed, will retry later: %v", account.Id, err)
		return nil
	}
	if actor == nil || actor.ID != account.OriginId || !actorTypes[actor.Type] {
		log.Debug("discovery: actor refresh for account %d returned an invalid identity", account.Id)
		return nil
	}
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(account, account.Id).Error; err != nil {
			return err
		}
		return tx.Model(account).Updates(actorDiscoveryUpdates(account, actor, false, time.Now())).Error
	})
}
