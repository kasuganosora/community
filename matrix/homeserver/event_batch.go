package homeserver

import "gorm.io/gorm"

// batchClientEvents preserves single-event conversion semantics, including
// device-scoped transaction IDs, with one latest-redaction query for the batch.
func batchClientEvents(conn *gorm.DB, events []MatrixEvent, deviceID string) ([]*ClientEvent, error) {
	result := make([]*ClientEvent, 0, len(events))
	ids := []string{}
	rooms := map[string]bool{}
	for i := range events {
		event := &events[i]
		result = append(result, event.clientEventWithoutRedaction(deviceID))
		if event.EventType != EventTypeRoomRedaction && event.EventID != "" && event.RoomID != "" {
			ids = append(ids, event.EventID)
			rooms[event.RoomID] = true
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	roomIDs := make([]string, 0, len(rooms))
	for room := range rooms {
		roomIDs = append(roomIDs, room)
	}
	var rows []MatrixEvent
	err := conn.Raw(`SELECT * FROM (SELECT matrix_events.*, ROW_NUMBER() OVER (PARTITION BY room_id,redacts ORDER BY origin_server_ts DESC,event_id ASC) AS redaction_rank FROM matrix_events WHERE room_id IN ? AND redacts IN ?) AS latest WHERE redaction_rank = 1`, roomIDs, ids).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	type key struct{ room, id string }
	latest := map[key]MatrixEvent{}
	for _, row := range rows {
		if row.Redacts != nil {
			latest[key{row.RoomID, *row.Redacts}] = row
		}
	}
	for _, event := range result {
		if event.Type == string(EventTypeRoomRedaction) {
			continue
		}
		redaction, ok := latest[key{event.RoomID, event.EventID}]
		if !ok {
			continue
		}
		if event.Unsigned == nil {
			event.Unsigned = map[string]any{}
		}
		event.Unsigned["redacted_because"] = map[string]any{"event_id": redaction.EventID, "type": string(redaction.EventType), "room_id": redaction.RoomID, "sender": redaction.Sender, "origin_server_ts": redaction.OriginServerTS, "content": redaction.Content, "redacts": event.EventID}
	}
	return result, nil
}
