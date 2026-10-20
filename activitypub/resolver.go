package activitypub

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/langdetect"
	"gorm.io/gorm/clause"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/htmlsafe"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
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
	// noQuoteFetch stops imported posts from fetching their own quotes.
	noQuoteFetch bool
	// profileBackfill marks every status it imports as profile backfill:
	// kept out of timelines and notifications (see backfillProfile).
	profileBackfill bool
}

// Default cache TTL (30 minutes).
const defaultCacheTTL = 30 * time.Minute

// Max cache entries to prevent memory leak.
const maxCacheEntries = 10000

// NewResolver creates a new Resolver.
func NewResolver(privateKey *rsa.PrivateKey, actorID string) *Resolver {
	client := NewClient(privateKey, actorID)
	if privateKey == nil {
		client = NewFetchClient()
	}
	return &Resolver{
		client:   client,
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

	err := db.GetDB().Preload("Emojis").Where("origin_id = ?", uri).First(&account).Error
	if err == nil {
		// Check if we need to refresh
		if shouldRefreshAccount(&account) {
			go r.safeRefreshAccount(uri) //nolint:contextcheck
		}

		r.storeAccountCache(uri, &account)

		return &account, nil
	}

	// Fetch from remote. Profile URLs such as https://maid.lat/@luna or
	// https://mastodon.social/@Gargron return the actor whose canonical id
	// differs from the requested URL; follow it to the canonical id.
	actor, err := r.fetchCanonicalActor(ctx, uri)
	if err != nil {
		return nil, err
	}
	if actor.ID != uri {
		if err := db.GetDB().Preload("Emojis").Where("origin_id = ?", actor.ID).First(&account).Error; err == nil {
			r.storeAccountCache(uri, &account)
			return &account, nil
		}
	}

	// Create account from actor
	account, err = r.createAccountFromActor(actor)
	if err != nil {
		return nil, fmt.Errorf("creating account: %w", err)
	}

	r.storeAccountCache(uri, &account)

	return &account, nil
}

// actorTypes are the ActivityStreams actor types we accept as accounts.
var actorTypes = map[string]bool{"Person": true, "Service": true, "Application": true, "Group": true, "Organization": true}

// fetchCanonicalActor fetches uri as an actor. When the document's id differs
// from uri, the id is fetched again and must identify itself, so a server can
// only vouch for actors it actually hosts.
func (r *Resolver) fetchCanonicalActor(ctx context.Context, uri string) (*Actor, error) {
	actor, err := r.client.FetchActor(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("fetching actor: %w", err)
	}
	if actor.ID != "" && actor.ID != uri {
		if err := validateFederationURI(actor.ID); err != nil {
			return nil, fmt.Errorf("actor ID: %w", err)
		}
		canonical := actor.ID
		actor, err = r.client.FetchActor(ctx, canonical)
		if err != nil {
			return nil, fmt.Errorf("fetching canonical actor: %w", err)
		}
		if actor.ID != canonical {
			return nil, errors.New("actor ID does not match requested URI")
		}
	}
	if actor.ID == "" {
		return nil, errors.New("actor has no id")
	}
	if !actorTypes[actor.Type] {
		return nil, fmt.Errorf("object is not an actor: %s", actor.Type)
	}
	return actor, nil
}

// resolveAccountByWebFinger resolves an account using WebFinger.
func (r *Resolver) resolveAccountByWebFinger(ctx context.Context, username, domain string) (*models.Account, error) {
	// Check database first
	var account models.Account

	err := db.GetDB().Where("LOWER(username) = LOWER(?) AND LOWER(domain) = LOWER(?)", username, domain).First(&account).Error
	if err == nil {
		if shouldRefreshAccount(&account) {
			go r.safeRefreshAccount(account.OriginId) //nolint:contextcheck
		}

		return &account, nil
	}

	// Use WebFinger to discover the actor URI
	acct := fmt.Sprintf("%s@%s", username, domain)

	actor, err := DiscoverActorContext(ctx, acct)
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

	err := db.GetDB().Preload("Content").Preload("Emojis").Preload("Account").Preload("Account.Emojis").Where("origin_uri = ? OR origin_url = ?", uri, uri).First(&status).Error
	if err == nil {
		r.storeStatusCache(uri, &status)

		return &status, nil
	}

	// Fetch from remote, following a differing canonical id (Mastodon's
	// https://host/@user/123 web URLs serve the object
	// https://host/users/user/statuses/123).
	obj, canonical, err := r.fetchCanonicalObject(ctx, uri)
	if err != nil {
		return nil, err
	}
	if canonical != uri {
		if err := db.GetDB().Preload("Content").Preload("Emojis").Preload("Account").Preload("Account.Emojis").Where("origin_uri = ?", canonical).First(&status).Error; err == nil {
			r.storeStatusCache(uri, &status)
			return &status, nil
		}
	}

	// Create status from object
	status, err = r.createStatusFromObject(obj, canonical) //nolint:contextcheck // internal call chain
	if err != nil {
		return nil, fmt.Errorf("creating status: %w", err)
	}

	r.storeStatusCache(uri, &status)

	return &status, nil
}

// fetchCanonicalObject fetches uri and, when the returned id differs, fetches
// that id and requires it to identify itself.
func (r *Resolver) fetchCanonicalObject(ctx context.Context, uri string) (map[string]any, string, error) {
	obj, err := r.client.FetchObject(ctx, uri)
	if err != nil {
		return nil, "", fmt.Errorf("fetching object: %w", err)
	}
	id, _ := obj["id"].(string)
	if id == "" {
		return nil, "", errors.New("remote object has no id")
	}
	if id == uri {
		return obj, uri, nil
	}
	if err := validateFederationURI(id); err != nil {
		return nil, "", fmt.Errorf("object id: %w", err)
	}
	obj, err = r.client.FetchObject(ctx, id)
	if err != nil {
		return nil, "", fmt.Errorf("fetching canonical object: %w", err)
	}
	if again, _ := obj["id"].(string); again != id {
		return nil, "", errors.New("remote object identity mismatch")
	}
	return obj, id, nil
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

	addLocationEmojiTags(ctx, actor)
	if err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		return applyActorProfile(tx, &account, actor)
	}); err != nil {
		log.Error("Failed to update account %s: %v", uri, err)
	} else {
		scheduleRemoteCollectionCounts(account.Id, actor)
		scheduleRemoteAvatarDecorations(account.Id, false)
	}

	log.Info("Refreshed remote account %s", uri)
}

