package homeserver

import (
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// Count only message events whose authenticated historical state includes this
// user as joined. This is deliberately conservative for pre-snapshot imports:
// missing history evidence cannot disclose activity from before an invitation.
// Stream positions are local receipt order, not a remote server's wall clock.
func webGroupUnreadCounts(conn *gorm.DB, ids []string, user string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	receipts := conn.Table("matrix_receipts r").Select("r.room_id, MAX(e.stream_position) AS position, MAX(e.created_at) AS created_at").Joins("JOIN matrix_events e ON e.event_id = r.event_id").Where("r.user_id = ? AND r.room_id IN ? AND r.receipt_type IN ? AND (r.thread_id = '' OR r.thread_id IS NULL OR r.thread_id = 'main')", user, ids, []string{"m.read", "m.read.private"}).Group("r.room_id")
	relation := "json_extract(e.content, '$.\"m.relates_to\".rel_type')"
	membership := dialect.JSONText(conn, "jm.content", "membership")
	contains := "EXISTS (SELECT 1 FROM json_each(e.state_before) st WHERE st.value = jm.event_id)"
	switch conn.Dialector.Name() {
	case "postgres":
		relation = "(e.content::jsonb -> 'm.relates_to' ->> 'rel_type')"
		// MatrixEvent.Content is serialized TEXT, unlike StateBefore's JSONB.
		// PostgreSQL requires an explicit conversion before JSON operators.
		membership = "(jm.content::jsonb ->> 'membership')"
		contains = "(e.state_before::jsonb @> jsonb_build_array(jm.event_id))"
	case "mysql":
		relation = "JSON_UNQUOTE(JSON_EXTRACT(e.content, '$.\"m.relates_to\".rel_type'))"
		contains = "JSON_CONTAINS(e.state_before, JSON_QUOTE(jm.event_id))"
	}
	joins := conn.Table("matrix_events jm").Select("1").Where("jm.room_id = e.room_id AND jm.event_type = ? AND jm.state_key = ?", EventTypeRoomMember, user).Where(membership+" = ?", "join").Where(contains)
	var rows []struct {
		RoomID string
		Count  int64
	}
	err := conn.Table("matrix_events e").Select("e.room_id, COUNT(*) AS count").Joins("LEFT JOIN (?) p ON p.room_id = e.room_id", receipts).
		Where("e.room_id IN ? AND e.sender <> ? AND e.event_type IN ? AND e.state_snapshot_known = ?", ids, user, []EventType{EventTypeRoomMessage, "m.room.encrypted"}, true).
		Where("e.stream_position > COALESCE(p.position,0) OR (e.stream_position = 0 AND COALESCE(p.position,0) = 0 AND (p.created_at IS NULL OR e.created_at > p.created_at))").
		Where("COALESCE("+relation+",'') <> ?", "m.replace").
		Where("NOT EXISTS (SELECT 1 FROM matrix_events rd WHERE rd.room_id = e.room_id AND rd.redacts = e.event_id)").
		Where("EXISTS (?)", joins).Group("e.room_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.RoomID] = r.Count
	}
	return out, nil
}
