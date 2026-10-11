package homeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

type MatrixDMReactionReference struct {
	EventID    string `gorm:"primaryKey;size:255"`
	RoomID     string `gorm:"size:255;index"`
	StatusID   uint64 `gorm:"index"`
	AccountID  uint64
	ReactionID uint64 `gorm:"index"`
	Name       string `gorm:"size:320"`
}

func (hs *HomeServer) bridgeReactionActor(tx *gorm.DB, event *MatrixEvent, statusID uint64) (*models.Account, error) {
	actor, err := hs.bridgeEventAccount(tx, event.RoomID, event.Sender)
	if err != nil {
		return nil, err
	}
	var member MatrixRoomMember
	if err = tx.Where("room_id = ? AND user_id = ? AND membership = ?", event.RoomID, event.Sender, MembershipJoin).First(&member).Error; err != nil {
		return nil, errDirectMessageSkipped
	}
	var encryption int64
	if err = tx.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", event.RoomID, EventTypeRoomEncryption).Count(&encryption).Error; err != nil {
		return nil, err
	}
	if encryption > 0 {
		return nil, errDirectMessageSkipped
	}
	var status models.Status
	if err = tx.Preload("ToAccounts").Where("id = ? AND deleted_at IS NULL AND visibility = ?", statusID, models.StatusVisibilityDirect).First(&status).Error; err != nil {
		return nil, err
	}
	allowed, err := status.CanViewEx(tx, actor)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, errDirectMessageSkipped
	}
	return actor, nil
}
func (hs *HomeServer) queueDirectBridgeReaction(tx *gorm.DB, event *MatrixEvent) (bool, error) {
	if event.EventType != "m.reaction" || (!strings.HasPrefix(event.RoomID, "!apdm_") && !strings.HasPrefix(event.RoomID, "!apchat_")) {
		return false, nil
	}
	relation, _ := event.Content["m.relates_to"].(map[string]any)
	target, _ := relation["event_id"].(string)
	key, _ := relation["key"].(string)
	if relation["rel_type"] != "m.annotation" || target == "" {
		return true, errDirectMessageSkipped
	}
	var mapping models.MatrixDMDelivery
	err := resolveDirectEventStatus(tx, event.RoomID, target, &mapping)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	actor, err := hs.bridgeReactionActor(tx, event, mapping.StatusID)
	if err != nil {
		return true, err
	}
	if _, err = models.ResolveReaction(tx, key, actor); err != nil {
		return true, errDirectMessageSkipped
	}
	sum := sha256.Sum256([]byte(event.EventID))
	job := models.MatrixDMEventJob{ID: "mx-reaction:" + hex.EncodeToString(sum[:]), Kind: "matrix_reaction", StatusID: mapping.StatusID, AccountID: actor.Id, RoomID: event.RoomID, EventID: event.EventID, ReactionName: key, State: "pending", NextAttemptAt: time.Now()}
	return true, tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
}
func (hs *HomeServer) queueBridgeReactionRemoval(tx *gorm.DB, event *MatrixEvent) (bool, error) {
	if event.Redacts == nil {
		return false, nil
	}
	// Serialize against the original reaction worker before checking its mapping.
	var pendingLock models.MatrixDMEventJob
	lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("event_id = ? AND room_id = ? AND kind = ?", *event.Redacts, event.RoomID, "matrix_reaction").First(&pendingLock).Error
	if lockErr != nil && !errors.Is(lockErr, gorm.ErrRecordNotFound) {
		return true, lockErr
	}
	var reference MatrixDMReactionReference
	err := tx.Where("event_id = ? AND room_id = ?", *event.Redacts, event.RoomID).First(&reference).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A client may withdraw a reaction before its worker runs. Queue behind the
		// original job, instead of allowing a late worker to publish the withdrawn reaction.
		var pending models.MatrixDMEventJob
		err = tx.Where("event_id = ? AND room_id = ? AND kind = ?", *event.Redacts, event.RoomID, "matrix_reaction").First(&pending).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return true, err
		}
		var target MatrixEvent
		if err = tx.First(&target, "event_id = ?", *event.Redacts).Error; err != nil {
			return true, err
		}
		if target.Sender != event.Sender {
			return true, errDirectMessageSkipped
		}
		return true, tx.Model(&models.MatrixDMEventJob{}).Where("id = ? AND state = ?", pending.ID, "pending").Updates(map[string]any{"state": "skipped", "last_error": "reaction withdrawn before delivery"}).Error
	}
	if err != nil {
		return true, err
	}
	actor, err := hs.bridgeReactionActor(tx, event, reference.StatusID)
	if err != nil {
		return true, err
	}
	if actor.Id != reference.AccountID {
		return true, errDirectMessageSkipped
	}
	sum := sha256.Sum256([]byte(event.EventID))
	return true, tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.MatrixDMEventJob{ID: "mx-reaction-undo:" + hex.EncodeToString(sum[:]), Kind: "matrix_reaction_undo", StatusID: reference.StatusID, AccountID: actor.Id, RoomID: event.RoomID, EventID: event.EventID, ReactionID: reference.ReactionID, ReactionName: reference.Name, State: "pending", NextAttemptAt: time.Now()}).Error
}

