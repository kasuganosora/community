package activitypub

import (
	"context"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/db/dialect"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ActorFetcher fetches remote actors and collections; *Client from
// NewFetchClient (signed retry on authorized-fetch servers) implements it.
type ActorFetcher interface {
	FetchActor(ctx context.Context, actorURI string) (*Actor, error)
	FetchObject(ctx context.Context, objectURI string) (map[string]any, error)
}

var (
	// ErrActorGone means the remote server answered 404/410 or a Tombstone.
	ErrActorGone = errors.New("remote actor is gone")
	// ErrNotRemoteAccount means the account has no ActivityPub actor URI.
	ErrNotRemoteAccount = errors.New("not a remote account")
)

// RefreshRemoteAccount re-fetches account's actor document and stores what
// a fresh resolve would: display name, bio, avatar/header references,
// locked/bot flags, profile fields, custom emoji, and (synchronously) the
// followers/following/statuses totals from the actor's collections. With
// dryRun the actor is only fetched and validated.
func RefreshRemoteAccount(ctx context.Context, conn *gorm.DB, client ActorFetcher, account *models.Account, dryRun bool) error {
	if account == nil || account.Domain == nil || account.OriginId == "" {
		return ErrNotRemoteAccount
	}
	uri := account.OriginId
	actor, err := client.FetchActor(ctx, uri)
	if err != nil {
		if status := fetchStatusCode(err); status == http.StatusNotFound || status == http.StatusGone {
			return ErrActorGone
		}
		return fmt.Errorf("fetching actor: %w", err)
	}
	if actor == nil {
		return errors.New("empty actor response")
	}
	if actor.Type == "Tombstone" {
		return ErrActorGone
	}
	// A stored identity is never re-pointed at another document.
	if actor.ID != uri {
		return fmt.Errorf("actor id %q does not match %q", actor.ID, uri)
	}
	if !actorTypes[actor.Type] {
		return fmt.Errorf("object is not an actor: %s", actor.Type)
	}
	if dryRun {
		return nil
	}
	if err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		dialect.AsyncCommit(tx) // remote profile refresh
		return applyActorProfile(tx, account, actor)
	}); err != nil {
		return fmt.Errorf("updating account: %w", err)
	}
	if err := syncRemoteCollectionCounts(ctx, conn.WithContext(ctx), client, account.Id, actor); err != nil {
		return fmt.Errorf("updating collection totals: %w", err)
	}
	// Decorations are cosmetic: a failure must not fail the refresh.
	if !testing.Testing() {
		if changed, err := RefreshRemoteAvatarDecorations(ctx, conn, account, true); err == nil && changed {
			invalidateProfile(ctx, account)
		}
	}
	return nil
}

// applyActorProfile writes actor's profile onto the existing account row.
func applyActorProfile(tx *gorm.DB, account *models.Account, actor *Actor) error {
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(account, account.Id).Error; err != nil {
		return err
	}
	now := time.Now()
	updates := map[string]any{
		"display_name": actor.Name,
		"note":         htmlsafe.Sanitize(actor.Summary),
		"locked":       actor.ManuallyApprovesFollowers,
		"bot":          actor.Type == "Service", //nolint:goconst
	}
	// Timestamps always differ, so they are not compared: an unchanged
	// profile costs no row write, except that the stamps are renewed once
	// they are older than profileStampRefresh (discovery freshness and
	// activity reads depend on them).
	stamps := map[string]any{
		"updated_at":       now, //nolint:goconst
		"last_activity_at": now,
	}
	for key, value := range actorDiscoveryUpdates(account, actor, false, now) {
		if key == "remote_discovery_checked_at" || key == "remote_discovery_attempted_at" {
			if value != nil {
				stamps[key] = value
				continue
			}
		}
		updates[key] = value
	}
	if stampStale(account.LastActivityAt, now) {
		updates["last_activity_at"] = now
	}
	if _, ok := stamps["remote_discovery_checked_at"]; ok && stampStale(account.RemoteDiscoveryCheckedAt, now) {
		updates["remote_discovery_checked_at"] = stamps["remote_discovery_checked_at"]
	}
	if actor.Icon != nil && actor.Icon.URL != "" {
		updates["avatar"] = actor.Icon.URL
		updates["avatar_static"] = actor.Icon.URL
	}
	if actor.Image != nil && actor.Image.URL != "" {
		updates["header"] = actor.Image.URL
		updates["header_static"] = actor.Image.URL
	}
	res := dialect.UpdateChanged(tx.Model(&models.Account{}).Where("id = ?", account.Id), updates, stamps)
	if res.Error != nil {
		return res.Error
	}
	if err := tx.First(account, account.Id).Error; err != nil {
		return err
	}
	if err := ensureAccountExtra(tx, account.Id); err != nil {
		return err
	}
	if err := applyRemoteProfileFields(tx, account.Id, actor); err != nil {
		return err
	}
	return importAccountEmojiTags(tx, account, actor.Tag)
}

// profileStampRefresh bounds how stale updated_at / last_activity_at /
// remote_discovery_checked_at may get while a remote profile is unchanged.
const profileStampRefresh = 6 * time.Hour

func stampStale(t *time.Time, now time.Time) bool {
	return t == nil || now.Sub(*t) >= profileStampRefresh
}

// ensureAccountExtra creates the accounts_extra row old remote accounts may
// lack, so fields and totals have somewhere to go.
func ensureAccountExtra(tx *gorm.DB, accountID uint64) error {
	var n int64
	if err := tx.Model(&models.AccountExtra{}).Where("account_id = ?", accountID).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return tx.Create(&models.AccountExtra{AccountId: accountID}).Error
}
