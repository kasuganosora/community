package activitypub

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/htmlsafe"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

// CacheEntry wraps a cached value with expiration time.
type CacheEntry struct {
	value     any
	expiresAt time.Time
}

// IsExpired checks if the cache entry has expired.
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.expiresAt)
}

// Resolver handles resolution of remote ActivityPub resources.
type Resolver struct {
	client       *Client
	accountCache sync.Map // Cache for remote accounts with expiration
	statusCache  sync.Map // Cache for remote statuses with expiration
	cacheTTL     time.Duration
}

// Default cache TTL (30 minutes).
const defaultCacheTTL = 30 * time.Minute

// Max cache entries to prevent memory leak.
const maxCacheEntries = 10000

// NewResolver creates a new Resolver.
func NewResolver(privateKey *rsa.PrivateKey, actorID string) *Resolver {
	return &Resolver{
		client:   NewClient(privateKey, actorID),
		cacheTTL: defaultCacheTTL,
	}
}

// storeAccountCache stores an account in cache with expiration.
func (r *Resolver) storeAccountCache(key string, account *models.Account) {
	entry := &CacheEntry{
		value:     account,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	r.accountCache.Store(key, entry)
	r.cleanupCacheIfNeeded()
}

// loadAccountCache loads an account from cache if not expired.
func (r *Resolver) loadAccountCache(key string) (*models.Account, bool) {
	if cached, ok := r.accountCache.Load(key); ok {
		if entry, ok := cached.(*CacheEntry); ok {
			if !entry.IsExpired() {
				if account, ok := entry.value.(*models.Account); ok {
					return account, true
				}
			}
			// Expired entry, delete it
			r.accountCache.Delete(key)
		}
	}

	return nil, false
}

// storeStatusCache stores a status in cache with expiration.
func (r *Resolver) storeStatusCache(key string, status *models.Status) {
	entry := &CacheEntry{
		value:     status,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	r.statusCache.Store(key, entry)
	r.cleanupCacheIfNeeded()
}

// loadStatusCache loads a status from cache if not expired.
func (r *Resolver) loadStatusCache(key string) (*models.Status, bool) {
	if cached, ok := r.statusCache.Load(key); ok {
		if entry, ok := cached.(*CacheEntry); ok {
			if !entry.IsExpired() {
				if status, ok := entry.value.(*models.Status); ok {
					return status, true
				}
			}
			// Expired entry, delete it
			r.statusCache.Delete(key)
		}
	}

	return nil, false
}

// cleanupCacheIfNeeded removes expired entries if cache is too large.
func (r *Resolver) cleanupCacheIfNeeded() {
	// Count entries (approximate)
	count := 0

	r.accountCache.Range(func(key, value any) bool {
		count++
		if count > maxCacheEntries {
			// Remove expired entries
			if entry, ok := value.(*CacheEntry); ok && entry.IsExpired() {
				r.accountCache.Delete(key)
			}
		}

		return true
	})
}

// ResolveAccount resolves a remote account by URI or username@domain.
func (r *Resolver) ResolveAccount(ctx context.Context, identifier string) (*models.Account, error) {
	// Check if it's a URI
	if strings.HasPrefix(identifier, "http://") || strings.HasPrefix(identifier, "https://") {
		return r.resolveAccountByURI(ctx, identifier)
	}

	// Check if it's username@domain format
	if strings.Contains(identifier, "@") {
		parts := strings.Split(identifier, "@")
		if len(parts) == 2 {
			return r.resolveAccountByWebFinger(ctx, parts[0], parts[1])
		}
	}

	return nil, fmt.Errorf("invalid account identifier: %s", identifier)
}

// resolveAccountByURI resolves an account by its ActivityPub URI.
func (r *Resolver) resolveAccountByURI(ctx context.Context, uri string) (*models.Account, error) {
	// Check local cache first
	if account, ok := r.loadAccountCache(uri); ok {
		return account, nil
	}

	// Check database first
	var account models.Account

	err := db.GetDB().Where("origin_id = ?", uri).First(&account).Error
	if err == nil {
		// Check if we need to refresh
		if shouldRefreshAccount(&account) {
			go r.safeRefreshAccount(uri) //nolint:contextcheck
		}

		r.storeAccountCache(uri, &account)

		return &account, nil
	}

	// Fetch from remote
	actor, err := r.client.FetchActor(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("fetching actor: %w", err)
	}

	// Create account from actor
	account, err = r.createAccountFromActor(actor)
	if err != nil {
		return nil, fmt.Errorf("creating account: %w", err)
	}

	r.storeAccountCache(uri, &account)

	return &account, nil
}

// resolveAccountByWebFinger resolves an account using WebFinger.
func (r *Resolver) resolveAccountByWebFinger(ctx context.Context, username, domain string) (*models.Account, error) {
	// Check database first
	var account models.Account

	err := db.GetDB().Where("username = ? AND domain = ?", username, domain).First(&account).Error
	if err == nil {
		if shouldRefreshAccount(&account) {
			go r.safeRefreshAccount(account.OriginId) //nolint:contextcheck
		}

		return &account, nil
	}

	// Use WebFinger to discover the actor URI
	acct := fmt.Sprintf("%s@%s", username, domain)

	actor, err := DiscoverActor(acct) //nolint:contextcheck
	if err != nil {
		return nil, fmt.Errorf("webfinger discovery: %w", err)
	}

	return r.resolveAccountByURI(ctx, actor.ID)
}

// ResolveStatus resolves a remote status by URI.
func (r *Resolver) ResolveStatus(ctx context.Context, uri string) (*models.Status, error) {
	// Check local cache first
	if status, ok := r.loadStatusCache(uri); ok {
		return status, nil
	}

	// Check database first
	var status models.Status

	err := db.GetDB().Preload("Content").Preload("Account").Where("origin_uri = ? OR origin_url = ?", uri, uri).First(&status).Error
	if err == nil {
		r.storeStatusCache(uri, &status)

		return &status, nil
	}

	// Fetch from remote
	obj, err := r.client.FetchObject(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("fetching object: %w", err)
	}

	// Create status from object
	status, err = r.createStatusFromObject(obj, uri) //nolint:contextcheck // internal call chain
	if err != nil {
		return nil, fmt.Errorf("creating status: %w", err)
	}

	r.storeStatusCache(uri, &status)

	return &status, nil
}

// safeRefreshAccount wraps refreshAccount with panic recovery.
func (r *Resolver) safeRefreshAccount(uri string) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic in refreshAccount for %s: %v", uri, r)
		}
	}()

	r.refreshAccount(uri)
}

