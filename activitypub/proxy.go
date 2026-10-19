package activitypub

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

const (
	proxyUsername    = "system.proxy"
	proxyDisplayName = "System Proxy"
	proxyNote        = "System proxy account for federation content delivery"
	rsaKeyBits       = 2048
)

// ProxyService manages the system proxy account that follows remote users
// on behalf of the system when no local user follows them.
type ProxyService struct {
	db *gorm.DB
}

var (
	// proxyMu guards the cached proxy identity; proxyEnsureMu serialises
	// ensures inside one process.
	proxyMu, proxyEnsureMu sync.Mutex
	cachedProxyAccount     *models.Account
	cachedProxyKey         *models.AccountKey
)

// NewProxyService creates a new ProxyService.
func NewProxyService() *ProxyService {
	return &ProxyService{
		db: db.GetDB(),
	}
}

// GetOrCreateProxyAccount returns the system proxy account, creating it if
// needed. Successful results are cached; failures are retried next call.
func (s *ProxyService) GetOrCreateProxyAccount() (*models.Account, *models.AccountKey, error) {
	proxyMu.Lock()
	account, key := cachedProxyAccount, cachedProxyKey
	proxyMu.Unlock()

	if account != nil && key != nil {
		return account, key, nil
	}

	account, key, err := s.getOrCreateProxyAccountInternal()
	if err != nil {
		return nil, nil, err
	}

	cacheProxyAccount(account, key)

	return account, key, nil
}

func cacheProxyAccount(account *models.Account, key *models.AccountKey) {
	proxyMu.Lock()
	cachedProxyAccount, cachedProxyKey = account, key
	proxyMu.Unlock()
}

// resetProxyCache drops the cached identity (tests swap databases).
func resetProxyCache() { cacheProxyAccount(nil, nil); resetSystemSigner() }

func (s *ProxyService) getOrCreateProxyAccountInternal() (*models.Account, *models.AccountKey, error) {
	return ensureProxyAccount(context.Background(), s.db)
}

// EnsureProxyAccount makes sure the system.proxy service actor and its
// signing keypair exist, so https://<domain>/users/system.proxy (the keyId
// of every request signed by the instance) resolves from the first request
// on. It is idempotent and safe to run concurrently, including from several
// replicas starting at once: creation is serialised with a database lock and
// the key relies on the unique account_keys.account_id index.
func EnsureProxyAccount(ctx context.Context) (*models.Account, *models.AccountKey, error) {
	account, key, err := ensureProxyAccount(ctx, db.GetDB())
	if err != nil {
		return nil, nil, err
	}

	cacheProxyAccount(account, key)

	return account, key, nil
}

const proxyEnsureAttempts = 5

func ensureProxyAccount(ctx context.Context, conn *gorm.DB) (*models.Account, *models.AccountKey, error) {
	proxyEnsureMu.Lock()
	defer proxyEnsureMu.Unlock()

	return ensureProxyAccountWithRetry(ctx, conn)
}

// ensureProxyAccountWithRetry is the cross-process part of the ensure (no
// in-process mutex), retrying transient lock / serialisation failures.
func ensureProxyAccountWithRetry(ctx context.Context, conn *gorm.DB) (*models.Account, *models.AccountKey, error) {
	var lastErr error

	for attempt := range proxyEnsureAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 50 * time.Millisecond):
			}
		}

		account, err := findOrCreateProxyAccount(ctx, conn)
		if err != nil {
			lastErr = err

			continue
		}

		// localAccountKey only locks the account row when the key is
		// missing and is backed by the unique account_id index, so
		// concurrent callers share one key.
		key, err := localAccountKeyWith(conn.WithContext(ctx), account)
		if err != nil {
			lastErr = fmt.Errorf("ensuring proxy signing key: %w", err)

			continue
		}

		return account, key, nil
	}

	return nil, nil, lastErr
}

// findProxyAccount returns the oldest system.proxy row: if an older release
// raced and created duplicates, everyone (including the actor endpoint,
// which also takes the first row by id) agrees on the same one.
func findProxyAccount(tx *gorm.DB) (*models.Account, error) {
	var account models.Account

	err := tx.Where("username = ? AND domain IS NULL", proxyUsername).Order("id ASC").First(&account).Error
	if err != nil {
		return nil, err
	}

	return &account, nil
}

