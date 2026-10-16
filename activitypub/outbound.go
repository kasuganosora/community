package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// OutboundWorker expands committed model events into the persistent delivery
// queue. No network request occurs in the user's mutation transaction.
type OutboundWorker struct {
	stop     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
	delivery *DeliveryQueue
}

func StartOutboundWorker() (*OutboundWorker, error) {
	for _, model := range []any{&models.FederationOutbox{}, &Delivery{}, &models.AccountKey{}, &models.StatusReaction{}, &models.RemoteReactionActivity{}} {
		if !db.GetDB().Migrator().HasTable(model) {
			return nil, errors.New("federation tables are missing; run database migrations")
		}
	}
	if !db.GetDB().Migrator().HasColumn(&Delivery{}, "Authorization") {
		return nil, errors.New("federation delivery authorization migration is missing")
	}
	models.RequireFederationOutbox()
	models.RequireReactionSchema()
	w := &OutboundWorker{stop: make(chan struct{}), delivery: NewDeliveryQueue(nil, "", 1)}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
				w.processPending()
			}
		}
	}()
	w.delivery.Start()
	return w, nil
}
func (w *OutboundWorker) Stop() { w.once.Do(func() { close(w.stop); w.wg.Wait(); w.delivery.Stop() }) }

func (w *OutboundWorker) processPending() {
	var events []models.FederationOutbox
	now := time.Now()
	if err := db.GetDB().Where("state IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", []string{"pending", "processing"}, now).
		Where(`NOT EXISTS (SELECT 1 FROM activitypub_outbox_events AS prior
			WHERE prior.account_id = activitypub_outbox_events.account_id
			AND prior.id < activitypub_outbox_events.id AND prior.state IN ?
			AND prior.next_attempt_at > ?)`, []string{"pending", "processing"}, now).
		Order("id ASC").Limit(20).Find(&events).Error; err != nil {
		log.Warn("Federation outbox read failed: %v", err)
		return
	}
	for _, e := range events {
		w.process(e)
	}
}

func (w *OutboundWorker) process(e models.FederationOutbox) {
	// Preserve an author's event order across retries (e.g. Create before Delete).
	var older int64
	if err := db.GetDB().Model(&models.FederationOutbox{}).Where("account_id = ? AND id < ? AND state IN ?", e.AccountID, e.ID, []string{"pending", "processing"}).Count(&older).Error; err != nil || older > 0 {
		return
	}
	now := time.Now()
	lease := now.Add(2 * time.Minute)
	claim := db.GetDB().Model(&models.FederationOutbox{}).Where("id = ? AND state IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", e.ID, []string{"pending", "processing"}, now).Updates(map[string]any{"state": "processing", "next_attempt_at": lease})
	if claim.Error != nil || claim.RowsAffected != 1 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	deliveries, err := w.expand(ctx, &e)
	if err == nil {
		err = db.GetDB().Transaction(func(tx *gorm.DB) error {
			if len(deliveries) > 0 {
				if err := tx.CreateInBatches(deliveries, 100).Error; err != nil {
					return err
				}
			}
			return tx.Model(&models.FederationOutbox{}).Where("id = ?", e.ID).Updates(map[string]any{"state": "processed", "next_attempt_at": nil, "last_error": ""}).Error
		})
	}
	if err != nil {
		attempts := e.Attempts + 1
		state := "pending"
		if attempts >= 5 {
			state = "failed"
		}
		next := time.Now().Add(time.Duration(attempts*attempts) * time.Minute)
		db.GetDB().Model(&models.FederationOutbox{}).Where("id = ?", e.ID).Updates(map[string]any{"state": state, "attempts": attempts, "last_error": err.Error(), "next_attempt_at": next})
		log.Warn("Federation outbox %d: %v", e.ID, err)
	}
}

