package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrMigrationAlias = errors.New("target account must list this account as an alias")
var ErrMigrationState = errors.New("account cannot be migrated")

// ResolveMigrationAccount accepts an exact account address or actor URL. Local
// addresses are resolved locally, never sent through remote WebFinger.
func ResolveMigrationAccount(ctx context.Context, conn *gorm.DB, address string) (*models.Account, error) {
	address = strings.TrimPrefix(strings.TrimSpace(address), "@")
	if address == "" || len(address) > 2048 {
		return nil, errors.New("invalid account address")
	}
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	if strings.HasPrefix(address, base+"/") {
		var accounts []models.Account
		name := strings.TrimPrefix(strings.TrimPrefix(address, base+"/@"), base+"/users/")
		if err := conn.Where("domain IS NULL AND (local_actor_uri = ? OR username = ?)", address, name).Find(&accounts).Error; err != nil {
			return nil, err
		}
		for _, a := range accounts {
			if a.ActorURI(base) == address || base+"/@"+a.Username == address {
				return &a, nil
			}
		}
		return nil, gorm.ErrRecordNotFound
	}
	if !strings.Contains(address, "://") {
		parts := strings.Split(address, "@")
		domain := ""
		if len(parts) == 2 {
			domain = parts[1]
		}
		baseURL, _ := url.Parse(base)
		if len(parts) == 1 || (len(parts) == 2 && (strings.EqualFold(domain, systemsettings.GetDomain()) || strings.EqualFold(domain, baseURL.Host))) {
			var a models.Account
			if err := models.WhereAcct(conn, parts[0], "", false).First(&a).Error; err != nil {
				return nil, err
			}
			return &a, nil
		}
	}
	return NewResolver(nil, "").ResolveAccount(ctx, address)
}

// VerifyMigrationTarget always re-fetches remote aliases: stale profile caches
// must never authorize transfer of another account's followers.
func VerifyMigrationTarget(ctx context.Context, conn *gorm.DB, old, target *models.Account, client ActorFetcher) error {
	if old.Id == target.Id || target.IsBan || target.DeletionRequestedAt != nil || target.MovedTo != "" {
		return ErrMigrationState
	}
	aliases := target.AlsoKnownAs
	if target.Domain != nil {
		actor, err := client.FetchActor(ctx, target.OriginId)
		if err != nil {
			return err
		}
		if actor == nil || actor.ID != target.OriginId || actor.Type == "Tombstone" || actor.MovedTo != "" {
			return ErrMigrationState
		}
		aliases = actor.Aliases
		if len(aliases) == 0 && actor.AlsoKnownAs != "" {
			aliases = []string{actor.AlsoKnownAs}
		}
	}
	oldURI := old.ActorURI(systemsettings.GetBaseURL())
	for _, alias := range aliases {
		if alias == oldURI {
			return nil
		}
	}
	return ErrMigrationAlias
}

// ApplyAccountMove runs only after VerifyMigrationTarget. Follow hooks preserve
// counters and enqueue Follow/Undo in this transaction; no network occurs here.
func ApplyAccountMove(conn *gorm.DB, oldID, targetID uint64, federate bool) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		var old, target models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, oldID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, targetID).Error; err != nil {
			return err
		}
		uri := target.ActorURI(systemsettings.GetBaseURL())
		if old.MovedTo == uri {
			return nil
		}
		if old.Id == target.Id || old.MovedTo != "" || old.IsBan || old.DeletionRequestedAt != nil || target.IsBan || target.MovedTo != "" || target.DeletionRequestedAt != nil {
			return ErrMigrationState
		}
		if target.Domain == nil {
			if err := VerifyMigrationTarget(tx.Statement.Context, tx, &old, &target, nil); err != nil {
				return err
			}
		}
		if federate {
			if old.Domain != nil {
				return ErrMigrationState
			}
			var recipients []uint64
			if err := tx.Model(&models.Follow{}).Where("follow_id = ? AND status = ? AND account_id IN (SELECT id FROM accounts WHERE domain IS NOT NULL)", old.Id, models.FollowStatusFollowed).Pluck("account_id", &recipients).Error; err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"target": uri})
			if err := tx.Create(&models.FederationOutbox{AccountID: old.Id, ObjectID: old.Id, Kind: "Move", Payload: string(payload), RecipientIDs: recipients}).Error; err != nil {
				return err
			}
		}
		var follows []models.Follow
		if err := tx.Where("follow_id = ? AND status = ? AND account_id IN (SELECT id FROM accounts WHERE domain IS NULL AND is_ban = ?)", old.Id, models.FollowStatusFollowed, false).Order("id").Find(&follows).Error; err != nil {
			return err
		}
		for _, f := range follows {
			var count int64
			if err := tx.Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?", f.AccountId, target.Id).Count(&count).Error; err != nil {
				return err
			}
			var follower models.Account
			if err := tx.First(&follower, f.AccountId).Error; err != nil {
				return err
			}
			blocked, err := models.AccountsBlocked(tx, &follower, &target)
			if err != nil {
				return err
			}
			if count == 0 && f.AccountId != target.Id && !blocked {
				state := models.FollowStatusFollowed
				if target.Domain != nil || target.Locked {
					state = models.FollowStatusRequested
				}
				follow := models.Follow{AccountId: f.AccountId, FollowId: target.Id, Status: state, Properties: map[string]any{}}
				if value, ok := f.Properties[models.FollowPropertyKeyReblogs]; ok {
					follow.Properties[models.FollowPropertyKeyReblogs] = value
				}
				if err := tx.Create(&follow).Error; err != nil {
					return err
				}
			}
			if err := tx.Delete(&f).Error; err != nil {
				return err
			}
		}
		return tx.Model(&old).UpdateColumn("moved_to", uri).Error
	})
}
