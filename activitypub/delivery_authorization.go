package activitypub

import (
	"encoding/json"
	"errors"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// DeliveryAuthorization is optional for legacy/relay/control deliveries.
// New status deliveries retain actual recipients to recheck after retries.
type DeliveryAuthorization struct {
	AccountID    uint64   `json:"account_id"`
	RecipientIDs []uint64 `json:"recipient_ids"`
	StatusID     uint64   `json:"status_id,omitempty"`
	Visibility   uint8    `json:"visibility"`
	Kind         string   `json:"kind"`
	// RelayID marks a delivery to a subscribed relay's inbox; it is only
	// sent while the relay is accepted and the status is still public.
	RelayID uint64 `json:"relay_id,omitempty"`
}

func authorizeQueuedDelivery(delivery *Delivery, activity map[string]any) (map[string]any, bool, error) {
	if delivery.Authorization == "" {
		return activity, true, nil
	}
	var auth DeliveryAuthorization
	if err := json.Unmarshal([]byte(delivery.Authorization), &auth); err != nil {
		return nil, false, err
	}
	// Revocation messages contain identifiers, never old note content, and
	// must be delivered even after an unfollow/block removed access.
	if auth.Kind == "ReactionUndo" || auth.Kind == "Undo" || auth.Kind == "Delete" || auth.Kind == "Reject" || auth.Kind == "AnnounceUndo" {
		return activity, true, nil
	}
	if auth.Kind == "Reaction" {
		current, err := reactionDeliveryCurrent(auth, activity)
		if err != nil || !current {
			return activity, false, err
		}
	}
	var author models.Account
	if err := db.GetDB().First(&author, auth.AccountID).Error; err != nil {
		return nil, false, err
	}
	if author.Domain != nil || author.IsBan {
		return activity, false, nil
	}
	var status *models.Status
	if auth.StatusID > 0 {
		status = &models.Status{}
		err := db.GetDB().Preload("ToAccounts").First(status, auth.StatusID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return activity, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if status.LocalOnly {
			return activity, false, nil
		}
		if ((status.AccountId != auth.AccountID || !status.IsLocal()) && auth.Kind != "Reaction") || status.ScheduledAt != nil || status.DeletedAt != nil {
			return activity, false, nil
		}
		if auth.Kind == "Reaction" {
			ok, err := status.CanViewEx(db.GetDB(), &author)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return activity, false, nil
			}
		}
		// A formerly public envelope may be cached/redistributed by a peer.
		// Never send it once the author has narrowed the audience.
		if status.Visibility > auth.Visibility {
			return activity, false, nil
		}
	}
	if auth.RelayID != 0 {
		var relay models.Relay
		err := db.GetDB().Where("id = ? AND state = ?", auth.RelayID, models.RelayStateAccepted).First(&relay).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return activity, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return activity, status != nil && status.Visibility == models.StatusVisibilityPublic, nil
	}
	var recipients []models.Account
	if err := db.GetDB().Where("id IN ?", auth.RecipientIDs).Find(&recipients).Error; err != nil {
		return nil, false, err
	}
	allowed := false
	revoked := make(map[string]bool)
	for i := range recipients {
		ok, err := outboundRecipientAllowed(status, &author, &recipients[i])
		if auth.Kind == "ProfileUpdate" {
			ok, err = profileRecipientAllowed(&author, &recipients[i])
		}
		if err != nil {
			return nil, false, err
		}
		if ok {
			allowed = true
		} else {
			revoked[accountActorURI(&recipients[i], systemsettings.GetBaseURL())] = true
		}
	}
	if !allowed {
		return activity, false, nil
	}
	// A shared inbox may still contain one authorized recipient; remove any
	// now-revoked explicit addresses before sending a restricted Note.
	if status != nil && status.Visibility >= models.StatusVisibilityPrivate {
		pruneAddresses(activity, revoked)
		if object, ok := activity["object"].(map[string]any); ok {
			pruneAddresses(object, revoked)
		}
	}
	return activity, true, nil
}

func pruneAddresses(object map[string]any, revoked map[string]bool) {
	for _, field := range []string{"to", "cc"} {
		switch addresses := object[field].(type) {
		case []any:
			kept := make([]any, 0, len(addresses))
			for _, a := range addresses {
				uri, _ := a.(string)
				if !revoked[uri] {
					kept = append(kept, a)
				}
			}
			object[field] = kept
		case []string:
			kept := make([]string, 0, len(addresses))
			for _, uri := range addresses {
				if !revoked[uri] {
					kept = append(kept, uri)
				}
			}
			object[field] = kept
		}
	}
}