func findOrCreateProxyAccount(ctx context.Context, conn *gorm.DB) (*models.Account, error) {
	conn = conn.WithContext(ctx)

	account, err := findProxyAccount(conn)
	if err == nil {
		return account, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("querying proxy account: %w", err)
	}

	err = withProxyCreationLock(ctx, conn, func(tx *gorm.DB) error {
		// Re-check under the lock: another process may have won.
		if existing, err := findProxyAccount(tx); err == nil {
			account = existing

			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		created := &models.Account{
			Username:    proxyUsername,
			DisplayName: proxyDisplayName,
			Note:        proxyNote,
			Bot:         true,
			Locked:      true,
		}
		if err := tx.Create(created).Error; err != nil {
			return fmt.Errorf("creating proxy account: %w", err)
		}

		account = created
		log.Info("Created system proxy account (id=%d)", created.Id)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return account, nil
}

// Cross-process lock identifiers for proxy account creation.
const (
	proxyLockName = "community:system.proxy"
	proxyLockID   = int64(0x636f6d6d7072) // "commpr"
)

// withProxyCreationLock serialises system.proxy creation across processes.
func withProxyCreationLock(ctx context.Context, conn *gorm.DB, fn func(tx *gorm.DB) error) error {
	return withNamedCreationLock(ctx, conn, proxyLockName, proxyLockID, fn)
}

// withNamedCreationLock runs fn in a transaction that holds a cross-process
// lock: a transaction-scoped advisory lock on PostgreSQL, a named lock held
// on the transaction's own connection on MySQL. SQLite already serialises
// writers per database file.
func withNamedCreationLock(ctx context.Context, conn *gorm.DB, lockName string, lockID int64, fn func(tx *gorm.DB) error) error {
	switch conn.Dialector.Name() {
	case "postgres":
		return conn.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockID).Error; err != nil {
				return fmt.Errorf("locking %s: %w", lockName, err)
			}

			return fn(tx)
		})
	case "mysql":
		sqlDB, err := conn.DB()
		if err != nil {
			return err
		}

		c, err := sqlDB.Conn(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()

		var got sql.NullInt64
		if err := c.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", lockName).Scan(&got); err != nil {
			return fmt.Errorf("locking %s: %w", lockName, err)
		}

		if !got.Valid || got.Int64 != 1 {
			return fmt.Errorf("timed out waiting for lock %s", lockName)
		}

		// Released after the transaction committed, on the same session.
		defer func() { _, _ = c.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName) }()

		session := conn.WithContext(ctx)
		session.Statement.ConnPool = c

		return session.Transaction(fn)
	default:
		return conn.Transaction(fn)
	}
}

// getProxyActorURI returns the ActivityPub actor URI for the proxy account.
func (s *ProxyService) getProxyActorURI() string {

	return systemsettings.GetBaseURL() + "/users/" + proxyUsername
}

// parsePrivateKey parses a PEM-encoded RSA private key.
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// HasLocalFollowers checks if a remote account has real local followers
// (excluding the proxy account itself).
func (s *ProxyService) HasLocalFollowers(remoteAccountID uint64) bool {
	// Two-step query to avoid JOIN issues across different DB engines:
	// 1. Get IDs of local non-proxy accounts
	// 2. Check if any of them follow the remote account
	var localAccountIDs []uint64
	s.db.Model(&models.Account{}).
		Where("domain IS NULL AND username != ?", proxyUsername).
		Pluck("id", &localAccountIDs)

	if len(localAccountIDs) == 0 {
		return false
	}

	var count int64
	s.db.Model(&models.Follow{}).
		Where("follow_id = ? AND account_id IN ? AND status = ?",
			remoteAccountID, localAccountIDs, models.FollowStatusFollowed).
		Count(&count)

	return count > 0
}

