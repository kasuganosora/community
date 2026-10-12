package homeserver

import (
	"context"
	"encoding/json"
	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

// clientHistoryReader is request-local: historical state is shared between page
// batches, never cached across users or membership changes.
type clientHistoryReader struct {
	conn                      *gorm.DB
	roomID, userID            string
	member                    *MatrixRoomMember
	state                     map[string]MatrixEvent
	sharedChecked, everJoined bool
	legacy                    *legacyLocalHistory
	roomVersion               *string // loaded once per request, not once per event
}

func newClientHistoryReader(conn *gorm.DB, roomID, userID string) (*clientHistoryReader, error) {
	r := &clientHistoryReader{conn: conn, roomID: roomID, userID: userID, state: map[string]MatrixEvent{}}
	var member MatrixRoomMember
	err := conn.Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err == nil {
		r.member = &member
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return r, nil
}
func (r *clientHistoryReader) filter(events []MatrixEvent) ([]MatrixEvent, error) {
	ids := map[string]bool{}
	for _, event := range events {
		if event.RoomID != r.roomID || len(event.StateBefore) > historicalStateMaxEvents {
			continue
		}
		for _, id := range event.StateBefore {
			if _, ok := r.state[id]; !ok {
				ids[id] = true
			}
		}
	}
	if len(ids) > 0 {
		keys := make([]string, 0, len(ids))
		for id := range ids {
			keys = append(keys, id)
		}
		for start := 0; start < len(keys); start += 500 {
			batch := keys[start:min(start+500, len(keys))]
			var states []MatrixEvent
			if err := r.conn.Select("event_id", "room_id", "event_type", "state_key", "content").Where("room_id = ? AND event_id IN ?", r.roomID, batch).Find(&states).Error; err != nil {
				return nil, err
			}
			for _, id := range batch {
				r.state[id] = MatrixEvent{}
			}
			for _, state := range states {
				r.state[state.EventID] = state
			}
		}
	}
	visible := make([]MatrixEvent, 0, len(events))
	for _, event := range events {
		if event.RoomID != r.roomID {
			continue
		}
		if r.member != nil && r.member.Forgotten {
			continue
		}
		visibility, membership := "shared", "leave"
		membershipEventID := ""
		states := []MatrixEvent{}
		if event.StateSnapshotKnown {
			valid := len(event.StateBefore) <= historicalStateMaxEvents
			tuples := map[historicalStateTuple]bool{}
			for _, id := range event.StateBefore {
				state, ok := r.state[id]
				if !ok || id == event.EventID || state.EventID == "" || state.RoomID != r.roomID || state.StateKey == nil {
					valid = false
					break
				}
				tuple := historicalStateTuple{string(state.EventType), *state.StateKey}
				if tuples[tuple] {
					valid = false
					break
				}
				tuples[tuple] = true
			}
			if !valid {
				continue
			}
			for _, id := range event.StateBefore {
				if state := r.state[id]; state.EventID != "" {
					states = append(states, state)
				}
			}
		} else {
			// Do not substitute today's room state or timestamps for event DAG state.
			if event.FederationJSON == "" {
				if r.legacy == nil {
					var err error
					r.legacy, err = newLegacyLocalHistory(r.conn, r.roomID)
					if err != nil {
						continue
					}
				}
				var err error
				states, err = r.legacy.stateBefore(event, 0)
				if err != nil || !r.legacy.hasVisibilityEvidence(event, states) {
					continue
				}
			} else {
				if r.roomVersion == nil {
					var room MatrixRoom
					if err := r.conn.Select("room_id", "room_version").Where("room_id = ?", r.roomID).First(&room).Error; err != nil {
						return nil, err
					}
					r.roomVersion = &room.RoomVersion
				}
				pdu, err := parseAndValidatePDU(*r.roomVersion, []byte(event.FederationJSON))
				if err != nil {
					continue
				}
				historical, err := stateBeforePDU(r.conn, pdu)
				if err != nil {
					continue
				}
				for _, p := range historical {
					var content map[string]any
					if json.Unmarshal(p.Content(), &content) != nil {
						continue
					}
					states = append(states, MatrixEvent{EventID: p.EventID(), RoomID: r.roomID, EventType: EventType(p.Type()), StateKey: p.StateKey(), Content: content})
				}

			}
		}
		for _, state := range states {
			if state.EventType == EventTypeRoomHistoryVisibility {
				if value, ok := state.Content["history_visibility"].(string); ok {
					visibility = value
				}
			}
			if state.EventType == EventTypeRoomMember && state.StateKey != nil && *state.StateKey == r.userID {
				membership, _ = state.Content["membership"].(string)
				membershipEventID = state.EventID
			}
		}
		// The transition event is visible under the less restrictive before/after
		// state (including a user's own invitation/join).
		if event.EventType == EventTypeRoomHistoryVisibility {
			if next, ok := event.Content["history_visibility"].(string); ok && historyVisibilityRank(next) < historyVisibilityRank(visibility) {
				visibility = next
			}
		}
		if event.EventType == EventTypeRoomMember && event.StateKey != nil && *event.StateKey == r.userID {
			next, _ := event.Content["membership"].(string)
			if next == "join" || (next == "invite" && membership != "join") {
				membership = next
			}
		}
		allowed := false
		switch visibility {
		case "world_readable":
			allowed = true
		case "shared":
			allowed = membership == "join" || (r.member != nil && r.member.Membership == MembershipJoin)
			if !allowed && r.member != nil && (r.member.Membership == MembershipLeave || r.member.Membership == MembershipBan) && membershipEventID != r.member.EventID {
				if !r.sharedChecked {
					var history []MatrixEvent
					if err := r.conn.Select("content").Where("room_id = ? AND event_type = ? AND state_key = ?", r.roomID, EventTypeRoomMember, r.userID).Find(&history).Error; err != nil {
						return nil, err
					}
					for _, entry := range history {
						if entry.Content["membership"] == "join" {
							r.everJoined = true
							break
						}
					}
					r.sharedChecked = true
				}
				allowed = r.everJoined
			}
		case "invited":
			allowed = membership == "join" || membership == "invite"
		case "joined":
			allowed = membership == "join"
		}
		if allowed {
			visible = append(visible, event)
		}
	}
	return visible, nil
}
func historyVisibilityRank(value string) int {
	switch value {
	case "world_readable":
		return 0
	case "shared":
		return 1
	case "invited":
		return 2
	default:
		return 3
	}
}
func canReadClientEvent(roomID, userID, eventID string) bool {
	return canReadClientEventContext(context.Background(), roomID, userID, eventID)
}

// canReadClientEventContext honours the request context, so deriving the
// historical state of one event in a big room stops when the client goes away.
func canReadClientEventContext(ctx context.Context, roomID, userID, eventID string) bool {
	conn := db.GetDB().WithContext(ctx)
	reader, err := newClientHistoryReader(conn, roomID, userID)
	if err != nil {
		return false
	}
	var event MatrixEvent
	if conn.Where("room_id = ? AND event_id = ?", roomID, eventID).First(&event).Error != nil {
		return false
	}
	visible, err := reader.filter([]MatrixEvent{event})
	return err == nil && len(visible) == 1
}