// createAccountFromActor creates a local account record from an ActivityPub Actor.
func (r *Resolver) createAccountFromActor(actor *Actor) (models.Account, error) {
	return createRemoteAccount(db.GetDB(), actor)
}
func createRemoteAccount(conn *gorm.DB, actor *Actor) (models.Account, error) {
	if !testing.Testing() {
		ectx, cancel := context.WithTimeout(context.Background(), 2*locationEmojiTimeout)
		addLocationEmojiTags(ectx, actor)
		cancel()
	}
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
		Locked:      actor.ManuallyApprovesFollowers,
		Bot:         actor.Type == "Service", //nolint:goconst
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	applyInitialActorDiscovery(&account, actor, now)
	if actor.IsCat != nil {
		account.IsCat = *actor.IsCat
	}

	account.Location = remoteLocation(actor)
	account.Birthday = remoteBirthday(actor)

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

	err := conn.Transaction(func(tx *gorm.DB) error {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(uri)))
		identity := models.RemoteActorIdentity{URIHash: digest, URI: uri}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&identity, "uri_hash = ?", digest).Error; err != nil {
			return err
		}
		if identity.URI != uri {
			return errors.New("remote actor identity collision")
		}
		if identity.AccountID != nil {
			return tx.First(&account, *identity.AccountID).Error
		}
		// Adopt a pre-migration account, never create a second identity for its URI.
		var existing models.Account
		err := tx.Where("origin_id = ? AND domain IS NOT NULL", uri).First(&existing).Error
		if err == nil {
			account = existing
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&account).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		if err := tx.Model(&identity).Update("account_id", account.Id).Error; err != nil {
			return err
		}
		if err := applyRemoteProfileFields(tx, account.Id, actor); err != nil {
			return err
		}
		return importAccountEmojiTags(tx, &account, actor.Tag)
	})
	if err != nil {
		return models.Account{}, fmt.Errorf("creating account in database: %w", err)
	}
	scheduleRemoteCollectionCounts(account.Id, actor)
	scheduleRemoteAvatarDecorations(account.Id, false)

	return account, nil
}

