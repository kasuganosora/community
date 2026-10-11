package homeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// normalizeBridgeReplacement authenticates the original identity before treating
// an edit as a delete/repost. The caller retains the old audience and performs
// Delete(old)+Create(new) in its existing durable outbound transaction.
func (hs *HomeServer) normalizeBridgeReplacement(tx *gorm.DB, event *MatrixEvent) (*MatrixEvent, *models.Status, error) {
	relation, _ := event.Content["m.relates_to"].(map[string]any)
	if relation["rel_type"] != "m.replace" {
		return event, nil, nil
	}
	targetID, _ := relation["event_id"].(string)
	if targetID == "" {
		return nil, nil, errDirectMessageSkipped
	}
	var target MatrixEvent
	if err := tx.Where("event_id = ? AND room_id = ?", targetID, event.RoomID).First(&target).Error; err != nil {
		return nil, nil, err
	}
	if target.Sender != event.Sender || target.EventType != EventTypeRoomMessage {
		return nil, nil, errDirectMessageSkipped
	}
	var mapping models.MatrixDMDelivery
	if err := resolveDirectEventStatus(tx, event.RoomID, targetID, &mapping); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errOutboundParentPending
		}
		return nil, nil, err
	}
	var status models.Status
	if err := tx.Preload("Account").Preload("ToAccounts").Where("id = ? AND deleted_at IS NULL AND visibility = ?", mapping.StatusID, models.StatusVisibilityDirect).First(&status).Error; err != nil {
		return nil, nil, err
	}
	if status.Account == nil || status.Account.Domain != nil {
		return nil, nil, errDirectMessageSkipped
	}
	actor, err := hs.bridgeEventAccount(tx, event.RoomID, event.Sender)
	if err != nil {
		return nil, nil, err
	}
	if actor.Id != status.AccountId {
		return nil, nil, errDirectMessageSkipped
	}
	content, ok := event.Content["m.new_content"].(map[string]any)
	if !ok {
		return nil, nil, errDirectMessageSkipped
	}
	if content["file"] != nil {
		return nil, nil, errDirectMessageSkipped
	}
	switch content["msgtype"] {
	case "m.text", "m.notice", "m.emote", "m.image", "m.video", "m.audio", "m.file":
	default:
		return nil, nil, errDirectMessageSkipped
	}
	if nested, _ := content["m.relates_to"].(map[string]any); nested["rel_type"] == "m.replace" {
		return nil, nil, errDirectMessageSkipped
	}
	copy := *event
	copy.Content = map[string]any{}
	for k, v := range content {
		copy.Content[k] = v
	}
	return &copy, &status, nil
}

// finalizeBridgeReplacement keeps Matrix's native edited-event presentation,
// while the ActivityPub side is explicitly withdrawn and posted anew. Retarget
// all trusted roots so a later withdrawal of the replacement clears them too.
func (hs *HomeServer) finalizeBridgeReplacement(tx *gorm.DB, event *MatrixEvent, oldStatus, newStatus *models.Status) error {
	if oldStatus == nil {
		return nil
	}
	if oldStatus.AccountId != newStatus.AccountId || oldStatus.Visibility != models.StatusVisibilityDirect || newStatus.Visibility != models.StatusVisibilityDirect {
		return errDirectMessageSkipped
	}
	if err := tx.Delete(oldStatus).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.MatrixDMEventJob{}).Where("id = ?", fmt.Sprintf("ap-delete:%d", oldStatus.Id)).Updates(map[string]any{"state": "delivered", "last_error": "replaced by Matrix edit"}).Error; err != nil {
		return err
	}
	var delivery models.MatrixDMDelivery
	err := tx.Where("status_id = ? AND room_id = ?", oldStatus.Id, event.RoomID).First(&delivery).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil && delivery.EventID != "" {
		ref := MatrixDMEventReference{EventID: delivery.EventID, RoomID: event.RoomID, StatusID: newStatus.Id}
		if err = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_id"}}, DoUpdates: clause.AssignmentColumns([]string{"status_id"})}).Create(&ref).Error; err != nil {
			return err
		}
		if err = tx.Model(&delivery).UpdateColumn("state", "replaced").Error; err != nil {
			return err
		}
	}
	if err = tx.Model(&MatrixDMEventReference{}).Where("room_id = ? AND status_id = ?", event.RoomID, oldStatus.Id).UpdateColumn("status_id", newStatus.Id).Error; err != nil {
		return err
	}
	var room MatrixRoom
	if err = tx.First(&room, "room_id = ?", event.RoomID).Error; err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(event.EventID))
	txn := "$bridge-replaced-" + hex.EncodeToString(sum[:])
	notice := MatrixEvent{EventID: txn, TxnKey: &txn, RoomID: event.RoomID, Sender: room.Creator, EventType: EventTypeRoomMessage, Content: map[string]any{"msgtype": "m.notice", "body": "This edit withdraws the previous ActivityPub message and sends a new message to the same recipients; it is not an in-place federated edit."}, OriginServerTS: time.Now().UnixMilli(), Depth: hs.Room.getNextDepth(tx, event.RoomID)}
	if err = tx.Set("matrix:signer", hs).Create(&notice).Error; err != nil {
		return err
	}
	if err = hs.queueBridgeFederation(tx, &notice); err != nil {
		return err
	}
	return tx.Model(&MatrixRoom{}).Where("room_id = ?", event.RoomID).Updates(map[string]any{"last_event_id": notice.EventID, "last_event_ts": notice.OriginServerTS}).Error
}