func (w *OutboundWorker) expand(ctx context.Context, e *models.FederationOutbox) ([]Delivery, error) {
	var author models.Account
	if err := db.GetDB().First(&author, e.AccountID).Error; err != nil {
		return nil, err
	}
	if author.Domain != nil || author.IsBan {
		return nil, nil
	} // Never reflect remote input.
	actorURI := accountActorURI(&author, systemsettings.GetBaseURL())
	activityID := actorURI + "/activities/" + strconv.FormatUint(e.ID, 10)
	var payload map[string]any
	var snapshot *models.Status
	switch e.Kind {
	case "ProfileUpdate":
		var err error
		payload, err = profileUpdatePayload(e, actorURI, activityID)
		if err != nil {
			return nil, err
		}
	case "Reaction", "ReactionUndo":
		var err error
		payload, snapshot, err = reactionPayload(e, actorURI, activityID)
		if err != nil {
			return nil, err
		}
		if payload == nil {
			return nil, nil
		}
	case "Follow", "Undo":
		var follow models.Follow
		if err := json.Unmarshal([]byte(e.Payload), &follow); err != nil {
			return nil, err
		}
		var target models.Account
		if err := db.GetDB().First(&target, follow.FollowId).Error; err != nil {
			return nil, err
		}
		if target.Domain == nil || target.OriginId == "" {
			return nil, errors.New("follow recipient has no remote actor URI")
		}
		followID := actorURI + "/follows/" + strconv.FormatUint(follow.Id, 10)
		if imported, _ := follow.Properties["activity_uri"].(string); imported != "" {
			followID = imported
		}
		original := map[string]any{"id": followID, "type": "Follow", "actor": actorURI, "object": target.OriginId}
		if e.Kind == "Follow" {
			payload = original
			activityID = followID
		} else {
			payload = map[string]any{"id": activityID, "type": "Undo", "actor": actorURI, "object": original}
		}
	case "Accept", "Reject":
		var follow models.Follow
		if err := json.Unmarshal([]byte(e.Payload), &follow); err != nil {
			return nil, err
		}
		originalID, _ := follow.Properties["activity_uri"].(string)
		if originalID == "" {
			originalID, _ = follow.Properties["activitypub_follow_id"].(string)
		}
		if originalID == "" {
			return nil, errors.New("follow decision is missing original activity ID")
		}
		var requester models.Account
		if err := db.GetDB().First(&requester, follow.AccountId).Error; err != nil {
			return nil, err
		}
		if requester.Domain == nil || requester.OriginId == "" {
			return nil, errors.New("follow decision recipient is not remote")
		}
		payload = map[string]any{"id": activityID, "type": e.Kind, "actor": actorURI, "object": map[string]any{"id": originalID, "type": "Follow", "actor": requester.OriginId, "object": actorURI}}
	case "Create", "Update", "Delete":
		snapshot = &models.Status{}
		if err := json.Unmarshal([]byte(e.Payload), snapshot); err != nil {
			return nil, err
		}
		if snapshot.LocalOnly {
			return nil, nil
		}
		if snapshot.Account == nil || snapshot.Account.Domain != nil || !snapshot.IsLocal() {
			return nil, errors.New("outbox status must be local")
		}
		if e.Kind == "Delete" {
			to, cc := statusRecipients(snapshot, &author, systemsettings.GetBaseURL())
			payload = map[string]any{"id": activityID, "type": "Delete", "actor": actorURI, "to": to, "cc": cc, "object": map[string]any{"id": statusObjectURI(snapshot, &author, systemsettings.GetBaseURL()), "type": "Tombstone"}}
		} else {
			if snapshot.Poll != nil && snapshot.Poll.Id != 0 {
				// Updates are coalesced; always send the current tallies.
				var current models.Poll
				if err := db.GetDB().Preload("Options").First(&current, snapshot.Poll.Id).Error; err == nil {
					snapshot.Poll = &current
				}
			}
			note, err := StatusToNote(snapshot)
			if err != nil {
				return nil, err
			}
			payload = map[string]any{"id": activityID, "type": e.Kind, "actor": actorURI, "to": note["to"], "cc": note["cc"], "object": note}
			if ctx, ok := note["@context"]; ok {
				payload["@context"] = ctx
			}
		}
	case "Announce", "AnnounceUndo":
		snapshot = &models.Status{}
		if err := json.Unmarshal([]byte(e.Payload), snapshot); err != nil {
			return nil, err
		}
		if snapshot.Account == nil || snapshot.Account.Domain != nil || !snapshot.IsLocal() || snapshot.Reblog == nil || snapshot.Reblog.Account == nil {
			return nil, errors.New("outbox boost must be local and reference its original")
		}
		payload, activityID = announcePayload(snapshot, &author, actorURI, activityID, e.Kind == "AnnounceUndo")
	case "PollVote":
		return pollVoteDeliveries(ctx, e, &author)
	default:
		return nil, fmt.Errorf("unknown federation event %q", e.Kind)
	}
	if _, ok := payload["@context"]; !ok {
		payload["@context"] = "https://www.w3.org/ns/activitystreams"
	}
	encoded, err := json.Marshal(stripPrivacyFields(payload))
	if err != nil {
		return nil, err
	}
	var targets []models.Account
	if len(e.RecipientIDs) > 0 {
		if err = db.GetDB().Where("id IN ? AND domain IS NOT NULL", e.RecipientIDs).Find(&targets).Error; err != nil {
			return nil, err
		}
	}
	deliveries := make([]Delivery, 0, len(targets))
	seen := map[string]bool{}
	authorizedIDs := map[uint64]bool{}
	for _, target := range targets {
		allowed, err := outboundRecipientAllowed(snapshot, &author, &target)
		if e.Kind == "ProfileUpdate" {
			allowed, err = profileRecipientAllowed(&author, &target)
		}
		if e.Kind == "ReactionUndo" || e.Kind == "Undo" || e.Kind == "Delete" || e.Kind == "Reject" || e.Kind == "AnnounceUndo" {
			allowed = true
			err = nil
		}
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		authorizedIDs[target.Id] = true
		inbox, err := remoteActorInbox(ctx, &target)
		if err != nil {
			return nil, err
		}
		// Several actors may share an inbox. Keep every intended actor for
		// send-time authorization; do not silently lose recipients on dedup.
		auth := DeliveryAuthorization{AccountID: author.Id, RecipientIDs: []uint64{target.Id}, Kind: e.Kind}
		if snapshot != nil {
			auth.StatusID = snapshot.Id
			auth.Visibility = snapshot.Visibility
		}
		if seen[inbox] {
			for i := range deliveries {
				if deliveries[i].TargetInbox == inbox {
					var existing DeliveryAuthorization
					_ = json.Unmarshal([]byte(deliveries[i].Authorization), &existing)
					existing.RecipientIDs = append(existing.RecipientIDs, target.Id)
					data, _ := json.Marshal(existing)
					deliveries[i].Authorization = string(data)
				}
			}
			continue
		}
		seen[inbox] = true
		authData, err := json.Marshal(auth)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, Delivery{ActivityID: activityID, TargetInbox: inbox, Payload: string(encoded), Authorization: string(authData), Status: DeliveryStatusPending})
	}
	if e.Kind == "Update" && snapshot != nil {
		revoked, err := revokePreviousRecipients(ctx, e, snapshot, &author, activityID, authorizedIDs, seen)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, revoked...)
	}
	relayed, err := relayDeliveries(e, snapshot, &author, activityID, string(encoded), seen)
	if err != nil {
		return nil, err
	}
	return append(deliveries, relayed...), nil
}

