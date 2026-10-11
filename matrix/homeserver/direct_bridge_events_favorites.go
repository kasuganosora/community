package homeserver

import (
	"errors"
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"time"
)

// Kept separate from emoji reaction identities and counters.
type MatrixDMFavoriteReference struct {
	FavoriteID uint64 `gorm:"primaryKey;autoIncrement:false"`
	EventID    string `gorm:"size:255;uniqueIndex"`
	RoomID     string `gorm:"size:255"`
	StatusID   uint64 `gorm:"index"`
}

func (hs *HomeServer) processBridgeFavourite(tx *gorm.DB, job *models.MatrixDMEventJob, changed map[string]bool) error {
	var ref MatrixDMFavoriteReference
	err := tx.First(&ref, "favorite_id = ?", job.ReactionID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if job.Kind == "FavoriteUndo" {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return hs.redactBridgeReaction(tx, MatrixDMReactionReference{EventID: ref.EventID, RoomID: ref.RoomID, StatusID: ref.StatusID}, changed)
	}
	if err == nil {
		return nil
	}
	var favorite models.FavoriteStatus
	if err = tx.First(&favorite, "id = ? AND account_id = ? AND status_id = ?", job.ReactionID, job.AccountID, job.StatusID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var status models.Status
	if err = tx.Preload("ToAccounts").Where("id = ? AND deleted_at IS NULL", job.StatusID).First(&status).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var actor models.Account
	if err = tx.First(&actor, job.AccountID).Error; err != nil {
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
	sender := hs.bridgeAccountID(actor)
	var member MatrixRoomMember
	if err = tx.Where("room_id = ? AND user_id = ? AND membership = ?", mapping.RoomID, sender, MembershipJoin).First(&member).Error; err != nil {
		return errDirectMessageSkipped
	}
	var encryption int64
	if err = tx.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", mapping.RoomID, EventTypeRoomEncryption).Count(&encryption).Error; err != nil {
		return err
	}
	if encryption > 0 {
		return errDirectMessageSkipped
	}
	txn := fmt.Sprintf("$bridge-favorite-%d", favorite.Id)
	event := MatrixEvent{EventID: txn, TxnKey: &txn, RoomID: mapping.RoomID, Sender: sender, EventType: "m.reaction", Content: map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": mapping.EventID, "key": "Like"}, "org.community.activitypub": map[string]any{"type": "Like", "favourite": true}}, OriginServerTS: time.Now().UnixMilli(), Depth: hs.Room.getNextDepth(tx, mapping.RoomID)}
	if err = tx.Create(&event).Error; err != nil {
		return err
	}
	if err = hs.queueBridgeFederation(tx, &event); err != nil {
		return err
	}
	if err = tx.Create(&MatrixDMFavoriteReference{FavoriteID: favorite.Id, EventID: event.EventID, RoomID: event.RoomID, StatusID: job.StatusID}).Error; err != nil {
		return err
	}
	changed[event.RoomID] = true
	return nil
}
