package homeserver

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Only session-local temporary tables are used; no application tables or
// schemas in the supplied PostgreSQL database are changed.
func postgresUnreadTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("COMMUNITY_GROUPS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set COMMUNITY_GROUPS_POSTGRES_DSN for PostgreSQL group read regressions")
	}
	conn, err := gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	tx := conn.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { tx.Rollback() })
	require.NoError(t, tx.Exec(`CREATE TEMP TABLE matrix_events (
		event_id TEXT PRIMARY KEY, room_id TEXT NOT NULL, event_type TEXT NOT NULL,
		sender TEXT NOT NULL, state_key TEXT, content TEXT, state_before JSONB,
		state_snapshot_known BOOLEAN, stream_position BIGINT DEFAULT 0,
		created_at TIMESTAMPTZ, redacts TEXT, federation_json TEXT
	) ON COMMIT DROP`).Error)
	require.NoError(t, tx.Exec(`CREATE TEMP TABLE matrix_receipts (
		room_id TEXT, user_id TEXT, event_id TEXT, receipt_type TEXT, thread_id TEXT
	) ON COMMIT DROP`).Error)
	require.NoError(t, tx.Exec("SET LOCAL search_path = pg_temp").Error)
	return tx
}

func TestWebGroupPostgresUnreadTextContent(t *testing.T) {
	conn := postgresUnreadTestDB(t)
	const room, user = "!unread:local.test", "@alice:local.test"
	require.NoError(t, conn.Exec(`INSERT INTO matrix_events
		(event_id,room_id,event_type,sender,state_key,content,state_before,state_snapshot_known,stream_position,created_at)
		VALUES
		('$join', ?, 'm.room.member', ?, ?, '{"membership":"join"}', '[]', true, 1, now()),
		('$visible', ?, 'm.room.message', '@bob:local.test', NULL, '{"msgtype":"m.text","body":"new message"}', '["$join"]', true, 2, now()),
		('$before-join', ?, 'm.room.message', '@bob:local.test', NULL, '{"msgtype":"m.text","body":"private history"}', '[]', true, 3, now()),
		('$edit', ?, 'm.room.message', '@bob:local.test', NULL, '{"msgtype":"m.text","m.relates_to":{"rel_type":"m.replace","event_id":"$visible"}}', '["$join"]', true, 4, now())`, room, user, user, room, room, room).Error)
	var storageType string
	require.NoError(t, conn.Raw("SELECT pg_typeof(content)::text FROM matrix_events LIMIT 1").Scan(&storageType).Error)
	require.Equal(t, "text", storageType, "must match MatrixEvent.Content, not hide the bug with a JSONB fixture")
	// Reproduce the deployed failure against the exact storage type, then
	// roll back only this deliberately failing statement before testing the fix.
	require.NoError(t, conn.SavePoint("uncast_membership").Error)
	var ignored string
	uncastErr := conn.Raw("SELECT content->>'membership' FROM matrix_events WHERE event_id = '$join'").Scan(&ignored).Error
	var postgresErr *pgconn.PgError
	require.ErrorAs(t, uncastErr, &postgresErr)
	require.Equal(t, "42883", postgresErr.Code)
	require.NoError(t, conn.RollbackTo("uncast_membership").Error)
	counts, err := webGroupUnreadCounts(conn, []string{room}, user)
	require.NoError(t, err)
	require.Equal(t, int64(1), counts[room], "only visible messages count; edits and pre-join history must not count")
	require.NoError(t, conn.Exec("INSERT INTO matrix_receipts VALUES (?, ?, '$visible', 'm.read', NULL)", room, user).Error)
	counts, err = webGroupUnreadCounts(conn, []string{room}, user)
	require.NoError(t, err)
	require.Zero(t, counts[room], "read receipts must remove the visible unread message")
}