// relayDeliveries adds the inbox of every accepted relay for activities about
// a PUBLIC local status (never unlisted, private or direct). Inboxes already
// receiving the activity (e.g. a follower shared inbox) are not repeated.
func relayDeliveries(e *models.FederationOutbox, snapshot *models.Status, author *models.Account, activityID, payload string, seen map[string]bool) ([]Delivery, error) {
	switch e.Kind {
	case "Create", "Update", "Delete", "Announce", "AnnounceUndo":
	default:
		return nil, nil
	}
	if snapshot == nil || snapshot.Visibility != models.StatusVisibilityPublic || snapshot.LocalOnly {
		return nil, nil
	}
	if snapshot.Reblog != nil && (snapshot.Reblog.Visibility != models.StatusVisibilityPublic || snapshot.Reblog.LocalOnly) {
		return nil, nil
	}
	relays, err := models.GetEnabledRelays(db.GetDB())
	if err != nil {
		return nil, err
	}
	var deliveries []Delivery
	for _, relay := range relays {
		if seen[relay.InboxURL] || validateFederationURI(relay.InboxURL) != nil {
			continue
		}
		seen[relay.InboxURL] = true
		auth, err := json.Marshal(DeliveryAuthorization{AccountID: author.Id, StatusID: snapshot.Id, Visibility: snapshot.Visibility, Kind: e.Kind, RelayID: relay.ID})
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, Delivery{ActivityID: activityID, TargetInbox: relay.InboxURL, Payload: payload, Authorization: string(auth), Status: DeliveryStatusPending})
	}
	return deliveries, nil
}