// EnsureProxyFollow ensures the proxy account follows a remote account for the given reason.
// If the remote account already has local followers, only records the reference without
// sending a Follow activity.
func (s *ProxyService) EnsureProxyFollow(remoteAccountID uint64, reason string, reasonID uint64) error {
	// Check if this exact reference already exists
	var existing models.ProxyFollow

	err := s.db.Where("account_id = ? AND reason = ? AND reason_id = ?",
		remoteAccountID, reason, reasonID).First(&existing).Error
	if err == nil {
		return nil // Already tracked
	}

	// Get proxy account
	proxyAccount, proxyKey, err := s.GetOrCreateProxyAccount()
	if err != nil {
		return fmt.Errorf("getting proxy account: %w", err)
	}

	// Get the remote account info
	var remoteAccount models.Account
	if err := s.db.First(&remoteAccount, remoteAccountID).Error; err != nil {
		return fmt.Errorf("remote account not found: %w", err)
	}

	// If account is local, skip
	if remoteAccount.Domain == nil {
		return nil
	}

	// Check if proxy already follows this account
	var existingFollow models.Follow

	alreadyFollowing := s.db.Where("account_id = ? AND follow_id = ?",
		proxyAccount.Id, remoteAccountID).First(&existingFollow).Error == nil

	// Check if real users already follow this account
	hasLocalFollowers := s.HasLocalFollowers(remoteAccountID)

	// Create the ProxyFollow reference
	proxyFollow := models.ProxyFollow{
		AccountID: remoteAccountID,
		Reason:    reason,
		ReasonID:  reasonID,
		Status:    models.ProxyFollowStatusPending,
	}

	if alreadyFollowing || hasLocalFollowers {
		// No need to send Follow activity, just record the reference
		proxyFollow.Status = models.ProxyFollowStatusAccepted
		err := s.db.Create(&proxyFollow).Error
		if err != nil {
			return fmt.Errorf("creating proxy follow record: %w", err)
		}

		return nil
	}

	// Need to actually follow — send Follow activity
	followActivityID, err := s.sendFollowActivity(proxyAccount, proxyKey, &remoteAccount)
	if err != nil {
		// Still record the reference so we can retry later
		proxyFollow.Status = models.ProxyFollowStatusRejected
		s.db.Create(&proxyFollow)

		return fmt.Errorf("sending follow activity: %w", err)
	}

	proxyFollow.FollowActivityID = followActivityID

	// Create Follow record in DB
	follow := models.Follow{
		AccountId: proxyAccount.Id,
		FollowId:  remoteAccountID,
		Status:    models.FollowStatusRequested,
	}
	if err := s.db.Create(&follow).Error; err != nil {
		return fmt.Errorf("creating follow record: %w", err)
	}

	// Save the proxy follow reference
	if err := s.db.Create(&proxyFollow).Error; err != nil {
		return fmt.Errorf("creating proxy follow record: %w", err)
	}

	log.Info("Sent proxy follow for remote account %d (%s)", remoteAccountID, remoteAccount.OriginId)

	return nil
}

// CleanupProxyFollow removes a proxy follow reference and unfollows if no more references remain.
func (s *ProxyService) CleanupProxyFollow(remoteAccountID uint64, reason string, reasonID uint64) error {
	// Delete this specific reference
	result := s.db.Where("account_id = ? AND reason = ? AND reason_id = ?",
		remoteAccountID, reason, reasonID).Delete(&models.ProxyFollow{})
	if result.Error != nil {
		return fmt.Errorf("deleting proxy follow: %w", result.Error)
	}

	// Check if any other references remain for this account
	var remaining int64
	s.db.Model(&models.ProxyFollow{}).Where("account_id = ?", remoteAccountID).Count(&remaining)

	if remaining > 0 {
		return nil // Still needed
	}

	// Check if real users still follow this account
	if s.HasLocalFollowers(remoteAccountID) {
		return nil // Real followers exist, no need to unfollow
	}

	// No more references and no real followers — unfollow
	proxyAccount, proxyKey, err := s.GetOrCreateProxyAccount()
	if err != nil {
		return fmt.Errorf("getting proxy account: %w", err)
	}

	// Find the follow record to get the activity ID
	var follow models.Follow
	if err := s.db.Where("account_id = ? AND follow_id = ?",
		proxyAccount.Id, remoteAccountID).First(&follow).Error; err != nil {
		return nil //nolint:nilerr // No follow to undo
	}

	// Get the remote account for inbox lookup
	var remoteAccount models.Account
	if err := s.db.First(&remoteAccount, remoteAccountID).Error; err != nil {
		return fmt.Errorf("remote account not found: %w", err)
	}

	// Find the original follow activity ID from any historical proxy follow
	var proxyFollow models.ProxyFollow

	followActivityID := ""
	if err := s.db.Unscoped().Where("account_id = ? AND follow_activity_id != ''",
		remoteAccountID).First(&proxyFollow).Error; err == nil {
		followActivityID = proxyFollow.FollowActivityID
	}

	// Send Undo Follow
	if err := s.sendUndoFollowActivity(proxyAccount, proxyKey, &remoteAccount, followActivityID); err != nil {
		log.Error("Failed to send undo follow for account %d: %v", remoteAccountID, err)
		// Continue to delete local record even if remote undo fails
	}

	// Delete the follow record
	s.db.Delete(&follow)
	log.Info("Proxy unfollowed remote account %d (%s)", remoteAccountID, remoteAccount.OriginId)

	return nil
}

