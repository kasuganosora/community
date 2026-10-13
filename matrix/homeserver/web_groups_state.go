package homeserver

import (
	"context"
	"errors"
	"time"

	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Keep web state updates, encryption/upgrade checks, attachment references and
// federation delivery in the same native-room transaction. The callback sees
// the locked current state, so permission patches cannot overwrite a concurrent
// administrator's unrelated user-level changes.
type webGroupStateChange func(*gorm.DB, *MatrixRoom, map[EventType]MatrixRoomState) (map[string]any, *MatrixError)

func webGroupPersistState(ctx context.Context, hs *HomeServer, roomID, user, kind, key, uploadID string, change webGroupStateChange) (*MatrixEvent, *MatrixError) {
	var event *MatrixEvent
	var failure *MatrixError
	err := db.GetDB().WithContext(ctx).Set("matrix:signer", hs).Transaction(func(tx *gorm.DB) error {
		var room MatrixRoom
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&room, "room_id = ?", roomID).Error; err != nil {
			return err
		}
		var member MatrixRoomMember
		if tx.Take(&member, "room_id = ? AND user_id = ? AND membership = ? AND forgotten = ?", roomID, user, MembershipJoin, false).Error != nil {
			failure = ErrForbidden
			return errors.New("not joined")
		}
		states, err := webGroupStates(tx, []string{roomID})
		if err != nil {
			return err
		}
		if failure = webGroupPlaintextState(&room, states[roomID]); failure != nil {
			return errors.New("web group is read only")
		}
		content, merr := change(tx, &room, states[roomID])
		if merr != nil {
			failure = merr
			return errors.New("state change denied")
		}
		if uploadID != "" {
			media, merr := webGroupUploadContent(tx, roomID, user, uploadID)
			if merr != nil {
				failure = merr
				return errors.New("avatar unavailable")
			}
			if kind != "m.room.avatar" || media["msgtype"] != "m.image" {
				failure = ErrInvalidParam
				return errors.New("invalid avatar")
			}
			content = map[string]any{"url": media["url"], "info": media["info"]}
		}
		event = &MatrixEvent{RoomID: roomID, Sender: user, EventType: EventType(kind), StateKey: &key, Content: content, OriginServerTS: time.Now().UnixMilli()}
		// This performs room-version authorization against the locked state.
		if err := preparePersistedLocalPDU(tx, event); err != nil {
			failure = ErrForbidden
			return err
		}
		if err := tx.Create(event).Error; err != nil {
			return err
		}
		if err := hs.Room.updateRoomState(tx, roomID, event); err != nil {
			return err
		}
		if kind == "m.room.member" {
			membership, _ := content["membership"].(string)
			if err := hs.Room.updateRoomMember(tx, roomID, key, MembershipState(membership), event.EventID); err != nil {
				return err
			}
		}
		if uploadID != "" {
			if err := webGroupMarkUploadUsed(tx, event, uploadID); err != nil {
				return err
			}
		}
		if err := tx.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Updates(map[string]any{"last_event_id": event.EventID, "last_event_ts": event.OriginServerTS}).Error; err != nil {
			return err
		}
		return hs.queueNativeFederation(tx, event)
	})
	if err != nil {
		if failure != nil {
			return nil, failure
		}
		return nil, ErrInternal
	}
	go hs.NotifyRoomEvent(roomID)
	return event, nil
}
