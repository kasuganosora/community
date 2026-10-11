package homeserver

import (
	"errors"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"gorm.io/gorm"
)

// Derive privileged creators from the immutable, verified create event. Keeping
// these separate from users prevents writing forbidden creator entries into the
// v12 power-level event and covers all client permission prechecks consistently.
func (r *RoomService) getRoomPowerLevels(roomID string) (*PowerLevelContent, error) {
	levels, err := r.getRoomPowerLevelsContent(roomID)
	if err != nil {
		return nil, err
	}
	var room MatrixRoom
	err = db.GetDB().Select("room_version").Where("room_id = ?", roomID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return levels, nil
	}
	if err != nil {
		return nil, err
	}
	version, err := gmsl.GetRoomVersion(gmsl.RoomVersion(room.RoomVersion))
	if err != nil || !version.PrivilegedCreators() {
		return levels, nil
	}
	var state MatrixRoomState
	if err = db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, EventTypeRoomCreate, "").First(&state).Error; err != nil {
		return nil, err
	}
	create, err := eventPDUFromDB(db.GetDB(), state.EventID, room.RoomVersion)
	if err != nil {
		return nil, err
	}
	levels.privilegedCreators = make(map[string]struct{})
	for _, userID := range gmsl.CreatorsFromCreateEvent(create) {
		levels.privilegedCreators[userID] = struct{}{}
	}
	return levels, nil
}

func privilegedEventCreator(p gmsl.PDU, create gmsl.PDU) bool {
	version, err := gmsl.GetRoomVersion(p.Version())
	if err != nil || !version.PrivilegedCreators() || create.Type() != "m.room.create" || create.RoomID() != p.RoomID() {
		return false
	}
	for _, creator := range gmsl.CreatorsFromCreateEvent(create) {
		if creator == string(p.SenderID()) {
			return true
		}
	}
	return false
}
