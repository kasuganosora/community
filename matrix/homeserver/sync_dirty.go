package homeserver

import (
	"time"

	"gorm.io/gorm"
)

// changedJoinedRooms is a conservative, request-local candidate set. Filtering
// and historical authorization remain the room builder's responsibility.
func (s *SyncService) changedJoinedRooms(userID string, joinedRooms []string, since int64) (map[string]bool, error) {
	changed := make(map[string]bool)
	if len(joinedRooms) == 0 {
		return changed, nil
	}
	sinceTime := time.UnixMilli(since)
	queries := []*gorm.DB{
		s.syncDB().Model(&MatrixEvent{}).Where("room_id IN ?", joinedRooms).
			Where("created_at > ? OR (created_at IS NULL AND origin_server_ts > ?)", sinceTime, since),
		s.syncDB().Model(&MatrixAccountData{}).Where("room_id IN ? AND user_id = ? AND updated_at > ?", joinedRooms, userID, sinceTime),
		s.syncDB().Model(&MatrixReceipt{}).Where("room_id IN ? AND ts > ?", joinedRooms, since).
			Where("receipt_type <> ? OR user_id = ?", "m.read.private", userID),
		s.syncDB().Model(&MatrixRoomMember{}).Where("room_id IN ? AND user_id = ? AND membership = ? AND updated_at > ?", joinedRooms, userID, MembershipJoin, sinceTime),
	}
	for _, query := range queries {
		var rooms []string
		if err := query.Distinct("room_id").Pluck("room_id", &rooms).Error; err != nil {
			return nil, err
		}
		for _, room := range rooms {
			changed[room] = true
		}
	}
	typingChangeTracker.RLock()
	for _, room := range joinedRooms {
		if typingChangeTracker.rooms[room] > since {
			changed[room] = true
		}
	}
	typingChangeTracker.RUnlock()
	// Device-list generation still visits the full membership set. Without these
	// negative entries it would reload unchanged membership history per room.
	if s.joinTransitions == nil {
		s.joinTransitions = make(map[syncJoinKey]bool)
	}
	for _, room := range joinedRooms {
		if !changed[room] {
			s.joinTransitions[syncJoinKey{roomID: room, userID: userID, since: since}] = false
		}
	}
	return changed, nil
}

// addNewSessionUsers queries each distinct peer once, regardless of how many
// rooms are shared. Bounded IN lists also work for large membership sets.
func (s *SyncService) addNewSessionUsers(members, changed, left map[string]bool, since int64) {
	users := make([]string, 0, len(members))
	for user := range members {
		if !changed[user] && !left[user] {
			users = append(users, user)
		}
	}
	for start := 0; start < len(users); start += 500 {
		end := min(start+500, len(users))
		var active []string
		err := s.syncDB().Model(&MatrixUserSession{}).Where("user_id IN ? AND created_at > ?", users[start:end], time.UnixMilli(since)).Distinct("user_id").Pluck("user_id", &active).Error
		if err != nil {
			s.noteReadError(err)
			return
		}
		for _, user := range active {
			changed[user] = true
		}
	}
}
