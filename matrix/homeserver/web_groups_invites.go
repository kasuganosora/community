package homeserver

import (
	"encoding/json"
	"errors"
	"strings"

	gmsl "github.com/matrix-org/gomatrixserverlib"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Invite stripped state is display-only, never inserted into MatrixRoomState.
// A missing room row previously made valid external invitations disappear from
// both the native and Community room lists. Full state remains unknown until
// the join response has been independently verified.
func persistWebInvitePreview(tx *gorm.DB, p gmsl.PDU, preview []json.RawMessage) error {
	var room MatrixRoom
	err := tx.Where("room_id = ?", p.RoomID().String()).First(&room).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	room = MatrixRoom{RoomID: p.RoomID().String(), RoomVersion: string(p.Version()), Visibility: "private", JoinRule: "invite", HistoryVisibility: "joined", GuestAccess: "forbidden"}
	for _, raw := range preview {
		var event struct {
			Type     string         `json:"type"`
			StateKey string         `json:"state_key"`
			Content  map[string]any `json:"content"`
		}
		if len(raw) > 65536 || json.Unmarshal(raw, &event) != nil || event.StateKey != "" {
			continue
		}
		text := func(key string, limit int) *string {
			value, ok := event.Content[key].(string)
			if !ok || len(value) > limit {
				return nil
			}
			return &value
		}
		switch event.Type {
		case "m.room.name":
			room.Name = text("name", 255)
		case "m.room.topic":
			room.Topic = text("topic", 512)
		case "m.room.avatar":
			if value := text("url", 512); value != nil && strings.HasPrefix(*value, "mxc://") {
				room.AvatarURL = value
			}
		case "m.room.create":
			if value := text("type", 32); value != nil && *value == "m.space" {
				room.RoomType = value
			}
		case "m.room.encryption":
			room.IsEncrypted = true
		}
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&room).Error
}