// refreshAccount refreshes a remote account's information.
func (r *Resolver) refreshAccount(uri string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	actor, err := r.client.FetchActor(ctx, uri)
	if err != nil {
		log.Warn("Failed to refresh account %s: %v", uri, err)

		return
	}

	var account models.Account
	if err := db.GetDB().Where("origin_id = ?", uri).First(&account).Error; err != nil {
		return
	}

	// Update account fields
	updates := map[string]any{
		"display_name":     actor.Name,
		"note":             htmlsafe.Sanitize(actor.Summary),
		"updated_at":       time.Now(), //nolint:goconst
		"last_activity_at": time.Now(),
	}

	if actor.Icon != nil && actor.Icon.URL != "" {
		updates["avatar"] = actor.Icon.URL
		updates["avatar_static"] = actor.Icon.URL
	}

	if actor.Image != nil && actor.Image.URL != "" {
		updates["header"] = actor.Image.URL
		updates["header_static"] = actor.Image.URL
	}

	if err := db.GetDB().Model(&account).Updates(updates).Error; err != nil {
		log.Error("Failed to update account %s: %v", uri, err)
	}

	log.Info("Refreshed remote account %s", uri)
}

// createAccountFromActor creates a local account record from an ActivityPub Actor.
func (r *Resolver) createAccountFromActor(actor *Actor) (models.Account, error) {
	now := time.Now()
	uri := actor.ID
	domain := utils.ExtractDomainFromURI(uri)

	username := actor.PreferredUsername
	if username == "" {
		username = utils.ExtractUsernameFromURI(uri)
	}

	account := models.Account{
		Username:    username,
		Domain:      &domain,
		OriginId:    uri,
		DisplayName: actor.Name,
		Note:        htmlsafe.Sanitize(actor.Summary),
		Locked:      false,
		Bot:         actor.Type == "Service", //nolint:goconst
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// Set avatar
	if actor.Icon != nil && actor.Icon.URL != "" {
		account.Avatar = actor.Icon.URL
		account.AvatarStatic = actor.Icon.URL
	}

	// Set header
	if actor.Image != nil && actor.Image.URL != "" {
		account.Header = actor.Image.URL
		account.HeaderStatic = actor.Image.URL
	}

	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		err := tx.Create(&account).Error
		if err != nil {
			return err
		}

		// Create account extra
		accountExtra := models.AccountExtra{
			AccountId: account.Id,
		}

		return tx.Create(&accountExtra).Error
	})
	if err != nil {
		return models.Account{}, fmt.Errorf("creating account in database: %w", err)
	}

	return account, nil
}