// createStatusFromObject creates a local status record from an ActivityPub object.
func (r *Resolver) createStatusFromObject(obj map[string]any, uri string) (models.Status, error) {
	objectType, _ := obj["type"].(string)
	if !statusObjectTypes[objectType] {
		return models.Status{}, fmt.Errorf("object is not a Note: %s", objectType)
	}

	// Get author account
	attributedTo := firstActorID(obj["attributedTo"])
	if attributedTo == "" {
		return models.Status{}, errors.New("no attributedTo in object")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	account, err := r.ResolveAccount(ctx, attributedTo)
	if err != nil {
		return models.Status{}, fmt.Errorf("resolving author account: %w", err)
	}

	if obj["id"] != uri || !sameURIOrigin(uri, attributedTo) {
		return models.Status{}, errors.New("remote object identity mismatch")
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

	recipients, err := recipientAccounts(ctx, obj, getVisibilityFromActivity(obj) <= models.StatusVisibilityUnlisted)
	if err != nil {
		return models.Status{}, err
	}
	// Create status
	now := time.Now()
	status := models.Status{
		AccountId:          account.Id,
		ToAccounts:         recipients,
		Content:            models.StatusContent{Content: content, SpoilerText: summary},
		Sensitive:          sensitive,
		Visibility:         getVisibilityFromActivity(obj),
		OriginUri:          &uri,
		InReplyToId:        inReplyToID,
		InReplyToAccountId: inReplyToAccountID,
		QuoteId:            knownQuoteID(ctx, obj),
		Language:           langdetect.FromContentMap(obj),
		Domain:             &domain,
		CreatedAt:          publishedAt,
		UpdatedAt:          &now,
	}

	if status.CreatedAt == nil {
		status.CreatedAt = &now
	}
	status.Poll = questionPoll(obj, account.Id)
	if r.profileBackfill {
		status.MarkProfileBackfill()
	} else if models.ShouldBackfill(*status.CreatedAt, now, true) {
		status.MarkBackfilled()
	}

	status.Attachments, err = incomingAttachments(ctx, obj["attachment"], account.Id)
	if err != nil {
		return models.Status{}, err
	}
	status.Sensitive = deriveSensitive(sensitive, summary, status.Attachments)
	err = db.GetDB().WithContext(models.WithStructuredMentions(ctx)).Transaction(func(tx *gorm.DB) error {
		err := persistIncomingStatusWithEmoji(ctx, tx, &status, obj["tag"])
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return models.Status{}, fmt.Errorf("creating status in database: %w", err)
	}

	scheduleQuoteFetch(r, &status, obj)

	return status, nil
}

// statusObjectTypes are the object types stored as statuses when resolved.
var statusObjectTypes = map[string]bool{"Note": true, "Question": true, "Article": true, "Page": true}

// firstActorID returns the actor id from an attributedTo value, which may be a
// string, an object with an id, or an array of those (PeerTube, Lemmy).
func firstActorID(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		id, _ := v["id"].(string)
		return id
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t != "" && !actorTypes[t] {
					continue
				}
			}
			if id := firstActorID(item); id != "" {
				return id
			}
		}
	}
	return ""
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
		// Unsigned fetches fall back to a signed one (NewFetchClient).
		defaultResolver = NewResolver(nil, InstanceActorURI())
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
