package homeserver

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRemoteBridgeMXCRequiresAdmittedSenderOrigin(t *testing.T) {
	hs := createTestHomeServer(t)
	wire, _ := json.Marshal(map[string]any{"sender": "@alice:remote.example", "signatures": map[string]any{"remote.example": map[string]string{"ed25519:key": "signed"}}})
	event := MatrixEvent{EventID: "$remote", Sender: "@alice:remote.example", FederationJSON: string(wire), Content: map[string]any{"url": "mxc://remote.example/opaque"}}
	server, id, err := hs.validateBridgeRemoteMXC(&event)
	require.NoError(t, err)
	require.Equal(t, "remote.example", server)
	require.Equal(t, "opaque", id)
	for _, raw := range []string{"https://remote.example/file", "mxc://127.0.0.1/opaque", "mxc://third.example/opaque", "mxc://remote.example/", "mxc://remote.example/a%2fb", "mxc://remote.example/a?secret=1", "mxc://remote.example/a#x", "mxc://user@remote.example/a", "mxc://remote.example/.."} {
		t.Run(raw, func(t *testing.T) {
			copy := event
			copy.Content = map[string]any{"url": raw}
			_, _, err := hs.validateBridgeRemoteMXC(&copy)
			require.Error(t, err)
		})
	}
	copy := event
	copy.FederationJSON = ""
	_, _, err = hs.validateBridgeRemoteMXC(&copy)
	require.Error(t, err)
	copy = event
	copy.Sender = "@different:remote.example"
	_, _, err = hs.validateBridgeRemoteMXC(&copy)
	require.Error(t, err)
	copy = event
	copy.Content = map[string]any{"url": "mxc://remote.example/opaque", "file": map[string]any{}}
	_, _, err = hs.validateBridgeRemoteMXC(&copy)
	require.Error(t, err)
}

type bridgeMediaRoundTrip func(*http.Request) (*http.Response, error)

func (f bridgeMediaRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRemoteBridgeMediaFailureRemainsRetryableAndBounded(t *testing.T) {
	hs := createTestHomeServer(t)
	calls := 0
	hs.mediaFederation().fedMediaHTTPClient = &http.Client{Transport: bridgeMediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Contains(t, r.Header.Get("Authorization"), "X-Matrix")
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 15*time.Second)
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("temporary unavailable"))}, nil
	})}
	e := MatrixEvent{EventID: "$admitted", Sender: "@alice:remote.example", FederationJSON: `{"sender":"@alice:remote.example","signatures":{"remote.example":{"ed25519:key":"signed"}}}`, Content: map[string]any{"url": "mxc://remote.example/opaque"}}
	_, err := hs.importBridgeRemoteMedia(context.Background(), &e)
	require.Error(t, err)
	require.NotErrorIs(t, err, errDirectMessageSkipped)
	require.Equal(t, 1, calls)
	e.Content["url"] = "mxc://another.example/private"
	_, err = hs.importBridgeRemoteMedia(context.Background(), &e)
	require.ErrorIs(t, err, errDirectMessageSkipped)
	require.Equal(t, 1, calls)
}
