package activitypub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
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
	cachedProxyAccount *models.Account
	cachedProxyKey     *models.AccountKey
	proxyOnce          sync.Once
	errProxyInit       error
)

// NewProxyService creates a new ProxyService.
func NewProxyService() *ProxyService {
	return &ProxyService{
		db: db.GetDB(),
	}
}

// GetOrCreateProxyAccount returns the system proxy account, creating it if needed.
// Results are cached after first successful call.
func (s *ProxyService) GetOrCreateProxyAccount() (*models.Account, *models.AccountKey, error) {
	proxyOnce.Do(func() {
		cachedProxyAccount, cachedProxyKey, errProxyInit = s.getOrCreateProxyAccountInternal()
	})

	if errProxyInit != nil {
		// Reset so next call retries
		proxyOnce = sync.Once{}

		return nil, nil, errProxyInit
	}

	return cachedProxyAccount, cachedProxyKey, nil
}

func (s *ProxyService) getOrCreateProxyAccountInternal() (*models.Account, *models.AccountKey, error) {
	// Try to find existing proxy account
	var account models.Account

	err := s.db.Where("username = ? AND domain IS NULL", proxyUsername).First(&account).Error
	if err == nil {
		// Account exists, get key
		var key models.AccountKey
		err := s.db.Where("account_id = ?", account.Id).First(&key).Error
		if err != nil {
			return nil, nil, fmt.Errorf("proxy account exists but key not found: %w", err)
		}

		return &account, &key, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, fmt.Errorf("querying proxy account: %w", err)
	}

	// Create proxy account with keypair in a transaction
	var (
		newAccount models.Account
		newKey     models.AccountKey
	)

	err = s.db.Transaction(func(tx *gorm.DB) error {
		newAccount = models.Account{
			Username:    proxyUsername,
			DisplayName: proxyDisplayName,
			Note:        proxyNote,
			Bot:         true,
			Locked:      true,
		}
		if err := tx.Create(&newAccount).Error; err != nil {
			return fmt.Errorf("creating proxy account: %w", err)
		}

		// Generate RSA keypair
		privateKey, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
		if err != nil {
			return fmt.Errorf("generating RSA key: %w", err)
		}

		privPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})

		pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
		if err != nil {
			return fmt.Errorf("marshaling public key: %w", err)
		}

		pubPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: pubBytes,
		})

		newKey = models.AccountKey{
			AccountID:  newAccount.Id,
			PublicKey:  string(pubPEM),
			PrivateKey: string(privPEM),
		}
		if err := tx.Create(&newKey).Error; err != nil {
			return fmt.Errorf("creating account key: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	log.Info("Created system proxy account (id=%d)", newAccount.Id)

	return &newAccount, &newKey, nil
}

// getProxyActorURI returns the ActivityPub actor URI for the proxy account.
func (s *ProxyService) getProxyActorURI() string {
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")

	return fmt.Sprintf("https://%s/users/%s", domain, proxyUsername)
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