// createStatusFromObject creates a local status record from an ActivityPub object.
func (r *Resolver) createStatusFromObject(obj map[string]any, uri string) (models.Status, error) {
	objectType, _ := obj["type"].(string)
	if objectType != "Note" { //nolint:goconst
		return models.Status{}, fmt.Errorf("object is not a Note: %s", objectType)
	}

	// Get author account
	attributedTo, _ := obj["attributedTo"].(string)
	if attributedTo == "" {
		return models.Status{}, errors.New("no attributedTo in object")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	account, err := r.ResolveAccount(ctx, attributedTo)
	if err != nil {
		return models.Status{}, fmt.Errorf("resolving author account: %w", err)
	}

	// Extract properties
	content, _ := obj["content"].(string)
	summary, _ := obj["summary"].(string)
	content = htmlsafe.Sanitize(content)
	summary = htmlsafe.StripTags(summary)
	publishedStr, _ := obj["published"].(string)
	inReplyTo, _ := obj["inReplyTo"].(string)
	sensitive, _ := obj["sensitive"].(bool)

	// Parse published time
	publishedAt := utils.ParseRFC3339(publishedStr)

	// Find in_reply_to status if specified
	var (
		inReplyToID        *uint64
		inReplyToAccountID *uint64
	)

	if inReplyTo != "" {
		replyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		replyStatus, err := r.ResolveStatus(replyCtx, inReplyTo)
		if err == nil {
			inReplyToID = &replyStatus.Id
			inReplyToAccountID = &replyStatus.AccountId
		}
	}

	// Set domain for remote status
	domain := utils.ExtractDomainFromURI(uri)

	// Create status
	now := time.Now()
	status := models.Status{
		AccountId:          account.Id,
		Content:            models.StatusContent{Content: content, SpoilerText: summary},
		Sensitive:          sensitive,
		Visibility:         getVisibilityFromActivity(obj),
		OriginUri:          &uri,
		InReplyToId:        inReplyToID,
		InReplyToAccountId: inReplyToAccountID,
		Domain:             &domain,
		CreatedAt:          publishedAt,
		UpdatedAt:          &now,
	}

	if status.CreatedAt == nil {
		status.CreatedAt = &now
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		err := tx.Create(&status).Error
		if err != nil {
			return err
		}

		// Create status content
		status.Content.StatusId = status.Id

		return tx.Create(&status.Content).Error
	})
	if err != nil {
		return models.Status{}, fmt.Errorf("creating status in database: %w", err)
	}

	return status, nil
}