func (hs *HomeServer) processBridgeReaction(tx *gorm.DB, job *models.MatrixDMEventJob, changed map[string]bool) error {
	if job.Kind == "matrix_reaction" || job.Kind == "matrix_reaction_undo" {
		var event MatrixEvent
		if err := tx.First(&event, "event_id = ? AND room_id = ?", job.EventID, job.RoomID).Error; err != nil {
			return err
		}
		actor, err := hs.bridgeReactionActor(tx, &event, job.StatusID)
		if err != nil {
			return err
		}
		if actor.Id != job.AccountID {
			return errDirectMessageSkipped
		}
		remove := job.Kind == "matrix_reaction_undo"
		var emoji *models.Emoji
		if !remove {
			relation, _ := event.Content["m.relates_to"].(map[string]any)
			if relation["key"] != job.ReactionName {
				return errDirectMessageSkipped
			}
			emoji, err = models.ResolveReaction(tx, job.ReactionName, actor)
			if err != nil {
				return errDirectMessageSkipped
			}
		} else {
			var live models.StatusReaction
			err = tx.Where("id = ? AND account_id = ? AND status_id = ?", job.ReactionID, actor.Id, job.StatusID).First(&live).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		if err = models.SetStatusReaction(tx, job.StatusID, actor, job.ReactionName, emoji, "", remove); err != nil {
			return err
		}
		if !remove {
			var live models.StatusReaction
			if err = tx.Where("account_id = ? AND status_id = ?", actor.Id, job.StatusID).First(&live).Error; err != nil {
				return err
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MatrixDMReactionReference{EventID: event.EventID, RoomID: event.RoomID, StatusID: job.StatusID, AccountID: actor.Id, ReactionID: live.ID, Name: live.Name}).Error
		}
		return nil
	}
	var references []MatrixDMReactionReference
	if err := tx.Where("reaction_id = ?", job.ReactionID).Find(&references).Error; err != nil {
		return err
	}
	if job.Kind == "ReactionUndo" {
		for _, ref := range references {
			if err := hs.redactBridgeReaction(tx, ref, changed); err != nil {
				return err
			}
		}
		return nil
	}
	if len(references) > 0 {
		return nil
	} // Already originated from Matrix: no echo.
	var status models.Status
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("ToAccounts").Where("id = ? AND deleted_at IS NULL", job.StatusID).First(&status).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var live models.StatusReaction
	if err := tx.First(&live, "id = ?", job.ReactionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var actor models.Account
	if err := tx.First(&actor, job.AccountID).Error; err != nil {
		return err
	}
	allowed, err := status.CanViewEx(tx, &actor)
	if err != nil {
		return err
	}
	if !allowed {
		return errDirectMessageSkipped
	}
	var mapping models.MatrixDMDelivery
	if err = tx.Where("status_id = ? AND state = ? AND event_id <> ''", job.StatusID, "delivered").First(&mapping).Error; err != nil {
		return err
	}
	var encrypted int64
	if err = tx.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", mapping.RoomID, EventTypeRoomEncryption).Count(&encrypted).Error; err != nil {
		return err
	}
	if encrypted > 0 {
		return errDirectMessageSkipped
	}
	if err := hs.validateBridgeAccountIdentity(tx, actor); err != nil {
		return errDirectMessageSkipped
	}
	sender := hs.bridgeAccountID(actor)
	var member MatrixRoomMember
	if err = tx.Where("room_id = ? AND user_id = ? AND membership = ?", mapping.RoomID, sender, MembershipJoin).First(&member).Error; err != nil {
		return errDirectMessageSkipped
	}
	key := job.ReactionName
	content := map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": mapping.EventID, "key": key}}
	if job.EmojiID != 0 {
		var emoji models.Emoji
		if err = tx.First(&emoji, job.EmojiID).Error; err != nil {
			return err
		}
		// Matrix has no universal AP custom-emoji extension. Preserve the shortcode
		// as a standard annotation; clients may optionally render the image metadata.
		content["org.community.custom_emoji"] = map[string]any{"shortcode": emoji.Shortcode, "domain": emoji.Domain, "url": emoji.Url}
	}
	txn := fmt.Sprintf("$bridge-reaction-%d", job.ReactionID)
	event := MatrixEvent{EventID: txn, TxnKey: &txn, RoomID: mapping.RoomID, Sender: sender, EventType: "m.reaction", Content: content, OriginServerTS: time.Now().UnixMilli(), Depth: hs.Room.getNextDepth(tx, mapping.RoomID)}
	if err = tx.Create(&event).Error; err != nil {
		return err
	}
	if err = hs.queueBridgeFederation(tx, &event); err != nil {
		return err
	}
	if err = tx.Create(&MatrixDMReactionReference{EventID: event.EventID, RoomID: event.RoomID, StatusID: job.StatusID, AccountID: actor.Id, ReactionID: job.ReactionID, Name: key}).Error; err != nil {
		return err
	}
	changed[event.RoomID] = true
	return nil
}
func (hs *HomeServer) redactBridgeReaction(tx *gorm.DB, reference MatrixDMReactionReference, changed map[string]bool) error {
	var target MatrixEvent
	if err := tx.First(&target, "event_id = ? AND room_id = ?", reference.EventID, reference.RoomID).Error; err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(reference.EventID))
	txn := "$bridge-reaction-undo-" + hex.EncodeToString(sum[:])
	var count int64
	if err := tx.Model(&MatrixEvent{}).Where("txn_key = ?", txn).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var room MatrixRoom
	if err := tx.First(&room, "room_id = ?", reference.RoomID).Error; err != nil {
		return err
	}
	event := MatrixEvent{EventID: txn, TxnKey: &txn, RoomID: reference.RoomID, Sender: room.Creator, EventType: EventTypeRoomRedaction, Redacts: &target.EventID, Content: map[string]any{}, OriginServerTS: time.Now().UnixMilli(), Depth: hs.Room.getNextDepth(tx, reference.RoomID)}
	if err := tx.Create(&event).Error; err != nil {
		return err
	}
	if err := hs.queueBridgeFederation(tx, &event); err != nil {
		return err
	}
	if err := applyStoredRedactionTx(tx, reference.RoomID, reference.EventID); err != nil {
		return err
	}
	changed[reference.RoomID] = true
	return nil
}
