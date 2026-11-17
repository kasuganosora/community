package homeserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func installMatrixRoomColumnLengthTriggers(t *testing.T, conn *gorm.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TRIGGER IF NOT EXISTS enforce_matrix_rooms_hs_varchar_insert
		 BEFORE INSERT ON matrix_rooms_hs
		 WHEN (NEW.topic IS NOT NULL AND (length(NEW.topic) > 512 OR instr(NEW.topic, char(0)) > 0))
		   OR (NEW.name IS NOT NULL AND (length(NEW.name) > 255 OR instr(NEW.name, char(0)) > 0))
		   OR (NEW.avatar_url IS NOT NULL AND (length(NEW.avatar_url) > 512 OR instr(NEW.avatar_url, char(0)) > 0))
		 BEGIN
		   SELECT RAISE(ABORT, 'value too long for type character varying(512)');
		 END;`,
		`CREATE TRIGGER IF NOT EXISTS enforce_matrix_rooms_hs_varchar_update
		 BEFORE UPDATE ON matrix_rooms_hs
		 WHEN (NEW.topic IS NOT NULL AND (length(NEW.topic) > 512 OR instr(NEW.topic, char(0)) > 0))
		   OR (NEW.name IS NOT NULL AND (length(NEW.name) > 255 OR instr(NEW.name, char(0)) > 0))
		   OR (NEW.avatar_url IS NOT NULL AND (length(NEW.avatar_url) > 512 OR instr(NEW.avatar_url, char(0)) > 0))
		 BEGIN
		   SELECT RAISE(ABORT, 'value too long for type character varying(512)');
		 END;`,
	}
	for _, stmt := range stmts {
		require.NoError(t, conn.Exec(stmt).Error)
	}
	t.Cleanup(func() {
		_ = conn.Exec(`DROP TRIGGER IF EXISTS enforce_matrix_rooms_hs_varchar_insert;`).Error
		_ = conn.Exec(`DROP TRIGGER IF EXISTS enforce_matrix_rooms_hs_varchar_update;`).Error
	})
}

func setupFederatedRoomWithRemoteStatePower(t *testing.T) (*HomeServer, *HomeServer, *gorm.DB, string) {
	t.Helper()
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	installMatrixRoomColumnLengthTriggers(t, conn)

	remote := minimalHS("remote.example")
	remoteAdmin := "@mod:remote.example"
	created, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{
		Visibility:  "public",
		Preset:      "public_chat",
		RoomVersion: "10",
		PowerLevelContentOverride: &PowerLevelContent{
			Users: map[string]int64{
				"@alice:test.local": 100,
				remoteAdmin:         100,
			},
			StateDefault: 50,
		},
	})
	require.Nil(t, me)

	joinEvt := &MatrixEvent{
		RoomID:         created.RoomID,
		Sender:         remoteAdmin,
		EventType:      EventTypeRoomMember,
		StateKey:       &remoteAdmin,
		Content:        map[string]any{"membership": "join"},
		OriginServerTS: time.Now().UnixMilli(),
	}
	require.NoError(t, buildLocalPDU(conn, remote, joinEvt, "10"))
	joinPDU, err := parseAndValidatePDU("10", []byte(joinEvt.FederationJSON))
	require.NoError(t, err)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		return persistVerifiedPDU(tx, hs, joinPDU, true)
	}))

	return hs, remote, conn, created.RoomID
}

func TestOversizedFederatedRoomTopicDoesNotAbortTransaction(t *testing.T) {
	hs, remote, conn, roomID := setupFederatedRoomWithRemoteStatePower(t)

	oversizedTopic := strings.Repeat("federated-topic-", 60) // 960 ASCII runes (> 512)
	emptyKey := ""
	topicEvt := &MatrixEvent{
		RoomID:         roomID,
		Sender:         "@mod:remote.example",
		EventType:      EventTypeRoomTopic,
		StateKey:       &emptyKey,
		Content:        map[string]any{"topic": oversizedTopic},
		OriginServerTS: time.Now().UnixMilli(),
	}
	require.NoError(t, buildLocalPDU(conn, remote, topicEvt, "10"))
	topicPDU, err := parseAndValidatePDU("10", []byte(topicEvt.FederationJSON))
	require.NoError(t, err)

	err = conn.Transaction(func(tx *gorm.DB) error {
		if err := persistVerifiedPDU(tx, hs, topicPDU, true); err != nil {
			return err
		}
		// Subsequent write in the same transaction must also succeed cleanly.
		return tx.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Update("last_event_id", topicPDU.EventID()).Error
	})
	require.NoError(t, err)

	var room MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", roomID).First(&room).Error)
	require.NotNil(t, room.Topic)
	require.Equal(t, 512, utf8.RuneCountInString(*room.Topic))
	require.True(t, strings.HasPrefix(oversizedTopic, *room.Topic))

	// Canonical Matrix state in matrix_room_state must keep the full untruncated topic.
	var state MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ''", roomID, EventTypeRoomTopic).First(&state).Error)
	require.Equal(t, oversizedTopic, state.Content["topic"])

	// Knock and invite stripped-state previews and CreateRoom must also bound oversized topics.
	knockRoomID := "!knockpreview:remote.example"
	knockEvt := &MatrixEvent{
		RoomID:         knockRoomID,
		Sender:         "@mod:remote.example",
		EventType:      EventTypeRoomCreate,
		StateKey:       &emptyKey,
		Content:        map[string]any{"creator": "@mod:remote.example", "room_version": "10"},
		Depth:          1,
		AuthEvents:     []string{},
		OriginServerTS: time.Now().UnixMilli(),
	}
	require.NoError(t, buildLocalPDU(conn, remote, knockEvt, "10"))
	knockPDU, err := parseAndValidatePDU("10", []byte(knockEvt.FederationJSON))
	require.NoError(t, err)

	eightHundredRunes := strings.Repeat("k", 800) // <= 1024 bytes, > 512 runes
	require.NoError(t, hs.Room.persistKnockPreview(context.Background(), "remote.example", knockPDU, []knockStrippedEvent{
		{Type: EventTypeRoomTopic, StateKey: "", Content: map[string]any{"topic": eightHundredRunes}},
		{Type: EventTypeRoomName, StateKey: "", Content: map[string]any{"name": strings.Repeat("n", 400)}},
	}))
	var knockRoom MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", knockRoomID).First(&knockRoom).Error)
	require.NotNil(t, knockRoom.Topic)
	require.Equal(t, 512, utf8.RuneCountInString(*knockRoom.Topic))
	require.NotNil(t, knockRoom.Name)
	require.Equal(t, 255, utf8.RuneCountInString(*knockRoom.Name))

	createdOversized, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{
		Visibility:  "public",
		Preset:      "public_chat",
		RoomVersion: "10",
		Topic:       &oversizedTopic,
	})
	require.Nil(t, me)
	var localRoom MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", createdOversized.RoomID).First(&localRoom).Error)
	require.NotNil(t, localRoom.Topic)
	require.Equal(t, 512, utf8.RuneCountInString(*localRoom.Topic))
}

func TestRoomTopicUnicodeRuneTruncationAndSanitization(t *testing.T) {
	hs, remote, conn, roomID := setupFederatedRoomWithRemoteStatePower(t)
	emptyKey := ""

	// 300 4-byte emoji runes = 1200 UTF-8 bytes, which is <= 512 runes and must NOT be truncated.
	threeHundredEmoji := strings.Repeat("🎉", 300)
	evt1 := &MatrixEvent{
		RoomID:         roomID,
		Sender:         "@mod:remote.example",
		EventType:      EventTypeRoomTopic,
		StateKey:       &emptyKey,
		Content:        map[string]any{"topic": threeHundredEmoji},
		OriginServerTS: time.Now().UnixMilli(),
	}
	require.NoError(t, buildLocalPDU(conn, remote, evt1, "10"))
	pdu1, err := parseAndValidatePDU("10", []byte(evt1.FederationJSON))
	require.NoError(t, err)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		return persistVerifiedPDU(tx, hs, pdu1, true)
	}))

	var room MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", roomID).First(&room).Error)
	require.NotNil(t, room.Topic)
	require.Equal(t, 300, utf8.RuneCountInString(*room.Topic))
	require.Equal(t, threeHundredEmoji, *room.Topic)

	// Invite stripped-state preview with 300 multi-byte runes (> 512 bytes, <= 512 runes) must preserve it.
	inviteRoomID := "!inviteunicode:remote.example"
	inviteCreate := &MatrixEvent{
		RoomID:         inviteRoomID,
		Sender:         "@mod:remote.example",
		EventType:      EventTypeRoomCreate,
		StateKey:       &emptyKey,
		Content:        map[string]any{"creator": "@mod:remote.example", "room_version": "10"},
		Depth:          1,
		AuthEvents:     []string{},
		OriginServerTS: time.Now().UnixMilli(),
	}
	require.NoError(t, buildLocalPDU(conn, remote, inviteCreate, "10"))
	invitePDU, err := parseAndValidatePDU("10", []byte(inviteCreate.FederationJSON))
	require.NoError(t, err)
	rawTopicPreview, err := json.Marshal(map[string]any{
		"type":      "m.room.topic",
		"state_key": "",
		"content":   map[string]any{"topic": threeHundredEmoji},
	})
	require.NoError(t, err)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		return persistWebInvitePreview(tx, invitePDU, []json.RawMessage{rawTopicPreview})
	}))
	var inviteRoom MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", inviteRoomID).First(&inviteRoom).Error)
	require.NotNil(t, inviteRoom.Topic)
	require.Equal(t, threeHundredEmoji, *inviteRoom.Topic)

	// 600 multi-byte CJK + emoji runes with an embedded NUL byte must strip NUL and truncate cleanly at 512 runes.
	sixHundredMixed := "初\x00" + strings.Repeat("测🎉", 300) // 601 non-NUL runes
	evt2 := &MatrixEvent{
		RoomID:         roomID,
		Sender:         "@mod:remote.example",
		EventType:      EventTypeRoomTopic,
		StateKey:       &emptyKey,
		Content:        map[string]any{"topic": sixHundredMixed},
		OriginServerTS: time.Now().UnixMilli() + 1,
	}
	require.NoError(t, buildLocalPDU(conn, remote, evt2, "10"))
	pdu2, err := parseAndValidatePDU("10", []byte(evt2.FederationJSON))
	require.NoError(t, err)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		return persistVerifiedPDU(tx, hs, pdu2, true)
	}))

	require.NoError(t, conn.Where("room_id = ?", roomID).First(&room).Error)
	require.NotNil(t, room.Topic)
	require.True(t, utf8.ValidString(*room.Topic))
	require.NotContains(t, *room.Topic, "\x00")
	require.Equal(t, 512, utf8.RuneCountInString(*room.Topic))
}

func TestPersistVerifiedPDURollsBackAndCanRetryWhenRoomSummaryWriteFails(t *testing.T) {
	hs, remote, conn, roomID := setupFederatedRoomWithRemoteStatePower(t)
	emptyKey := ""

	topicText := "recoverable federated topic update"
	evt := &MatrixEvent{
		RoomID:         roomID,
		Sender:         "@mod:remote.example",
		EventType:      EventTypeRoomTopic,
		StateKey:       &emptyKey,
		Content:        map[string]any{"topic": topicText},
		OriginServerTS: time.Now().UnixMilli(),
	}
	require.NoError(t, buildLocalPDU(conn, remote, evt, "10"))
	pdu, err := parseAndValidatePDU("10", []byte(evt.FederationJSON))
	require.NoError(t, err)

	require.NoError(t, conn.Exec(`
		CREATE TRIGGER IF NOT EXISTS fail_matrix_rooms_hs_topic_once
		BEFORE UPDATE ON matrix_rooms_hs
		WHEN NEW.topic IS NOT NULL
		BEGIN
		  SELECT RAISE(ABORT, 'simulated transient room summary write failure');
		END;
	`).Error)
	t.Cleanup(func() {
		_ = conn.Exec(`DROP TRIGGER IF EXISTS fail_matrix_rooms_hs_topic_once;`).Error
	})

	err = conn.Transaction(func(tx *gorm.DB) error {
		return persistVerifiedPDU(tx, hs, pdu, true)
	})
	require.Error(t, err, "persistVerifiedPDU must propagate updateRoomState failures so the transaction rolls back cleanly")

	var storedEvents int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("event_id = ?", pdu.EventID()).Count(&storedEvents).Error)
	require.Zero(t, storedEvents, "rolled-back transaction must not leave MatrixEvent stored without room state")

	var storedState int64
	require.NoError(t, conn.Model(&MatrixRoomState{}).Where("event_id = ?", pdu.EventID()).Count(&storedState).Error)
	require.Zero(t, storedState, "rolled-back transaction must not leave partial MatrixRoomState")

	require.NoError(t, conn.Exec(`DROP TRIGGER fail_matrix_rooms_hs_topic_once;`).Error)

	// Retry must succeed cleanly once the transient error clears.
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		return persistVerifiedPDU(tx, hs, pdu, true)
	}))

	var room MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", roomID).First(&room).Error)
	require.NotNil(t, room.Topic)
	require.Equal(t, topicText, *room.Topic)
}