// shouldRefreshAccount checks if a remote account should be refreshed.
func shouldRefreshAccount(account *models.Account) bool {
	if account.LastActivityAt == nil {
		return true
	}

	// Refresh if last update was more than 24 hours ago
	return time.Since(*account.LastActivityAt) > 24*time.Hour
}

// ResolveStatusWithContext resolves a remote status with reply context.
func (r *Resolver) ResolveStatusWithContext(ctx context.Context, uri string, depth int) (*models.Status, []*models.Status, error) {
	status, err := r.ResolveStatus(ctx, uri)
	if err != nil {
		return nil, nil, err
	}

	if depth <= 0 {
		return status, nil, nil
	}

	// Fetch ancestors
	ancestors := make([]*models.Status, 0)

	if status.InReplyToId != nil {
		var parentStatus models.Status
		err := db.GetDB().Preload("Content").Preload("Account").First(&parentStatus, *status.InReplyToId).Error
		if err == nil {
			ancestors = append(ancestors, &parentStatus)
		} else if status.OriginUri != nil {
			// Try to resolve remote parent
			parentCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// We'd need the inReplyTo URI to fetch the parent
			// This is a simplified version
			_ = parentCtx
		}
	}

	return status, ancestors, nil
}

// FetchRemoteFollowers fetches the followers collection of a remote account.
func (r *Resolver) FetchRemoteFollowers(ctx context.Context, account *models.Account, limit int) ([]string, error) {
	if account.OriginId == "" {
		return nil, errors.New("not a remote account")
	}

	// Get the actor to find followers URL
	actor, err := r.client.FetchActor(ctx, account.OriginId)
	if err != nil {
		return nil, fmt.Errorf("fetching actor: %w", err)
	}

	if actor.Followers == "" {
		return nil, errors.New("no followers URL")
	}

	// Fetch the followers collection
	data, err := r.client.Get(ctx, actor.Followers)
	if err != nil {
		return nil, fmt.Errorf("fetching followers: %w", err)
	}

	var collection struct {
		Type         string   `json:"type"`
		First        string   `json:"first"`
		TotalItems   int      `json:"totalItems"`
		OrderedItems []string `json:"orderedItems"`
		Items        []any    `json:"items"`
	}

	if err := json.Unmarshal(data, &collection); err != nil {
		return nil, fmt.Errorf("parsing followers: %w", err)
	}

	// If it's a paged collection, fetch the first page
	if collection.First != "" && len(collection.OrderedItems) == 0 && len(collection.Items) == 0 {
		data, err = r.client.Get(ctx, collection.First)
		if err != nil {
			return nil, fmt.Errorf("fetching followers page: %w", err)
		}

		err := json.Unmarshal(data, &collection)
		if err != nil {
			return nil, fmt.Errorf("parsing followers page: %w", err)
		}
	}

	// Extract URIs
	followers := make([]string, 0)
	followers = append(followers, collection.OrderedItems...)

	if limit > 0 && len(followers) > limit {
		followers = followers[:limit]
	}

	return followers, nil
}

// GetOrCreateRemoteAccount is a convenience function to get or create a remote account.
var defaultResolver *Resolver
var resolverOnce sync.Once

func getDefaultResolver() *Resolver {
	resolverOnce.Do(func() {
		actorID := systemsettings.GetBaseURL() + "/actor"
		defaultResolver = NewResolver(nil, actorID)
	})

	return defaultResolver
}

// GetOrCreateRemoteAccount gets or creates a remote account by URI.
func GetOrCreateRemoteAccount(uri string) (*models.Account, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return getDefaultResolver().ResolveAccount(ctx, uri)
}

// ResolveRemoteStatus resolves a remote status by URI.
func ResolveRemoteStatus(uri string) (*models.Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return getDefaultResolver().ResolveStatus(ctx, uri)
}
