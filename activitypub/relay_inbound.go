package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Relay actors are not followed accounts: they are identified by the relay
// rows ops configured. An HTTP signer is a relay when its host is the host of
// a configured relay inbox and its actor document advertises that inbox (as
// inbox or endpoints.sharedInbox).

const relayActorCacheTTL = 10 * time.Minute

type relayActorEntry struct {
	inboxes []string
	expires time.Time
}

var relayActorCache sync.Map // signer actor URI -> relayActorEntry

func resetRelayActorCache() {
	relayActorCache.Range(func(k, _ any) bool { relayActorCache.Delete(k); return true })
}

// relayActorInboxes returns the inbox URLs advertised by actorURI, fetched
// with the signed-fetch fallback and cached briefly.
func relayActorInboxes(ctx context.Context, actorURI string) ([]string, error) {
	if v, ok := relayActorCache.Load(actorURI); ok {
		if entry := v.(relayActorEntry); time.Now().Before(entry.expires) {
			return entry.inboxes, nil
		}
	}
	actor, err := NewFetchClient().FetchActor(ctx, actorURI)
	if err != nil {
		return nil, err
	}
	if actor.ID != actorURI {
		return nil, errors.New("relay actor identity mismatch")
	}
	inboxes := []string{actor.Inbox}
	if actor.Endpoints != nil && actor.Endpoints.SharedInbox != "" {
		inboxes = append(inboxes, actor.Endpoints.SharedInbox)
	}
	relayActorCache.Store(actorURI, relayActorEntry{inboxes: inboxes, expires: time.Now().Add(relayActorCacheTTL)})
	return inboxes, nil
}

func sameHost(a, b string) bool {
	ua, ea := url.Parse(a)
	ub, eb := url.Parse(b)
	return ea == nil && eb == nil && ua.Host != "" && strings.EqualFold(ua.Host, ub.Host)
}

// relayOwnedBy reports whether signer is the actor operating relay.
func relayOwnedBy(ctx context.Context, relay *models.Relay, signer string) (bool, error) {
	if !sameHost(relay.InboxURL, signer) {
		return false, nil
	}
	inboxes, err := relayActorInboxes(ctx, signer)
	if err != nil {
		return false, err
	}
	for _, inbox := range inboxes {
		if inbox == relay.InboxURL {
			return true, nil
		}
	}
	return false, nil
}

// acceptedRelayForSigner returns the accepted relay operated by signer, or
// nil when signer is not a subscribed relay.
func acceptedRelayForSigner(ctx context.Context, conn *gorm.DB, signer string) (*models.Relay, error) {
	var relays []models.Relay
	if err := conn.Where("state = ?", models.RelayStateAccepted).Find(&relays).Error; err != nil {
		if !conn.Migrator().HasTable(&models.Relay{}) {
			return nil, nil // minimal schemas without relays
		}
		return nil, err
	}
	for i := range relays {
		if !sameHost(relays[i].InboxURL, signer) {
			continue
		}
		owned, err := relayOwnedBy(ctx, &relays[i], signer)
		if err != nil {
			log.Warn("Cannot verify relay actor %s: %v", signer, err)
			continue
		}
		if owned {
			return &relays[i], nil
		}
	}
	return nil, nil
}

// handleRelayFollowResponse settles an Accept/Reject of a relay subscription.
// It runs before any remote-account lookup: relay actors are usually not
// cached accounts. It reports whether id referred to a relay Follow.
func handleRelayFollowResponse(ctx context.Context, c *gin.Context, id string, object map[string]any, signer string, accepted bool) bool {
	if id == "" {
		return false
	}
	var relay models.Relay
	err := activityDB(ctx).Where("follow_activity_id = ? AND follow_activity_id <> ''", id).First(&relay).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}
	if err != nil {
		if !activityDB(ctx).Migrator().HasTable(&models.Relay{}) {
			return false
		}
		c.JSON(500, gin.H{"error": "failed to locate relay"})
		return true
	}
	if object != nil {
		if a, ok := object["actor"].(string); ok && a != InstanceActorURI() {
			c.JSON(403, gin.H{"error": "follow identity mismatch"})
			return true
		}
	}
	owned, err := relayOwnedBy(ctx, &relay, signer)
	if err != nil {
		c.JSON(502, gin.H{"error": "cannot verify relay actor"})
		return true
	}
	if !owned {
		c.JSON(403, gin.H{"error": "wrong relay actor"})
		return true
	}
	state := models.RelayStateRejected
	if accepted {
		state = models.RelayStateAccepted
	}
	if err := activityDB(ctx).Model(&models.Relay{}).Where("id = ? AND follow_activity_id = ?", relay.ID, id).Update("state", state).Error; err != nil {
		c.JSON(500, gin.H{"error": "failed relay update"})
		return true
	}
	log.Info("Relay %s %s our subscription", relay.InboxURL, map[bool]string{true: "accepted", false: "rejected"}[accepted])
	c.JSON(200, gin.H{"status": "accepted"})
	return true
}

func isRelayForwardType(activityType string) bool {
	switch activityType {
	case "Create", "Update", "Delete", "Announce":
		return true
	}
	return false
}

