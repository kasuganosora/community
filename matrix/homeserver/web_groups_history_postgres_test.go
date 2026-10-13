package homeserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// PostgreSQL sorts NULL first by default for DESC. Legacy events have a zero
// stream position and may lack created_at; the history cursor must still visit
// every dated and undated message, once each, across page boundaries.
func TestWebGroupPostgresHistoryIncludesUndatedLegacyMessages(t *testing.T) {
	conn := postgresUnreadTestDB(t)
	require.NoError(t, conn.Exec(`CREATE TEMP TABLE matrix_room_members (
		id bigint PRIMARY KEY, room_id text, user_id text, membership text,
		forgotten boolean, event_id text
	) ON COMMIT DROP`).Error)
	const room, user, member = "!legacy:history.test", "@alice:history.test", "$joined"
	require.NoError(t, conn.Exec(`INSERT INTO matrix_room_members
		(id,room_id,user_id,membership,forgotten,event_id) VALUES (1,?,?,?,false,?)`,
		room, user, "join", member).Error)
	require.NoError(t, conn.Exec(`INSERT INTO matrix_events
		(event_id,room_id,event_type,sender,state_key,content,stream_position,state_snapshot_known)
		VALUES (?,?,?,?,?,?,0,true)`, member, room, "m.room.member", user, user,
		`{"membership":"join"}`).Error)
	now := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		id string
		at any
	}{
		{"$undated-a", nil},
		{"$newer", now},
		{"$undated-b", nil},
		{"$older", now.Add(-time.Hour)},
	} {
		require.NoError(t, conn.Exec(`INSERT INTO matrix_events
			(event_id,room_id,event_type,sender,content,state_before,state_snapshot_known,stream_position,created_at)
			VALUES (?,?,?,?,?,?,true,0,?)`, row.id, room, "m.room.message", user,
			`{"msgtype":"m.text","body":"legacy"}`, `["$joined"]`, row.at).Error)
	}
	var cursor *webGroupHistoryCursor
	var ids []string
	for i := 0; i < 5; i++ {
		page, next, err := webGroupHistory(conn, room, user, cursor, 1)
		require.NoError(t, err)
		for _, event := range page {
			ids = append(ids, event.EventID)
		}
		cursor = next
		if next == nil {
			break
		}
	}
	require.Nil(t, cursor, "all legacy messages must be exhausted")
	require.Equal(t, []string{"$newer", "$older", "$undated-b", "$undated-a"}, ids)
}
