package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFederationDirectoryRejectsErrorsAndInvalidRoomIDs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not found", 404, `{"errcode":"M_NOT_FOUND","error":"No alias"}`},
		{"forbidden", 403, `{"errcode":"M_FORBIDDEN"}`},
		{"server failure", 502, `{"errcode":"M_UNKNOWN"}`},
		{"alias instead of ID", 200, `{"room_id":"#room:remote.example","servers":["remote.example"]}`},
		{"empty room", 200, `{"servers":["remote.example"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, addr := newTLSFederationClient(server)
			response, err := client.QueryDirectory(addr, "#room:remote.example")
			require.Error(t, err)
			require.Nil(t, response)
			if tc.status != 200 {
				var statusErr *federationHTTPError
				require.ErrorAs(t, err, &statusErr)
				require.Equal(t, tc.status, statusErr.Status)
			}
		})
	}
}

func TestFederationDirectoryPreservesParticipatingServers(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "#offtopic-godot:matrix.org", r.URL.Query().Get("room_alias"))
		require.Contains(t, r.Header.Get("Authorization"), "X-Matrix")
		_ = json.NewEncoder(w).Encode(QueryDirectoryResponse{RoomID: "!opaque-room-id", Servers: []string{"one.example", "two.example"}})
	}))
	defer server.Close()
	client, addr := newTLSFederationClient(server)
	response, err := client.QueryDirectory(addr, "#offtopic-godot:matrix.org")
	require.NoError(t, err)
	require.Equal(t, "!opaque-room-id", response.RoomID)
	require.Equal(t, []string{"one.example", "two.example"}, response.Servers)
}

func TestJoinRoomResolvesLocalAliasWithoutMutatingRequest(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	created, err := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat"})
	require.Nil(t, err)
	require.NoError(t, NewAliasService(hs).SetAlias("#room:test.local", created.RoomID, "@alice:test.local"))
	req := &JoinRoomRequest{Servers: []string{"test.local", "test.local"}}
	roomID, joinErr := hs.Room.JoinRoom("@bob:test.local", "#room:test.local", req)
	require.Nil(t, joinErr)
	require.Equal(t, created.RoomID, roomID)
	require.Equal(t, []string{"test.local", "test.local"}, req.Servers)
	_, joinErr = hs.Room.JoinRoom("@bob:test.local", "#missing:test.local", nil)
	require.Equal(t, ErrNotFound, joinErr)
}
