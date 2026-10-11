package homeserver

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type joinBudgetTransport func(*http.Request) (*http.Response, error)

func (f joinBudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAliasAndJoinShareOneDeadline(t *testing.T) {
	server := newFakeFed(t)
	hs := joinTestEnv(t, server)
	old := joinSettingsSource
	joinSettingsSource = func() joinSettings { s := old(); s.Total = 150 * time.Millisecond; return s }
	var aliasDeadline, joinDeadline time.Time
	newFederationClient = func(hs *HomeServer) *FederationClient {
		c := NewFederationClient(hs)
		c.fedHTTPClient = &http.Client{Transport: joinBudgetTransport(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "/query/directory") {
				aliasDeadline, _ = r.Context().Deadline()
				select {
				case <-time.After(40 * time.Millisecond):
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"room_id":"!room:remote.example","servers":["remote.example"]}`)), Request: r}, nil
			}
			joinDeadline, _ = r.Context().Deadline()
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		return c
	}
	_, me := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", "#room:remote.example", nil)
	require.NotNil(t, me)
	require.Contains(t, me.Error, joinTimeoutPrefix)
	require.False(t, aliasDeadline.IsZero())
	require.Equal(t, aliasDeadline, joinDeadline, "alias time must count against the same total budget as make_join")
	require.Zero(t, joinAttemptsRunning.Load())
}

func TestCancelledAliasJoinPreservesCancellation(t *testing.T) {
	hs := createTestHomeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, me := hs.Room.JoinRoomContext(ctx, "@alice:test.local", "#room:remote.example", nil)
	require.NotNil(t, me)
	require.Contains(t, me.Error, "cancelled")
}
