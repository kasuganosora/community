package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// Client construction
// ====================================================================

func TestNewClientWithConfig(t *testing.T) {
	cfg := &Config{
		HomeserverURL: "https://matrix.example.com/",
		ASToken:       "as-token",
	}
	c := NewClientWithConfig(cfg)
	require.NotNil(t, c)
	// Trailing slash must be trimmed.
	assert.Equal(t, "https://matrix.example.com", c.homeserverURL)
	assert.Equal(t, "as-token", c.asToken)
	assert.NotNil(t, c.httpClient)
}

func TestClient_SetASToken(t *testing.T) {
	c := NewClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "old"})
	c.SetASToken("new-token")
	assert.Equal(t, "new-token", c.asToken)
}

// ====================================================================
// GetUserID
// ====================================================================

func TestClient_GetUserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "Bearer as-token", r.Header.Get("Authorization"))
		assert.Contains(t, r.URL.Path, "/profile/")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"displayname": "Alice",
			"avatar_url":  "mxc://example.com/a",
		})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as-token"})
	profile, err := c.GetUserID("@alice:example.com")
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, "Alice", profile.DisplayName)
	assert.Equal(t, "mxc://example.com/a", profile.AvatarURL)
}

// ====================================================================
// GetRoomState
// ====================================================================

func TestClient_GetRoomState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/state/")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":    "m.room.name",
			"content": map[string]any{"name": "Test Room"},
		})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as-token"})
	ev, err := c.GetRoomState("!room:example.com", "m.room.name", "")
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Equal(t, "m.room.name", ev.Type)
}

func TestClient_GetRoomState_WithStateKey(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	_, err := c.GetRoomState("!room:example.com", "m.room.member", "@bob:example.com")
	require.NoError(t, err)
	// State key is appended to the path.
	assert.Contains(t, seenPath, "@bob:example.com")
}

// ====================================================================
// GetRoomMembers
// ====================================================================

func TestClient_GetRoomMembers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/members")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chunk": []map[string]any{
				{"type": "m.room.member", "sender": "@alice:example.com"},
				{"type": "m.room.member", "sender": "@bob:example.com"},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	events, err := c.GetRoomMembers("!room:example.com")
	require.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, "@alice:example.com", events[0].Sender)
}

// ====================================================================
// CreateRoom
// ====================================================================

func TestClient_CreateRoom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/createRoom")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"room_id": "!new:example.com"})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	resp, err := c.CreateRoom(&RoomCreateRequest{
		Name: "Room",
	}, "@alice:example.com")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "!new:example.com", resp.RoomID)
}

func TestClient_CreateRoom_NoAsUserID(t *testing.T) {
	var qs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qs = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"room_id": "!r:x"})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	_, err := c.CreateRoom(&RoomCreateRequest{}, "")
	require.NoError(t, err)
	// No user_id query param.
	assert.NotContains(t, qs, "user_id=")
}

// ====================================================================
// InviteUser / JoinRoom / LeaveRoom
// ====================================================================

func TestClient_InviteUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/invite")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	err := c.InviteUser("!room:example.com", "@bob:example.com", "@alice:example.com")
	require.NoError(t, err)
}

func TestClient_JoinRoom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/join")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	err := c.JoinRoom("!room:example.com", "@alice:example.com")
	require.NoError(t, err)
}

func TestClient_LeaveRoom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/leave")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	err := c.LeaveRoom("!room:example.com", "@alice:example.com")
	require.NoError(t, err)
}

// ====================================================================
// SendMessage / SendEvent
// ====================================================================

func TestClient_SendMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Contains(t, r.URL.Path, "/send/m.room.message/")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$evt:x"})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	resp, err := c.SendMessage("!room:example.com", "@alice:example.com", &RoomMessageContent{
		MsgType: MsgTypeText,
		Body:    "hi",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "$evt:x", resp.EventID)
}

func TestClient_SendMessage_NoAsUser(t *testing.T) {
	var qs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qs = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$e:x"})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	_, err := c.SendMessage("!room:example.com", "", &RoomMessageContent{MsgType: MsgTypeText, Body: "x"})
	require.NoError(t, err)
	assert.NotContains(t, qs, "user_id=")
}

func TestClient_SendEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Contains(t, r.URL.Path, "/send/m.custom/")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$custom:x"})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	resp, err := c.SendEvent("!room:example.com", "m.custom", "@alice:example.com", map[string]any{"k": "v"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "$custom:x", resp.EventID)
}

// ====================================================================
// SetDisplayName / SetAvatarURL / RegisterUser / Sync
// ====================================================================

func TestClient_SetDisplayName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Contains(t, r.URL.Path, "displayname")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	err := c.SetDisplayName("@alice:example.com", "New Name")
	require.NoError(t, err)
}

func TestClient_SetAvatarURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Contains(t, r.URL.Path, "avatar_url")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	err := c.SetAvatarURL("@alice:example.com", "mxc://example.com/a")
	require.NoError(t, err)
}

func TestClient_RegisterUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/_matrix/client/v3/register", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), "newuser")
		assert.Contains(t, string(body), "m.login.application_service")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	err := c.RegisterUser("newuser")
	require.NoError(t, err)
}

func TestClient_Sync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/sync")
		assert.Contains(t, r.URL.RawQuery, "user_id=")
		assert.Contains(t, r.URL.RawQuery, "timeout=5000")
		_ = json.NewEncoder(w).Encode(map[string]any{"next_batch": "nb"})
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	resp, err := c.Sync("@alice:example.com", "", 5000)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "nb", resp.NextBatch)
}

// ====================================================================
// Client error paths
// ====================================================================

func TestClient_GetUserID_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"not found"}`))
	}))
	defer srv.Close()

	c := NewClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	_, err := c.GetUserID("@missing:example.com")
	require.Error(t, err)
	// httpclient.Do() returns a status-code error for non-2xx responses
	// after exhausting retries; doMatrixRequest wraps it.
	assert.Contains(t, err.Error(), "Matrix API request failed")
}

func TestClient_ConnectionError(t *testing.T) {
	// Non-routable port.
	c := NewClientWithConfig(&Config{HomeserverURL: "http://127.0.0.1:1", ASToken: "as"})
	_, err := c.GetUserID("@alice:example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Matrix API request failed")
}
