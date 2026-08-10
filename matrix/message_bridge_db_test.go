package matrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// isEventProcessed (DB-backed)
// ====================================================================

func TestMessageBridge_IsEventProcessed(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	// Initially not processed.
	assert.False(t, mb.isEventProcessed("$unprocessed:example.com"))

	// Create a message mapping.
	msg := &models.MatrixMessage{
		StatusId:       1,
		MatrixEventID:  "$processed:example.com",
		MatrixRoomID:   "!room:example.com",
		MatrixSenderID: "@sender:example.com",
		Direction:      "from_matrix",
	}
	require.NoError(t, db.Create(msg).Error)

	assert.True(t, mb.isEventProcessed("$processed:example.com"))
}

// ====================================================================
// createMessageMapping (DB-backed)
// ====================================================================

func TestMessageBridge_CreateMessageMapping(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	event := &Event{
		EventID: "$maptest:example.com",
		RoomID:  "!maproom:example.com",
		Sender:  "@mapper:example.com",
	}

	err := mb.createMessageMapping(123, event)
	require.NoError(t, err)

	// Verify mapping.
	var msg models.MatrixMessage
	require.NoError(t, db.Where("matrix_event_id = ?", "$maptest:example.com").First(&msg).Error)
	assert.Equal(t, uint64(123), msg.StatusId)
	assert.Equal(t, "!maproom:example.com", msg.MatrixRoomID)
	assert.Equal(t, "from_matrix", msg.Direction)
}

// ====================================================================
// GetStatusByEventID (DB-backed)
// ====================================================================

func TestMessageBridge_GetStatusByEventID(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	conv := &models.Conversation{AccountsIds: []uint64{1}}
	require.NoError(t, db.Create(conv).Error)

	status := &models.Status{
		AccountId:      1,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: &conv.Id,
	}
	require.NoError(t, db.Create(status).Error)

	msg := &models.MatrixMessage{
		StatusId:       status.Id,
		MatrixEventID:  "$statustest:example.com",
		MatrixRoomID:   "!room:example.com",
		MatrixSenderID: "@s:example.com",
		Direction:      "from_matrix",
	}
	require.NoError(t, db.Create(msg).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	got, err := mb.GetStatusByEventID("$statustest:example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, status.Id, got.Id)
}

func TestMessageBridge_GetStatusByEventID_NotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	got, err := mb.GetStatusByEventID("$notfound:example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ====================================================================
// GetEventIDByStatusID (DB-backed)
// ====================================================================

func TestMessageBridge_GetEventIDByStatusID(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	msg := &models.MatrixMessage{
		StatusId:       500,
		MatrixEventID:  "$byStatus:example.com",
		MatrixRoomID:   "!room:example.com",
		MatrixSenderID: "@s:example.com",
		Direction:      "from_matrix",
	}
	require.NoError(t, db.Create(msg).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	eventID, err := mb.GetEventIDByStatusID(500)
	require.NoError(t, err)
	assert.Equal(t, "$byStatus:example.com", eventID)
}

func TestMessageBridge_GetEventIDByStatusID_NotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	eventID, err := mb.GetEventIDByStatusID(99999)
	require.NoError(t, err)
	assert.Equal(t, "", eventID)
}

// ====================================================================
// SendLocalMessageToMatrix (DB-backed + mock HS)
// ====================================================================

func TestMessageBridge_SendLocalMessageToMatrix(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "sender"}
	require.NoError(t, db.Create(account).Error)

	// Create local matrix user mapping.
	matrixUser := &models.MatrixUser{
		LocalAccountId:  account.Id,
		MatrixUserID:    "@_c_sender:example.com",
		MatrixLocalpart: "_c_sender",
		MatrixServer:    "example.com",
		Direction:       "local",
	}
	require.NoError(t, db.Create(matrixUser).Error)

	conv := &models.Conversation{AccountsIds: []uint64{account.Id}}
	require.NoError(t, db.Create(conv).Error)

	// Pre-create the room mapping so GetOrCreateRoomByConversation hits DB.
	room := &models.MatrixRoom{
		ConversationId:      conv.Id,
		MatrixRoomID:        "!sendroom:example.com",
		MatrixRoomLocalpart: "sendroom",
		MatrixServer:        "example.com",
		IsDirect:            true,
	}
	require.NoError(t, db.Create(room).Error)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"event_id": "$sent:example.com"})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	eventID, err := mb.SendLocalMessageToMatrix(conv.Id, account.Id, "hello matrix")
	require.NoError(t, err)
	assert.Equal(t, "$sent:example.com", eventID)
}

func TestMessageBridge_SendLocalMessageToMatrix_UserNotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	// Account doesn't exist -> GetMatrixUserID -> GetOrCreateMatrixUserByLocal fails.
	_, err := mb.SendLocalMessageToMatrix(1, 99999, "hello")
	require.Error(t, err)
}

// ====================================================================
// HandleMatrixMessage (DB-backed full path)
// ====================================================================

func TestMessageBridge_HandleMatrixMessage_AlreadyProcessed(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Pre-create the message mapping to simulate already processed.
	msg := &models.MatrixMessage{
		StatusId:       1,
		MatrixEventID:  "$alreadydone:example.com",
		MatrixRoomID:   "!room:example.com",
		MatrixSenderID: "@s:example.com",
		Direction:      "from_matrix",
	}
	require.NoError(t, db.Create(msg).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)

	err := mb.HandleMatrixMessage(&Event{
		EventID: "$alreadydone:example.com",
		RoomID:  "!room:example.com",
		Sender:  "@s:example.com",
		Content: map[string]any{
			"msgtype": MsgTypeText,
			"body":    "duplicate",
		},
	})
	require.NoError(t, err)
}
