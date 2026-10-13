package homeserver

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// assignWebGroupStream runs inside the event insert transaction. Holding the
// room row until commit means a reader never skips a lower, uncommitted event.
// Different rooms remain independent; there is no server-wide sequence lock.
func assignWebGroupStream(tx *gorm.DB, event *MatrixEvent) error {
	event.RelationType = webGroupString(webGroupRelation(event.Content), "rel_type")
	event.RelationTarget = webGroupString(webGroupRelation(event.Content), "event_id")
	var room MatrixRoom
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("room_id", "web_stream_position").Where("room_id = ?", event.RoomID).Take(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} // imported auth before its room exists
	if err != nil {
		return err
	}
	event.StreamPosition = room.WebStreamPosition + 1
	return tx.Model(&MatrixRoom{}).Where("room_id = ?", event.RoomID).UpdateColumn("web_stream_position", event.StreamPosition).Error
}