// HandleAccept processes an Accept activity for a proxy follow.
// Returns nil if the follow activity was found and updated, error otherwise.
func (s *ProxyService) HandleAccept(followActivityID string) error {
	// Find proxy follow by activity ID
	var proxyFollow models.ProxyFollow

	err := s.db.Where("follow_activity_id = ? AND status = ?",
		followActivityID, models.ProxyFollowStatusPending).First(&proxyFollow).Error
	if err != nil {
		return fmt.Errorf("proxy follow not found: %w", err)
	}

	// Update proxy follow status
	s.db.Model(&proxyFollow).Update("status", models.ProxyFollowStatusAccepted)

	// Update all proxy follows for this account (they share the same actual follow)
	s.db.Model(&models.ProxyFollow{}).
		Where("account_id = ? AND status = ?", proxyFollow.AccountID, models.ProxyFollowStatusPending).
		Update("status", models.ProxyFollowStatusAccepted)

	// Update the actual follow record
	proxyAccount, _, err := s.GetOrCreateProxyAccount()
	if err != nil {
		return fmt.Errorf("getting proxy account: %w", err)
	}

	s.db.Model(&models.Follow{}).
		Where("account_id = ? AND follow_id = ? AND status = ?",
			proxyAccount.Id, proxyFollow.AccountID, models.FollowStatusRequested).
		Update("status", models.FollowStatusFollowed)

	log.Info("Proxy follow accepted for remote account %d", proxyFollow.AccountID)

	return nil
}

// sendFollowActivity sends a Follow activity to a remote account.
func (s *ProxyService) sendFollowActivity(
	proxyAccount *models.Account, //nolint:unparam // param reserved for future use
	proxyKey *models.AccountKey,
	remoteAccount *models.Account,
) (string, error) {
	if remoteAccount.OriginId == "" {
		return "", errors.New("remote account has no OriginId")
	}

	rsaKey, err := parsePrivateKey(proxyKey.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("parsing private key: %w", err)
	}

	proxyActorURI := s.getProxyActorURI()
	activityID := fmt.Sprintf("%s#follows/%d", proxyActorURI, time.Now().UnixNano())

	activity := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams", //nolint:goconst
		"id":       activityID,
		"type":     "Follow",               //nolint:goconst
		"actor":    proxyActorURI,          //nolint:goconst
		"object":   remoteAccount.OriginId, //nolint:goconst
	}

	// Fetch remote actor to get inbox
	client := NewClient(rsaKey, proxyActorURI)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	actor, err := client.FetchActor(ctx, remoteAccount.OriginId)
	if err != nil {
		return "", fmt.Errorf("fetching remote actor: %w", err)
	}

	inboxURL := actor.Inbox
	if inboxURL == "" {
		return "", errors.New("remote actor has no inbox")
	}

	// Send the Follow activity
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

	if err := client.Post(ctx2, inboxURL, activity); err != nil {
		return "", fmt.Errorf("posting follow activity: %w", err)
	}

	return activityID, nil
}

// sendUndoFollowActivity sends an Undo{Follow} activity to a remote account.
func (s *ProxyService) sendUndoFollowActivity(
	proxyAccount *models.Account, //nolint:unparam // param reserved for future use
	proxyKey *models.AccountKey,
	remoteAccount *models.Account,
	followActivityID string,
) error {
	if remoteAccount.OriginId == "" {
		return errors.New("remote account has no OriginId")
	}

	rsaKey, err := parsePrivateKey(proxyKey.PrivateKey)
	if err != nil {
		return fmt.Errorf("parsing private key: %w", err)
	}

	proxyActorURI := s.getProxyActorURI()
	undoID := fmt.Sprintf("%s#undo/%d", proxyActorURI, time.Now().UnixNano())

	activity := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       undoID,
		"type":     "Undo", //nolint:goconst
		"actor":    proxyActorURI,
		"object": map[string]any{
			"id":     followActivityID,
			"type":   "Follow",
			"actor":  proxyActorURI,
			"object": remoteAccount.OriginId,
		},
	}

	// Fetch remote actor to get inbox
	client := NewClient(rsaKey, proxyActorURI)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	actor, err := client.FetchActor(ctx, remoteAccount.OriginId)
	if err != nil {
		return fmt.Errorf("fetching remote actor: %w", err)
	}

	inboxURL := actor.Inbox
	if inboxURL == "" {
		return errors.New("remote actor has no inbox")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

	if err := client.Post(ctx2, inboxURL, activity); err != nil {
		return fmt.Errorf("posting undo follow activity: %w", err)
	}

	return nil
}
