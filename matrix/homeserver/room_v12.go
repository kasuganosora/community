package homeserver

import (
	"time"

	"github.com/google/uuid"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
)

// Version 12 identifies a room by the signed creation event's reference hash.
// Finalize that identity before any membership or state references are stored.
func (r *RoomService) initializeDomainlessRoom(tx *gorm.DB, room *MatrixRoom, create *MatrixEvent) error {
	version, err := gmsl.GetRoomVersion(gmsl.RoomVersion(room.RoomVersion))
	if err != nil {
		return err
	}
	builder := version.NewEventBuilder()
	builder.Type, builder.SenderID, builder.StateKey = string(create.EventType), create.Sender, create.StateKey
	builder.Depth = 1
	builder.PrevEvents, builder.AuthEvents = []string{}, []string{}
	// Separate otherwise identical creations within the same millisecond.
	create.Content["org.community.creation_nonce"] = uuid.NewString()
	if err = builder.SetContent(create.Content); err != nil {
		return err
	}
	pdu, err := builder.Build(time.UnixMilli(create.OriginServerTS), spec.ServerName(r.HS.Config.ServerName), gmsl.KeyID(r.HS.keyID), r.HS.privateKey)
	if err != nil {
		return err
	}
	auth, err := gmsl.NewAuthEvents(nil)
	if err != nil {
		return err
	}
	if err = gmsl.Allowed(pdu, auth, matrixUserIDForSender); err != nil {
		return err
	}
	roomID := pdu.RoomID().String()
	if err = tx.Model(room).Update("room_id", roomID).Error; err != nil {
		return err
	}
	room.RoomID = roomID
	*create = *pduToMatrixEvent(pdu)
	create.StateSnapshotKnown = true
	create.StateBefore = []string{}
	return nil
}