// announcePayload builds the Announce for a local boost, or its Undo. The
// Announce id is stable (<boost uri>/activity) so the Undo can name it.
func announcePayload(boost *models.Status, author *models.Account, actorURI, undoID string, undo bool) (map[string]any, string) {
	base := systemsettings.GetBaseURL()
	original := boost.Reblog
	objectURI := statusObjectURI(original, original.Account, base)
	announceID := statusObjectURI(boost, author, base) + "/activity"
	to := []string{"https://www.w3.org/ns/activitystreams#Public"}
	cc := []string{accountActorURI(original.Account, base), actorURI + "/followers"}
	if boost.Visibility == models.StatusVisibilityUnlisted {
		to, cc = []string{actorURI + "/followers"}, []string{"https://www.w3.org/ns/activitystreams#Public", accountActorURI(original.Account, base)}
	}
	announce := map[string]any{"id": announceID, "type": "Announce", "actor": actorURI, "to": to, "cc": cc, "object": objectURI}
	if boost.CreatedAt != nil {
		announce["published"] = boost.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !undo {
		return announce, announceID
	}
	return map[string]any{"id": undoID, "type": "Undo", "actor": actorURI, "to": to, "cc": cc, "object": announce}, undoID
}

// When an edit removes a remote audience, send only a Tombstone to those
// previously targeted inboxes, never the new restricted body.
func revokePreviousRecipients(ctx context.Context, e *models.FederationOutbox, status *models.Status, author *models.Account, activityID string, authorized map[uint64]bool, currentInboxes map[string]bool) ([]Delivery, error) {
	var previous []models.FederationOutbox
	if err := db.GetDB().Where("account_id = ? AND object_id = ? AND id < ? AND kind IN ?", e.AccountID, e.ObjectID, e.ID, []string{"Create", "Update"}).Find(&previous).Error; err != nil {
		return nil, err
	}
	ids := map[uint64]bool{}
	for _, p := range previous {
		var queued int64
		priorID := accountActorURI(author, systemsettings.GetBaseURL()) + "/activities/" + strconv.FormatUint(p.ID, 10)
		if err := db.GetDB().Model(&Delivery{}).Where("activity_id = ?", priorID).Count(&queued).Error; err != nil {
			return nil, err
		}
		if queued == 0 {
			continue
		}
		for _, id := range p.RecipientIDs {
			if !authorized[id] {
				ids[id] = true
			}
		}
	}
	var deliveries []Delivery
	for id := range ids {
		var target models.Account
		if err := db.GetDB().First(&target, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		if target.Domain == nil {
			continue
		}
		inbox, err := remoteActorInbox(ctx, &target)
		if err != nil {
			return nil, err
		}
		// One server can host both audiences. Its Update carries the corrected
		// audience; a Delete to that same inbox would delete the retained copy.
		if currentInboxes[inbox] {
			continue
		}
		currentInboxes[inbox] = true
		revokeID := activityID + "/revoke/" + strconv.FormatUint(id, 10)
		payload := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": revokeID, "type": "Delete", "actor": accountActorURI(author, systemsettings.GetBaseURL()), "to": []string{target.OriginId}, "object": map[string]any{"id": statusObjectURI(status, author, systemsettings.GetBaseURL()), "type": "Tombstone"}}
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		authData, err := json.Marshal(DeliveryAuthorization{AccountID: author.Id, RecipientIDs: []uint64{id}, Kind: "Delete", StatusID: status.Id})
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, Delivery{ActivityID: revokeID, TargetInbox: inbox, Payload: string(data), Authorization: string(authData), Status: DeliveryStatusPending})
	}
	return deliveries, nil
}

func outboundRecipientAllowed(status *models.Status, author, target *models.Account) (bool, error) {
	if target.IsBan {
		return false, nil
	}
	var blocks int64
	if err := db.GetDB().Model(&models.Block{}).Where("(account_id = ? AND target_account_id = ?) OR (account_id = ? AND target_account_id = ?)", author.Id, target.Id, target.Id, author.Id).Count(&blocks).Error; err != nil {
		return false, err
	}
	if blocks > 0 {
		return false, nil
	}
	if target.Domain != nil {
		if err := db.GetDB().Model(&models.Block{}).Where("account_id = ? AND type = ? AND target_domain = ?", author.Id, models.BlockTypeDomain, *target.Domain).Count(&blocks).Error; err != nil {
			return false, err
		}
		if blocks > 0 {
			return false, nil
		}
	}
	if status == nil {
		return true, nil
	}
	// CanViewEx checks accepted followers, and direct only permits mentions.
	return status.CanViewEx(db.GetDB(), target)
}

// Inbox URLs are actor metadata, not a convention based on username/domain.
// Misskey in particular uses opaque actor IDs and a shared /inbox endpoint.
func remoteActorInbox(ctx context.Context, account *models.Account) (string, error) {
	if account.OriginId == "" {
		return "", errors.New("remote account has no actor URI")
	}
	actor, err := NewFetchClient().FetchActor(ctx, account.OriginId)
	if err != nil {
		return "", err
	}
	if actor.ID != account.OriginId || actor.Inbox == "" {
		return "", errors.New("remote actor identity or inbox mismatch")
	}
	if err = validateFederationURI(actor.Inbox); err != nil {
		return "", err
	}
	return actor.Inbox, nil
}