func activityObjectID(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		id, _ := v["id"].(string)
		return id
	}
	return ""
}

// attributedToURI returns the single author of a fetched object.
func attributedToURI(object map[string]any) string {
	switch v := object["attributedTo"].(type) {
	case string:
		return v
	case []any:
		if len(v) == 1 {
			return activityObjectID(v[0])
		}
	case map[string]any:
		return activityObjectID(v)
	}
	return ""
}

// relayFetch fetches uri unsigned, retrying with a signature on 401/403,
// and reports the final HTTP status (0 on transport errors).
func relayFetch(ctx context.Context, uri string) (map[string]any, int, error) {
	data, status, err := NewFetchClient().getWithStatus(ctx, uri)
	if err != nil {
		return nil, status, err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, status, err
	}
	return obj, status, nil
}

// handleRelayedActivity processes an activity a subscribed relay delivered
// (forwarded Mastodon-style, or wrapped in the relay's own Announce). The
// embedded copy is never trusted: the object is re-fetched from its origin,
// the origin must be the author's host, and the fetched copy is imported.
// A relay Announce never creates a boost, counters or notifications.
func handleRelayedActivity(ctx context.Context, c *gin.Context, activity map[string]any, activityID string, relay *models.Relay) {
	activityType, _ := activity["type"].(string)
	objectID := activityObjectID(activity["object"])
	if objectID == "" || validateFederationURI(objectID) != nil {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}
	if activityType == "Delete" {
		handleRelayedDelete(ctx, c, activity, activityID, objectID)
		return
	}
	obj, canonical, err := NewResolver(nil, InstanceActorURI()).fetchCanonicalObject(ctx, objectID)
	if err != nil {
		log.Info("Relay %s: cannot fetch %s: %v", relay.InboxURL, objectID, err)
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	// Only Notes are imported from relays. Check the type before the author:
	// relays also forward Update{Person}, Question, Video..., which have no
	// single attributedTo and must be ignored rather than refused with 403.
	if objectType, _ := obj["type"].(string); objectType != "Note" {
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	author := attributedToURI(obj)
	if author == "" || !sameURIOrigin(canonical, author) {
		log.Warn("Relay %s: %s is not hosted by its author %q", relay.InboxURL, canonical, author)
		c.JSON(403, gin.H{"error": "object origin does not match its author"})
		return
	}
	if getVisibilityFromActivity(obj) != models.StatusVisibilityPublic {
		// Relays only carry public posts; never widen anything else.
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	if blocked, err := domainSuspended(actorHostname(author)); err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	} else if blocked {
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	var existing int64
	if err := db.GetDB().WithContext(ctx).Model(&models.Status{}).Where("origin_uri = ?", canonical).Count(&existing).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	kind := "Create"
	if activityType == "Update" && existing > 0 {
		kind = "Update"
	} else if existing > 0 {
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}
	obj["id"] = canonical
	synthesized := map[string]any{"id": activityID, "type": kind, "actor": author, "object": obj}
	authorCtx := context.WithValue(ctx, actorKey, author)
	processInboxTransaction(authorCtx, c, activityID, func(ctx context.Context) {
		if kind == "Update" {
			handleUpdateActivity(ctx, c, synthesized, nil)
		} else {
			handleCreateActivity(ctx, c, synthesized, nil)
		}
	})
}

// handleRelayedDelete applies a forwarded Delete only when the origin
// confirms the object (or actor) is gone.
func handleRelayedDelete(ctx context.Context, c *gin.Context, activity map[string]any, activityID, objectID string) {
	declared, _ := activity["actor"].(string)
	var owner string
	if objectID == declared {
		owner = declared
	} else {
		var status models.Status
		err := db.GetDB().WithContext(ctx).Where("origin_uri = ?", objectID).First(&status).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(200, gin.H{"status": "accepted"})
			return
		}
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}
		var account models.Account
		if err := db.GetDB().WithContext(ctx).First(&account, status.AccountId).Error; err != nil {
			c.JSON(200, gin.H{"status": "accepted"})
			return
		}
		owner = account.OriginId
	}
	if owner == "" || !sameURIOrigin(owner, objectID) {
		c.JSON(403, gin.H{"error": "object origin does not match its author"})
		return
	}
	obj, status, err := relayFetch(ctx, objectID)
	gone := status == http.StatusNotFound || status == http.StatusGone
	if err == nil {
		if t, _ := obj["type"].(string); t == "Tombstone" {
			gone = true
		}
	}
	if !gone {
		// Still served (or unreachable): the forwarded Delete is unproven.
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	synthesized := map[string]any{"id": activityID, "type": "Delete", "actor": owner, "object": objectID}
	processInboxTransaction(context.WithValue(ctx, actorKey, owner), c, activityID, func(ctx context.Context) {
		handleDeleteActivity(ctx, c, synthesized, nil)
	})
}

func domainSuspended(host string) (bool, error) {
	if host == "" {
		return false, nil
	}
	suspend, _, err := models.DomainSeverities(db.GetDB())
	if err != nil {
		return false, err
	}
	return suspend[host], nil
}
