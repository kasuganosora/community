package activitypub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// The instance actor is the server's own ActivityPub identity at
// https://<domain>/actor (type Application, like Mastodon's). Relay
// subscriptions are sent and signed by it, and relays deliver Accepts and
// forwarded posts to its inbox or to the shared /inbox.
//
// It is deliberately a separate account from system.proxy: that actor's
// /users/system.proxy id has already been published as the keyId of signed
// fetches and as the follower of proxy-followed accounts, so turning it into
// /actor would orphan those relationships. Both identities stay served.
const (
	instanceActorDisplayName = "Instance actor"
	instanceActorNote        = "Server identity used for relay subscriptions and signed fetches"
	instanceActorLockName    = "community:instance.actor"
	instanceActorLockID      = int64(0x636f6d6d6961) // "commia"
)

var (
	instanceActorMu, instanceActorEnsureMu sync.Mutex
	cachedInstanceActor                    *models.Account
)

// InstanceActorURI is the instance actor's id: <base>/actor, or the actor
// imported from the source server (federation.instance_actor_uri, e.g.
// Misskey's <base>/users/<id>) so its URI and keyId survive the migration.
func InstanceActorURI() string {
	if uri := importedActorURI(models.SettingInstanceActorURI); uri != "" {
		return uri
	}
	return defaultInstanceActorURI()
}

func defaultInstanceActorURI() string {
	return strings.TrimRight(systemsettings.GetBaseURL(), "/") + "/actor"
}

// RelayActorURI is the actor that subscribes to relays: the imported relay
// actor (Misskey relay.actor) when set, else the instance actor.
func RelayActorURI() string {
	if uri := importedActorURI(models.SettingRelayActorURI); uri != "" {
		return uri
	}
	return InstanceActorURI()
}

// importedActorURI reads an imported system actor setting; only a local
// URI under the base URL is honoured.
func importedActorURI(key string) string {
	uri := strings.TrimSpace(systemsettings.GetString(key, ""))
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	if uri == "" || base == "" || !strings.HasPrefix(uri, base+"/") {
		return ""
	}
	return uri
}

// isInstanceActor reports whether a local account is the instance actor.
func isInstanceActor(account *models.Account) bool {
	return account != nil && account.Domain == nil && account.LocalActorURI != "" && account.LocalActorURI == InstanceActorURI()
}

// instanceActorUsername is the account's username and preferredUsername.
// Like Mastodon it is the domain, which can never be registered (usernames
// only contain letters, digits and underscores), so acct:<domain>@<domain>
// resolves to it through WebFinger.
func instanceActorUsername() string {
	if domain := systemsettings.GetDomain(); domain != "" {
		return domain
	}
	return "instance.actor"
}

// EnsureInstanceActor makes sure the instance actor account and its signing
// key exist. It is idempotent and safe across concurrent replicas: creation
// runs under a database advisory/named lock (as for system.proxy) and the key
// relies on the unique account_keys.account_id index. An actor imported by a
// migration (local_actor_uri = <base>/actor) is reused as is.
func EnsureInstanceActor(ctx context.Context) (*models.Account, *models.AccountKey, error) {
	instanceActorEnsureMu.Lock()
	defer instanceActorEnsureMu.Unlock()

	account, key, err := ensureInstanceActorWithRetry(ctx, db.GetDB())
	if err != nil {
		return nil, nil, err
	}
	instanceActorMu.Lock()
	cachedInstanceActor = account
	instanceActorMu.Unlock()
	return account, key, nil
}

// getInstanceActor returns the (cached) instance actor, creating it on demand.
func getInstanceActor(ctx context.Context) (*models.Account, error) {
	instanceActorMu.Lock()
	account := cachedInstanceActor
	instanceActorMu.Unlock()
	if account != nil && account.LocalActorURI == InstanceActorURI() {
		return account, nil
	}
	account, _, err := EnsureInstanceActor(ctx)
	return account, err
}

// resetInstanceActorCache drops the cached account (tests swap databases).
func resetInstanceActorCache() {
	instanceActorMu.Lock()
	cachedInstanceActor = nil
	instanceActorMu.Unlock()
}

func ensureInstanceActorWithRetry(ctx context.Context, conn *gorm.DB) (*models.Account, *models.AccountKey, error) {
	if systemsettings.GetBaseURL() == "" {
		return nil, nil, errors.New("base URL is not configured")
	}
	var lastErr error
	for attempt := range proxyEnsureAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 50 * time.Millisecond):
			}
		}
		account, err := findOrCreateInstanceActor(ctx, conn)
		if err != nil {
			lastErr = err
			continue
		}
		key, err := localAccountKeyWith(conn.WithContext(ctx), account)
		if err != nil {
			lastErr = fmt.Errorf("ensuring instance actor signing key: %w", err)
			continue
		}
		return account, key, nil
	}
	return nil, nil, lastErr
}

func findInstanceActor(tx *gorm.DB) (*models.Account, error) {
	var account models.Account
	if err := localActorQuery(tx, InstanceActorURI()).Order("id ASC").First(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func findOrCreateInstanceActor(ctx context.Context, conn *gorm.DB) (*models.Account, error) {
	conn = conn.WithContext(ctx)
	account, err := findInstanceActor(conn)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("querying instance actor: %w", err)
	}
	err = withNamedCreationLock(ctx, conn, instanceActorLockName, instanceActorLockID, func(tx *gorm.DB) error {
		if existing, err := findInstanceActor(tx); err == nil {
			account = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if InstanceActorURI() != defaultInstanceActorURI() {
			// Never mint a new identity under an imported actor's URI.
			return fmt.Errorf("imported instance actor %s (%s) not found", InstanceActorURI(), models.SettingInstanceActorURI)
		}
		created := &models.Account{
			Username:      instanceActorUsername(),
			DisplayName:   instanceActorDisplayName,
			Note:          instanceActorNote,
			LocalActorURI: InstanceActorURI(),
			Bot:           true,
			Locked:        true,
		}
		if err := tx.Create(created).Error; err != nil {
			return fmt.Errorf("creating instance actor: %w", err)
		}
		// Accounts with a local_actor_uri never get a key generated lazily
		// (that would silently change an imported identity), so the new
		// actor's keypair is created together with it.
		key, err := newAccountKey(created.Id)
		if err != nil {
			return err
		}
		if err := tx.Create(key).Error; err != nil {
			return fmt.Errorf("creating instance actor key: %w", err)
		}
		account = created
		log.Info("Created instance actor %s (id=%d)", created.LocalActorURI, created.Id)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// instanceActorSigningClient signs requests as <base>/actor.
func instanceActorSigningClient(ctx context.Context) (*Client, error) {
	account, err := getInstanceActor(ctx)
	if err != nil {
		return nil, err
	}
	return localAccountClient(account, InstanceActorURI())
}
