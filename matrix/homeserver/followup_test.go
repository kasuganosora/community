package homeserver

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// countEventIDLookups counts queries that look matrix_events up with a single
// "event_id = ?" predicate (the shape of First/Take by event ID) and found nothing.
func countEventIDLookups(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	conn := db.GetDB()
	name := fmt.Sprintf("test:eventid_%d", time.Now().UnixNano())
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "matrix_events") && strings.Contains(sql, "event_id = ?") && !strings.Contains(sql, "room_id") {
			if tx.Statement.RowsAffected == 0 {
				n.Add(1)
			}
		}
	}))
	t.Cleanup(func() { conn.Callback().Query().Remove(name) })
	return &n
}

// seedInterleavedRoom stores a timeline of newest-first-visible messages
// interleaved with member events that have no verifiable history, as a
// partial-state join leaves it: every `every`-th event (from the oldest) is a message.
func seedInterleavedRoom(t *testing.T, roomID, userID string, total, every int) (messageIDs []string) {
	t.Helper()
	conn := db.GetDB()
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: userID, Membership: MembershipJoin}).Error)
	require.NoError(t, conn.Create(&MatrixRoom{RoomID: roomID, RoomVersion: "10"}).Error)
	tag := strings.NewReplacer("!", "", ":", "", ".", "").Replace(roomID)
	rows := make([]MatrixEvent, 0, total)
	for i := 0; i < total; i++ {
		if i%every == 0 {
			id := fmt.Sprintf("$%s-msg%05d", tag, i)
			messageIDs = append(messageIDs, id)
			rows = append(rows, MatrixEvent{EventID: id, RoomID: roomID, EventType: EventTypeRoomMessage, Sender: userID, OriginServerTS: int64(1000 + i), StateSnapshotKnown: true, Content: map[string]any{"body": i}})
			continue
		}
		key := fmt.Sprintf("@m%05d:remote.example", i)
		rows = append(rows, MatrixEvent{EventID: fmt.Sprintf("$%s-mem%05d", tag, i), RoomID: roomID, EventType: EventTypeRoomMember, StateKey: &key, Sender: key, OriginServerTS: int64(1000 + i), FederationJSON: "{not a pdu", Content: map[string]any{"membership": "join"}})
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).CreateInBatches(rows, 200).Error)
	return messageIDs
}

func TestMessagesFillPageAcrossInterleavedMemberEventsAndOmitEndWhenExhausted(t *testing.T) {
	hs := createTestHomeServer(t)
	roomID, userID := "!interleaved:test.local", "@reader:test.local"
	// 1 message per 40 events: a limit-5 page must scan ~200 rows to fill.
	msgs := seedInterleavedRoom(t, roomID, userID, 2000, 40)
	require.Len(t, msgs, 50)

	var seen []string
	from, pages := "", 0
	for {
		page, merr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 5, From: from})
		require.Nil(t, merr)
		pages++
		for _, ev := range page.Chunk {
			seen = append(seen, ev.EventID)
		}
		if page.End == "" {
			require.Less(t, len(page.Chunk), 5, "a full page always carries a token; only the final short page omits it")
			break
		}
		require.Len(t, page.Chunk, 5, "scan budget is not hit, so every page with a token is full")
		require.NotEqual(t, from, page.End)
		from = page.End
		require.Less(t, pages, 100)
	}
	require.Len(t, seen, 50)
	for i, id := range seen {
		require.Equal(t, msgs[len(msgs)-1-i], id)
	}
	require.Equal(t, 11, pages, "10 full pages of 5 carry tokens; the 11th is empty and ends the timeline")
}

func TestMessagesShortPageWithBudgetExhaustedKeepsEnd(t *testing.T) {
	t.Setenv("MATRIX_HISTORY_SCAN_MAX", "100")
	hs := createTestHomeServer(t)
	roomID, userID := "!interleaved2:test.local", "@reader:test.local"
	seedInterleavedRoom(t, roomID, userID, 2000, 40)
	page, merr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 5})
	require.Nil(t, merr)
	require.Less(t, len(page.Chunk), 5)
	require.NotEmpty(t, page.End, "budget exhausted with history left: client must be able to continue")
	// A room that is fully consumed returns a short page without a token.
	rid2 := "!interleaved3:test.local"
	seedInterleavedRoom(t, rid2, userID, 30, 10)
	last, merr := hs.Event.GetRoomMessages(rid2, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 50})
	require.Nil(t, merr)
	require.Len(t, last.Chunk, 3)
	require.Empty(t, last.End)
}

func TestBulkPersistIssuesBoundedEventIDLookups(t *testing.T) {
	hs := createTestHomeServer(t)
	user := "@alice:test.local"
	roomID := createRoomWithUser(t, hs, user)
	var room MatrixRoom
	require.NoError(t, db.GetDB().Where("room_id = ?", roomID).First(&room).Error)
	const n = 60
	ids := make([]string, n)
	for i := range ids {
		ids[i] = sendMessage(t, hs, roomID, user, fmt.Sprintf("m%d", i))
	}
	pdus := make([]gmsl.PDU, n)
	for i, id := range ids {
		pdus[i] = historicalPDU(t, id, room.RoomVersion)
	}
	// Newest first: every predecessor is absent when its successor is stored,
	// the shape of a partial-state room whose history was never stored.
	reverse := func() []gmsl.PDU {
		out := make([]gmsl.PDU, n)
		for i := range pdus {
			out[n-1-i] = pdus[i]
		}
		return out
	}
	remove := func() {
		require.NoError(t, db.GetDB().Unscoped().Where("event_id IN ?", ids).Delete(&MatrixEvent{}).Error)
	}
	conn := db.GetDB()
	wrap := func(f func(*gorm.DB) error) error { return conn.Transaction(f) }

	remove()
	lookups := countEventIDLookups(t)
	stored, err := persistVerifiedPDUsBulk(conn, hs, reverse(), false, false, wrap)
	require.NoError(t, err)
	require.Equal(t, n, stored)
	// Per event the old path issued: own-ID load, existence check and one
	// predecessor load (3*60 = 180 single-row lookups, all empty here).
	require.LessOrEqual(t, lookups.Load(), int64(1), "absence is established by chunked IN queries, not per event")

	var count int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("event_id IN ?", ids).Count(&count).Error)
	require.EqualValues(t, n, count)

	// Idempotent: a second pass stores nothing and stays bounded.
	lookups2 := countEventIDLookups(t)
	stored, err = persistVerifiedPDUsBulk(conn, hs, reverse(), false, false, wrap)
	require.NoError(t, err)
	require.Zero(t, stored)
	require.Zero(t, lookups2.Load())

	// In-order storage (predecessors present) still works and keeps snapshots.
	remove()
	stored, err = persistVerifiedPDUsBulk(conn, hs, pdus, false, false, wrap)
	require.NoError(t, err)
	require.Equal(t, n, stored)
	var snap int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("event_id IN ? AND state_snapshot_known = ?", ids, true).Count(&snap).Error)
	require.EqualValues(t, n, snap, "predecessors stored earlier in the same batch are found, so the snapshot is known")
}
