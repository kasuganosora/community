package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This small operation record stores only a pending signed invitation. It is
// not a second room/member/message store. The stable payload survives a timeout
// between remote countersigning and local commit; completed entries prevent a
// create retry from re-inviting a user who already declined the invitation.
type WebGroupInvitation struct {
	ID          string `gorm:"primaryKey;size:64"`
	RoomID      string `gorm:"size:255;index"`
	Sender      string `gorm:"size:255"`
	Target      string `gorm:"size:255"`
	State       string `gorm:"size:16"`
	Lease       string `gorm:"size:36"`
	LeaseUntil  time.Time
	PreparedPDU string   `gorm:"type:longtext"`
	RoomVersion string   `gorm:"size:16"`
	StateBefore []string `gorm:"type:json;serializer:json"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (WebGroupInvitation) TableName() string { return "matrix_web_group_invitations" }

var errWebInviteForbidden = errors.New("group invitation forbidden")
var errWebInviteBusy = errors.New("group invitation in progress")

func webGroupInviteAllowed(tx *gorm.DB, hs *HomeServer, room *MatrixRoom, sender, target string) (MembershipState, error) {
	var member MatrixRoomMember
	if tx.Where("room_id = ? AND user_id = ? AND membership = ?", room.RoomID, sender, MembershipJoin).Take(&member).Error != nil {
		return "", errWebInviteForbidden
	}
	states, err := webGroupStates(tx, []string{room.RoomID})
	if err != nil {
		return "", err
	}
	caps := webGroupCapabilities(hs, room, MembershipJoin, states[room.RoomID], sender)
	if caps["invite"] != true {
		return "", errWebInviteForbidden
	}
	var recipient MatrixRoomMember
	err = tx.Where("room_id = ? AND user_id = ?", room.RoomID, target).Take(&recipient).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if recipient.Membership == MembershipBan {
		return "", errWebInviteForbidden
	}
	return recipient.Membership, nil
}

func (hs *HomeServer) inviteWebGroupUser(ctx context.Context, roomID, sender, target, reason, transaction string) *MatrixError {
	if sender == target {
		return ErrInvalidParam
	}
	if transaction == "" {
		transaction = uuid.NewString()
	}
	key := sha256.Sum256([]byte(roomID + "\x00" + sender + "\x00" + transaction + "\x00" + target))
	id := hex.EncodeToString(key[:])
	lease := uuid.NewString()
	conn := db.GetDB().WithContext(ctx).Set("matrix:signer", hs)
	var operation WebGroupInvitation
	complete := false
	err := conn.Transaction(func(tx *gorm.DB) error {
		// All group mutations take the room lock first, avoiding lock inversion.
		var room MatrixRoom
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&room, "room_id = ?", roomID).Error; err != nil {
			return err
		}
		existing := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&operation, "id = ?", id).Error
		if existing == nil && operation.State == "completed" {
			complete = true
			return nil
		}
		if existing != nil && !errors.Is(existing, gorm.ErrRecordNotFound) {
			return existing
		}
		if operation.State == "processing" && operation.LeaseUntil.After(time.Now()) {
			return errWebInviteBusy
		}
		membership, err := webGroupInviteAllowed(tx, hs, &room, sender, target)
		if err != nil {
			return err
		}
		if errors.Is(existing, gorm.ErrRecordNotFound) {
			operation = WebGroupInvitation{ID: id, RoomID: roomID, Sender: sender, Target: target, State: "pending", RoomVersion: room.RoomVersion}
		}
		if membership == MembershipJoin || membership == MembershipInvite {
			operation.State = "completed"
			operation.PreparedPDU = ""
			complete = true
			return tx.Save(&operation).Error
		}
		if operation.PreparedPDU == "" {
			event := &MatrixEvent{RoomID: roomID, EventType: EventTypeRoomMember, Sender: sender, StateKey: &target, Content: map[string]any{"membership": "invite", "reason": reason}, OriginServerTS: time.Now().UnixMilli(), SenderDomain: hs.Config.ServerName}
			if err = preparePersistedLocalPDU(tx, event); err != nil {
				return err
			}
			operation.PreparedPDU = event.FederationJSON
			operation.StateBefore = event.StateBefore
			if operation.PreparedPDU == "" {
				return errWebInviteForbidden
			}
		}
		operation.State = "processing"
		operation.Lease = lease
		operation.LeaseUntil = time.Now().Add(30 * time.Second)
		return tx.Save(&operation).Error
	})
	if err != nil {
		return webGroupInviteError(err)
	}
	if complete {
		return nil
	}
	parsed, err := parseAndValidatePDU(operation.RoomVersion, []byte(operation.PreparedPDU))
	if err != nil {
		return ErrInternal
	}
	event := pduToMatrixEvent(parsed)
	if extractServerName(target) != hs.Config.ServerName {
		networkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = hs.Room.signRemoteInvite(conn.WithContext(networkCtx), event)
		cancel()
		if err != nil {
			// A canceled HTTP request must not leave a lease indefinitely. This bounded
			// update contains no network activity or user content.
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = db.GetDB().WithContext(cleanup).Model(&WebGroupInvitation{}).Where("id = ? AND lease = ?", id, lease).Updates(map[string]any{"state": "pending", "lease": ""}).Error
			cancel()
			return &MatrixError{ErrCode: "M_BAD_GATEWAY"}
		}
	}
	event.StateBefore = operation.StateBefore
	event.StateSnapshotKnown = true
	event.TxnKey = &id
	err = conn.Transaction(func(tx *gorm.DB) error {
		var room MatrixRoom
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&room, "room_id = ?", roomID).Error; err != nil {
			return err
		}
		var job WebGroupInvitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&job, "id = ? AND lease = ?", id, lease).Error; err != nil {
			return err
		}
		membership, err := webGroupInviteAllowed(tx, hs, &room, sender, target)
		if err != nil {
			return err
		}
		if membership != MembershipJoin && membership != MembershipInvite {
			// Recheck authorization against current state after remote I/O. No network
			// request runs while this write transaction holds the room row.
			if err = tx.Create(event).Error; err != nil {
				return err
			}
			if err = hs.Room.updateRoomState(tx, roomID, event); err != nil {
				return err
			}
			if err = hs.Room.updateRoomMember(tx, roomID, target, MembershipInvite, event.EventID); err != nil {
				return err
			}
			if err = hs.queueNativeFederation(tx, event); err != nil {
				return err
			}
		}
		return tx.Model(&job).Updates(map[string]any{"state": "completed", "lease": "", "prepared_pdu": "", "state_before": nil}).Error
	})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = db.GetDB().WithContext(cleanup).Model(&WebGroupInvitation{}).Where("id = ? AND lease = ?", id, lease).Updates(map[string]any{"state": "pending", "lease": ""}).Error
		cancel()
		return webGroupInviteError(err)
	}
	hs.NotifyUser(target)
	go hs.NotifyRoomEvent(roomID)
	return nil
}
func webGroupInviteError(err error) *MatrixError {
	if errors.Is(err, errWebInviteForbidden) {
		return ErrForbidden
	}
	if errors.Is(err, errWebInviteBusy) {
		return &MatrixError{ErrCode: "M_CONFLICT"}
	}
	return ErrInternal
}
