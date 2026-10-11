package homeserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMakeJoinNegotiatesSupportedRoomVersions(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}, r.URL.Query()["ver"])
		require.Contains(t, r.Header.Get("Authorization"), "X-Matrix")
		_, _ = w.Write([]byte(`{"room_version":"6","event":{"type":"m.room.member"}}`))
	}))
	defer server.Close()
	client, addr := newTLSFederationClient(server)
	result, err := client.MakeJoin(addr, "!room:remote.example", "@alice:origin.local")
	require.NoError(t, err)
	require.Equal(t, "6", result["room_version"])
}
