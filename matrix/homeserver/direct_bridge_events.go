package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Only a same-room, authenticated bridge mapping can become a reply relation.
func bridgeReplyRelation(tx *gorm.DB, roomID string, status *models.Status, content map[string]any) error {
	if status.InReplyToId == nil {
		return nil
	}
	var mapping models.MatrixDMDelivery
	err := tx.Where("status_id = ? AND room_id = ? AND state = ? AND event_id <> ''", *status.InReplyToId, roomID, "delivered").First(&mapping).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var target MatrixEvent
	if err = tx.Where("event_id = ? AND room_id = ?", mapping.EventID, roomID).First(&target).Error; err != nil {
		return err
	}
	if target.EventType != EventTypeRoomMessage || target.Content["body"] == nil {
		return nil
	}
	threadRoot := target.EventID
	if relation, ok := target.Content["m.relates_to"].(map[string]any); ok && relation["rel_type"] == "m.thread" {
		if root, ok := relation["event_id"].(string); ok && root != "" {
			var rootEvent MatrixEvent
			if err := tx.Where("event_id = ? AND room_id = ?", root, roomID).First(&rootEvent).Error; err == nil {
				threadRoot = root
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
	}
	content["m.relates_to"] = map[string]any{"rel_type": "m.thread", "event_id": threadRoot, "is_falling_back": true, "m.in_reply_to": map[string]any{"event_id": target.EventID}}
	return nil
}

func (hs *HomeServer) queueDirectBridgeRedaction(tx *gorm.DB, event *MatrixEvent) error {
	if !strings.HasPrefix(event.RoomID, "!apdm_") && !strings.HasPrefix(event.RoomID, "!apchat_") {
		return nil
	}
	if handled, err := hs.queueBridgeReactionRemoval(tx, event); handled || err != nil {
		return err
	}
	if event.Redacts == nil {
		return nil
	}
	var mapping models.MatrixDMDelivery
	err := resolveDirectEventStatus(tx, event.RoomID, *event.Redacts, &mapping)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var target MatrixEvent
	if err = tx.Where("event_id = ? AND room_id = ?", *event.Redacts, event.RoomID).First(&target).Error; err != nil {
		return err
	}
	// Moderator power over a Matrix room never grants another AP actor's identity.
	if target.Sender != event.Sender {
		return errDirectMessageSkipped
	}
	var status models.Status
	err = tx.Preload("Account").Where("id = ? AND deleted_at IS NULL", mapping.StatusID).First(&status).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	actor, resolveErr := hs.bridgeEventAccount(tx, event.RoomID, event.Sender)
	if resolveErr != nil {
		return resolveErr
	}
	if status.Account == nil || status.Account.Domain != nil || actor.Id != status.AccountId {
		return errDirectMessageSkipped
	}
	sum := sha256.Sum256([]byte(event.EventID))
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.MatrixDMEventJob{ID: "mx-delete:" + hex.EncodeToString(sum[:]), Kind: "matrix_delete", StatusID: status.Id, AccountID: status.AccountId, RoomID: event.RoomID, EventID: event.EventID, State: "pending", NextAttemptAt: time.Now()}).Error
}

func (hs *HomeServer) processDirectBridgeEventJobs(ctx context.Context) {
	var jobs []models.MatrixDMEventJob
	if err := db.GetDB().WithContext(ctx).Where("state = ? AND next_attempt_at <= ?", "pending", time.Now()).Order("created_at,id").Limit(20).Find(&jobs).Error; err != nil {
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		_ = hs.ProcessDirectBridgeEvent(ctx, job.ID)
	}
}

func (hs *HomeServer) ProcessDirectBridgeEvent(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, publish := models.BeginDeferredStatusEvents(ctx)
	ctx, publishReactions := models.BeginReactionNotifications(ctx)
	changed := map[string]bool{}
	err := db.GetDB().WithContext(ctx).Set("matrix:signer", hs).Transaction(func(tx *gorm.DB) error {
		var job models.MatrixDMEventJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", id).Error; err != nil {
			return err
		}
		if job.State != "pending" {
			return nil
		}
		switch job.Kind {
		case "Favorite", "FavoriteUndo":
			if err := hs.processBridgeFavourite(tx, &job, changed); err != nil {
				return err
			}
		case "matrix_reaction", "matrix_reaction_undo", "Reaction", "ReactionUndo":
			if err := hs.processBridgeReaction(tx, &job, changed); err != nil {
				return err
			}
		case "matrix_delete":
			var status models.Status
			err := tx.Preload("Account").Where("id = ? AND deleted_at IS NULL", job.StatusID).First(&status).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				if status.AccountId != job.AccountID || status.Visibility != models.StatusVisibilityDirect || status.Account == nil || status.Account.Domain != nil {
					return errDirectMessageSkipped
				}
				if err = tx.Delete(&status).Error; err != nil {
					return err
				}
			}
		case "ap_delete":
			var refs []MatrixDMEventReference
			if err := tx.Where("status_id = ?", job.StatusID).Find(&refs).Error; err != nil {
				return err
			}
			var reactionRefs []MatrixDMReactionReference
			if err := tx.Where("status_id = ?", job.StatusID).Find(&reactionRefs).Error; err != nil {
				return err
			}
			for _, ref := range reactionRefs {
				refs = append(refs, MatrixDMEventReference{EventID: ref.EventID, RoomID: ref.RoomID, StatusID: job.StatusID})
			}
			var favorites []MatrixDMFavoriteReference
			if err := tx.Where("status_id = ?", job.StatusID).Find(&favorites).Error; err != nil {
				return err
			}
			for _, ref := range favorites {
				refs = append(refs, MatrixDMEventReference{EventID: ref.EventID, RoomID: ref.RoomID, StatusID: job.StatusID})
			}
			var delivery models.MatrixDMDelivery
			err := tx.First(&delivery, "status_id = ?", job.StatusID).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil && delivery.EventID != "" {
				refs = append(refs, MatrixDMEventReference{EventID: delivery.EventID, RoomID: delivery.RoomID, StatusID: job.StatusID})
			}
			seen := map[string]bool{}
			for _, ref := range refs {
				if seen[ref.EventID] {
					continue
				}
				seen[ref.EventID] = true
				var target MatrixEvent
				err := tx.Where("event_id = ? AND room_id = ?", ref.EventID, ref.RoomID).First(&target).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				// All references were written atomically by this bridge, never from client metadata.
				sum := sha256.Sum256([]byte(fmt.Sprintf("ap-delete:%d:%s", job.StatusID, target.EventID)))
				txn := "$bridge-delete-" + hex.EncodeToString(sum[:])
				var count int64
				if err := tx.Model(&MatrixEvent{}).Where("txn_key = ?", txn).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					var room MatrixRoom
					if err := tx.First(&room, "room_id = ?", ref.RoomID).Error; err != nil {
						return err
					}
					event := MatrixEvent{EventID: txn, TxnKey: &txn, RoomID: ref.RoomID, Sender: room.Creator, EventType: EventTypeRoomRedaction, Redacts: &target.EventID, Content: map[string]any{"reason": "ActivityPub message withdrawn"}, OriginServerTS: time.Now().UnixMilli(), Depth: hs.Room.getNextDepth(tx, ref.RoomID)}
					if err := tx.Create(&event).Error; err != nil {
						return err
					}
					if err := hs.queueBridgeFederation(tx, &event); err != nil {
						return err
					}
					if err := applyStoredRedactionTx(tx, ref.RoomID, target.EventID); err != nil {
						return err
					}
					if err := tx.Model(&MatrixRoom{}).Where("room_id = ?", ref.RoomID).Updates(map[string]any{"last_event_id": event.EventID, "last_event_ts": event.OriginServerTS}).Error; err != nil {
						return err
					}
					changed[ref.RoomID] = true
				}
			}
		default:
			return errors.New("unsupported bridge action")
		}
		return tx.Model(&job).Updates(map[string]any{"state": "delivered", "last_error": ""}).Error
	})
	if err == nil {
		publish()
		publishReactions()
		for room := range changed {
			hs.NotifyRoomEvent(room)
		}
		return nil
	}
	// Retry bounded batches; the row lock and event transaction key prevent duplicates.
	state := "pending"
	if errors.Is(err, errDirectMessageSkipped) {
		state = "skipped"
	}
	_ = db.GetDB().WithContext(ctx).Model(&models.MatrixDMEventJob{}).Where("id = ? AND state = ?", id, "pending").Updates(map[string]any{"state": state, "last_error": "bridge action could not be delivered", "attempts": gorm.Expr("attempts + 1"), "next_attempt_at": time.Now().Add(time.Minute)}).Error
	return err
}
