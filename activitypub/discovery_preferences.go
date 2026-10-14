package activitypub

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// RefreshRemoteDiscoveryPreferences is background-only, bounded maintenance for
// stale or unknown remote consent. It fetches only the canonical actor: no
// collection totals, media, or recursive resolution. A failed attempt does not
// extend the checked-at deadline. Callers coordinate the worker across replicas.
func RefreshRemoteDiscoveryPreferences(ctx context.Context, conn *gorm.DB, client ActorFetcher, limit int) (int, error) {
	if conn == nil || client == nil {
		return 0, errors.New("discovery refresher requires a database and actor fetcher")
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 16 {
		limit = 16
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	now := time.Now()
	var accounts []models.Account
	err := conn.WithContext(ctx).Where("domain IS NOT NULL AND origin_id <> '' AND is_ban = ?", false).
		// Keep inactive historical imports from starving authors with content
		// that can actually enter the bounded recommendation/hotspot windows.
		Where("accounts.last_activity_at >= ? OR accounts.created_at >= ? OR EXISTS (SELECT 1 FROM statuses WHERE statuses.account_id = accounts.id AND statuses.deleted_at IS NULL AND statuses.visibility = ? AND statuses.scheduled_at IS NULL AND statuses.created_at >= ? AND statuses.created_at <= ?)", now.Add(-30*24*time.Hour), now.Add(-7*24*time.Hour), models.StatusVisibilityPublic, now.Add(-30*24*time.Hour), now).
		Where("remote_discovery_checked_at IS NULL OR remote_discovery_checked_at < ?", now.Add(-models.RemoteDiscoveryMaxAge)).
		Where("remote_discovery_attempted_at IS NULL OR remote_discovery_attempted_at < ?", now.Add(-30*time.Minute)).
		Order("CASE WHEN remote_discovery_attempted_at IS NULL THEN 0 ELSE 1 END ASC").
		Order("remote_discovery_attempted_at ASC").Order("id ASC").Limit(limit).Find(&accounts).Error
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures []error
	for idx := range accounts {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		account := &accounts[idx]
		attemptedAt := time.Now()
		if err := conn.WithContext(ctx).Model(account).Update("remote_discovery_attempted_at", &attemptedAt).Error; err != nil {
			failures = append(failures, err)
			break
		}
		processed++
		fetchCtx, fetchCancel := context.WithTimeout(ctx, 4*time.Second)
		actor, fetchErr := client.FetchActor(fetchCtx, account.OriginId)
		fetchCancel()
		if fetchErr != nil {
			failures = append(failures, fmt.Errorf("refresh discovery account %d: %w", account.Id, fetchErr))
			continue
		}
		if actor == nil || actor.ID != account.OriginId || !actorTypes[actor.Type] {
			failures = append(failures, fmt.Errorf("refresh discovery account %d: invalid actor identity", account.Id))
			continue
		}
		if err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(account, account.Id).Error; err != nil {
				return err
			}
			return tx.Model(account).Updates(actorDiscoveryUpdates(account, actor, false, time.Now())).Error
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return processed, errors.Join(failures...)
}
