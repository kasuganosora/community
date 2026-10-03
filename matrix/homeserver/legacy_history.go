package homeserver

import (
	"errors"
	"fmt"
	"gorm.io/gorm"
	"sort"
)

// Legacy UUID events remain local data, never invented signed PDUs. New local
// writes can nevertheless record trustworthy before-state under the room lock.
func captureLegacyLocalState(tx *gorm.DB, event *MatrixEvent, room *MatrixRoom) error {
	var state []MatrixRoomState
	if err := tx.Where("room_id = ?", event.RoomID).Find(&state).Error; err != nil {
		return err
	}
	if len(state) > historicalStateMaxEvents {
		return errors.New("legacy room state exceeds limit")
	}
	ids := make([]string, 0, len(state))
	for _, s := range state {
		ids = append(ids, s.EventID)
	}
	rows, err := loadLocalSnapshot(tx, event.RoomID, ids)
	if err != nil {
		return err
	}
	byID := map[string]MatrixEvent{}
	for _, row := range rows {
		byID[row.EventID] = row
	}
	hasVisibility := false
	for _, s := range state {
		row := byID[s.EventID]
		if row.EventType != s.EventType || row.StateKey == nil || *row.StateKey != s.StateKey || !contentEqual(row.Content, s.Content) {
			return errors.New("legacy current state does not match stored event")
		}
		if s.EventType == EventTypeRoomHistoryVisibility {
			hasVisibility = true
		}
	}
	if !hasVisibility && room.HistoryVisibility != "" && room.HistoryVisibility != "shared" {
		return errors.New("legacy room lacks provable history visibility state")
	}
	sort.Strings(ids)
	event.StateBefore = ids
	event.StateSnapshotKnown = true
	return nil
}

func loadLocalSnapshot(conn *gorm.DB, roomID string, ids []string) ([]MatrixEvent, error) {
	if len(ids) > historicalStateMaxEvents {
		return nil, errors.New("historical snapshot exceeds limit")
	}
	rows := []MatrixEvent{}
	for start := 0; start < len(ids); start += 500 {
		var batch []MatrixEvent
		if err := conn.Select("event_id", "room_id", "event_type", "state_key", "content").Where("room_id = ? AND event_id IN ?", roomID, ids[start:min(start+500, len(ids))]).Find(&batch).Error; err != nil {
			return nil, err
		}
		rows = append(rows, batch...)
	}
	byID := map[string]MatrixEvent{}
	for _, row := range rows {
		byID[row.EventID] = row
	}
	tuples := map[historicalStateTuple]bool{}
	ordered := make([]MatrixEvent, 0, len(ids))
	for _, id := range ids {
		row, ok := byID[id]
		if !ok || row.StateKey == nil {
			return nil, errors.New("missing or invalid historical state reference")
		}
		tuple := historicalStateTuple{string(row.EventType), *row.StateKey}
		if tuples[tuple] {
			return nil, errors.New("duplicate historical state tuple")
		}
		tuples[tuple] = true
		ordered = append(ordered, row)
	}
	return ordered, nil
}

type legacyLocalHistory struct {
	conn                    *gorm.DB
	roomID, server, creator string
	requireVisibility       bool
	before                  map[string][]MatrixEvent
	active                  map[string]bool
	visited                 map[string]bool
}

func newLegacyLocalHistory(conn *gorm.DB, roomID string) (*legacyLocalHistory, error) {
	hs := GlobalHS
	if hs == nil || hs.Config == nil {
		return nil, errors.New("local history identity unavailable")
	}
	var room MatrixRoom
	if err := conn.Where("room_id = ?", roomID).First(&room).Error; err != nil {
		return nil, err
	}
	if extractServerName(roomID) != hs.Config.ServerName || extractServerName(room.Creator) != hs.Config.ServerName {
		return nil, errors.New("unsigned history is not a locally created room")
	}
	return &legacyLocalHistory{conn: conn, roomID: roomID, server: hs.Config.ServerName, creator: room.Creator, requireVisibility: room.HistoryVisibility != "" && room.HistoryVisibility != "shared", before: map[string][]MatrixEvent{}, active: map[string]bool{}, visited: map[string]bool{}}, nil
}
func (r *legacyLocalHistory) stateBefore(event MatrixEvent, depth int) ([]MatrixEvent, error) {
	if err := r.conn.Statement.Context.Err(); err != nil {
		return nil, err
	}
	if event.RoomID != r.roomID || extractServerName(event.Sender) != r.server {
		return nil, errors.New("unsigned historical event is not local")
	}
	if depth > historicalStateMaxDepth || len(r.visited) >= historicalStateMaxEvents {
		return nil, errors.New("legacy historical traversal exceeds limit")
	}
	if state, ok := r.before[event.EventID]; ok {
		return state, nil
	}
	if r.active[event.EventID] {
		return nil, errors.New("legacy historical cycle")
	}
	r.active[event.EventID] = true
	r.visited[event.EventID] = true
	defer delete(r.active, event.EventID)
	if event.StateSnapshotKnown {
		for _, id := range event.StateBefore {
			if id == event.EventID {
				return nil, errors.New("self-referencing snapshot")
			}
		}
		state, err := loadLocalSnapshot(r.conn, r.roomID, event.StateBefore)
		if err == nil {
			r.before[event.EventID] = state
		}
		return state, err
	}
	if event.EventType == EventTypeRoomCreate && len(event.PrevEvents) == 0 && event.Sender == r.creator {
		r.before[event.EventID] = []MatrixEvent{}
		return r.before[event.EventID], nil
	}
	if len(event.PrevEvents) == 0 || len(event.PrevEvents) > historicalStateMaxPredecessors {
		return nil, errors.New("legacy historical predecessor evidence unavailable")
	}
	var resolved map[historicalStateTuple]MatrixEvent
	for _, id := range event.PrevEvents {
		var previous MatrixEvent
		if err := r.conn.Where("room_id = ? AND event_id = ?", r.roomID, id).First(&previous).Error; err != nil {
			return nil, err
		}
		state, err := r.stateBefore(previous, depth+1)
		if err != nil {
			return nil, err
		}
		branch := map[historicalStateTuple]MatrixEvent{}
		for _, entry := range state {
			branch[historicalStateTuple{string(entry.EventType), *entry.StateKey}] = entry
		}
		if previous.StateKey != nil {
			branch[historicalStateTuple{string(previous.EventType), *previous.StateKey}] = previous
		}
		if resolved == nil {
			resolved = branch
			continue
		}
		if len(branch) != len(resolved) {
			return nil, errors.New("unsigned legacy state fork cannot be resolved")
		}
		for tuple, entry := range branch {
			if resolved[tuple].EventID != entry.EventID {
				return nil, fmt.Errorf("unsigned legacy state conflict %s", tuple.eventType)
			}
		}
	}

	result := make([]MatrixEvent, 0, len(resolved))
	for _, entry := range resolved {
		result = append(result, entry)
	}
	r.before[event.EventID] = result
	return result, nil
}

func (r *legacyLocalHistory) hasVisibilityEvidence(event MatrixEvent, state []MatrixEvent) bool {
	if !r.requireVisibility {
		return true
	}
	for _, entry := range state {
		if entry.EventType == EventTypeRoomHistoryVisibility && entry.StateKey != nil && *entry.StateKey == "" {
			return true
		}
	}
	// A stored visibility transition itself supplies explicit local evidence.
	return event.EventType == EventTypeRoomHistoryVisibility && event.StateKey != nil && *event.StateKey == ""
}
