package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== matrixErrorStatus tests =====

func TestMatrixErrorStatus(t *testing.T) {
	tests := []struct {
		name     string
		errCode  string
		expected int
	}{
		{"M_FORBIDDEN", "M_FORBIDDEN", http.StatusForbidden},
		{"M_USER_DEACTIVATED", "M_USER_DEACTIVATED", http.StatusForbidden},
		{"M_NOT_FOUND", "M_NOT_FOUND", http.StatusNotFound},
		{"M_UNAUTHORIZED", "M_UNAUTHORIZED", http.StatusUnauthorized},
		{"M_UNKNOWN_TOKEN", "M_UNKNOWN_TOKEN", http.StatusUnauthorized},
		{"M_MISSING_TOKEN", "M_MISSING_TOKEN", http.StatusUnauthorized},
		{"M_LIMIT_EXCEEDED", "M_LIMIT_EXCEEDED", http.StatusTooManyRequests},
		{"M_TOO_LARGE", "M_TOO_LARGE", http.StatusRequestEntityTooLarge},
		{"M_USER_IN_USE", "M_USER_IN_USE", http.StatusBadRequest},
		{"M_INVALID_USERNAME", "M_INVALID_USERNAME", http.StatusBadRequest},
		{"M_EXCLUSIVE", "M_EXCLUSIVE", http.StatusBadRequest},
		{"unknown defaults to 400", "M_SOMETHING_ELSE", http.StatusBadRequest},
		{"empty defaults to 400", "", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &MatrixError{ErrCode: tt.errCode}
			result := matrixErrorStatus(err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ===== validateFilterSchema tests =====

func TestValidateFilterSchema_Valid(t *testing.T) {
	tests := []struct {
		name   string
		filter map[string]any
	}{
		{
			name:   "empty filter",
			filter: map[string]any{},
		},
		{
			name: "valid object fields",
			filter: map[string]any{
				"presence":     map[string]any{},
				"account_data": map[string]any{},
				"room":         map[string]any{},
			},
		},
		{
			name: "valid event_fields array",
			filter: map[string]any{
				"event_fields": []any{"type", "content"},
			},
		},
		{
			name: "valid room sub-filters",
			filter: map[string]any{
				"room": map[string]any{
					"state":    map[string]any{},
					"timeline": map[string]any{},
					"rooms":    []any{"!room:server"},
				},
			},
		},
		{
			name: "valid sub-filter arrays",
			filter: map[string]any{
				"room": map[string]any{
					"timeline": map[string]any{
						"types":   []any{"m.room.message"},
						"senders": []any{"@alice:server"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilterSchema(tt.filter)
			assert.NoError(t, err)
		})
	}
}

func TestValidateFilterSchema_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		filter    map[string]any
		errSubstr string
	}{
		{
			name:      "presence not object",
			filter:    map[string]any{"presence": "notobject"},
			errSubstr: "'presence' must be an object",
		},
		{
			name:      "account_data not object",
			filter:    map[string]any{"account_data": 123},
			errSubstr: "'account_data' must be an object",
		},
		{
			name:      "room not object",
			filter:    map[string]any{"room": "string"},
			errSubstr: "'room' must be an object",
		},
		{
			name:      "event_fields not array",
			filter:    map[string]any{"event_fields": "notarray"},
			errSubstr: "'event_fields' must be an array",
		},
		{
			name: "room.state not object",
			filter: map[string]any{
				"room": map[string]any{"state": "bad"},
			},
			errSubstr: "'room.state' must be an object",
		},
		{
			name: "room.rooms not array",
			filter: map[string]any{
				"room": map[string]any{"rooms": "bad"},
			},
			errSubstr: "'room.rooms' must be an array",
		},
		{
			name: "room.timeline.types not array",
			filter: map[string]any{
				"room": map[string]any{
					"timeline": map[string]any{"types": "bad"},
				},
			},
			errSubstr: "'room.timeline.types' must be an array",
		},
		{
			name: "room.timeline.types element not string",
			filter: map[string]any{
				"room": map[string]any{
					"timeline": map[string]any{"types": []any{123}},
				},
			},
			errSubstr: "must be strings",
		},
		{
			name: "invalid room ID in rooms",
			filter: map[string]any{
				"room": map[string]any{
					"timeline": map[string]any{"rooms": []any{"badroom"}},
				},
			},
			errSubstr: "invalid room ID",
		},
		{
			name: "invalid user ID in senders",
			filter: map[string]any{
				"room": map[string]any{
					"timeline": map[string]any{"senders": []any{"notvalid"}},
				},
			},
			errSubstr: "invalid user ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilterSchema(tt.filter)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errSubstr)
		})
	}
}

// ===== handleVersions tests =====

func TestHandleVersions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/client/versions", nil)

	handleVersions(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	versions, ok := resp["versions"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, versions)
	assert.Contains(t, versions, "v1.11")

	unstable, ok := resp["unstable_features"].(map[string]any)
	require.True(t, ok)
	assert.True(t, unstable["org.matrix.e2e_cross_signing"].(bool))
}

// ===== handleGetCapabilities tests =====

func TestHandleGetCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{RoomVersion: "10"}})
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/capabilities", nil)

	handleGetCapabilities(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	caps, ok := resp["capabilities"].(map[string]any)
	require.True(t, ok)

	roomVersions, ok := caps["m.room_versions"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "10", roomVersions["default"])

	changePass, ok := caps["m.change_password"].(map[string]any)
	require.True(t, ok)
	assert.True(t, changePass["enabled"].(bool))
}

// ===== handleGetTurnServer tests =====

func TestHandleGetTurnServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/voip/turnServer", nil)

	handleGetTurnServer(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(86400), resp["ttl"])
}

// ===== handleGetThirdPartyProtocol tests =====

func TestHandleGetThirdPartyProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/thirdparty/protocol", nil)

	handleGetThirdPartyProtocol(c)

	// Protocol not configured → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

// ===== handleGetThirdPartyUser / Location (empty) =====

func TestHandleGetThirdPartyUser_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/thirdparty/user", nil)

	handleGetThirdPartyUser(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleGetThirdPartyLocation_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/thirdparty/location", nil)

	handleGetThirdPartyLocation(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== MatrixError.ToJSON tests =====

func TestMatrixError_ToJSON(t *testing.T) {
	err := &MatrixError{ErrCode: "M_TEST", Error: "test message"}
	data := err.ToJSON()
	require.NotEmpty(t, data)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "M_TEST", parsed["errcode"])
	assert.Equal(t, "test message", parsed["error"])
}

func TestMatrixError_ToJSON_SpecialChars(t *testing.T) {
	err := &MatrixError{ErrCode: "M_TEST", Error: `message with "quotes"`}
	data := err.ToJSON()
	require.NotEmpty(t, data)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, `message with "quotes"`, parsed["error"])
}

// ===== syncResponseHasData tests =====

func TestSyncResponseHasData_Empty(t *testing.T) {
	resp := &SyncResponse{DeviceLists: &DeviceLists{}}
	assert.False(t, syncResponseHasData(resp))
}

func TestSyncResponseHasData_JoinedTimeline(t *testing.T) {
	resp := &SyncResponse{
		Rooms: &SyncRooms{
			Join: map[string]*JoinedRoom{
				"!room:test": {Timeline: &SyncTimeline{Events: []*ClientEvent{{EventID: "$e:test"}}}},
			},
		},
		DeviceLists: &DeviceLists{},
	}
	assert.True(t, syncResponseHasData(resp))
}

func TestSyncResponseHasData_Invite(t *testing.T) {
	resp := &SyncResponse{
		Rooms: &SyncRooms{
			Invite: map[string]*InvitedRoom{
				"!room:test": {},
			},
		},
		DeviceLists: &DeviceLists{},
	}
	assert.True(t, syncResponseHasData(resp))
}

func TestSyncResponseHasData_ToDevice(t *testing.T) {
	resp := &SyncResponse{
		ToDevice:    &SyncToDevice{Events: []*ToDeviceEvent{{Sender: "@a:test"}}},
		DeviceLists: &DeviceLists{},
	}
	assert.True(t, syncResponseHasData(resp))
}

func TestSyncResponseHasData_AccountData(t *testing.T) {
	resp := &SyncResponse{
		AccountData: &SyncAccountData{Events: []*ClientEvent{{EventID: "$ad:test"}}},
		DeviceLists: &DeviceLists{},
	}
	assert.True(t, syncResponseHasData(resp))
}

func TestSyncResponseHasData_DeviceListsChanged(t *testing.T) {
	resp := &SyncResponse{
		DeviceLists: &DeviceLists{Changed: []string{"@user:test"}},
	}
	assert.True(t, syncResponseHasData(resp))
}

func TestSyncResponseHasData_DeviceListsLeft(t *testing.T) {
	resp := &SyncResponse{
		DeviceLists: &DeviceLists{Left: []string{"@user:test"}},
	}
	assert.True(t, syncResponseHasData(resp))
}

// ===== ParseMatrixUserID tests =====

func TestParseMatrixUserID(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		localpart string
		domain    string
		expectOK  bool
	}{
		{"valid", "@alice:example.com", "alice", "example.com", true},
		{"valid with dashes", "@alice-bob:my-server.com", "alice-bob", "my-server.com", true},
		{"missing @", "alice:example.com", "", "", false},
		{"missing colon", "@alice", "", "", false},
		{"empty", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lp, domain, ok := ParseMatrixUserID(tt.input)
			assert.Equal(t, tt.expectOK, ok)
			if tt.expectOK {
				assert.Equal(t, tt.localpart, lp)
				assert.Equal(t, tt.domain, domain)
			}
		})
	}
}

// ===== FormatMatrixUserID tests =====

func TestFormatMatrixUserID(t *testing.T) {
	result := FormatMatrixUserID("alice", "example.com")
	assert.Equal(t, "@alice:example.com", result)
}
