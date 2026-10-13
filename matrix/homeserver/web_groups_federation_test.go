package homeserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func TestWebGroupNativeOutboxRetainsDepartingServerAndDeduplicates(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}))
	roomID := createRoomWithUser(t, hs, "@alice:test.local")
	bob := "@bob:remote.example"
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: bob, Membership: MembershipLeave}).Error)
	event := MatrixEvent{EventID: "$departure", RoomID: roomID, EventType: EventTypeRoomMember, Sender: "@alice:test.local", StateKey: &bob, Content: map[string]any{"membership": "leave"}}
	require.NoError(t, hs.queueNativeFederation(conn, &event))
	require.NoError(t, hs.queueNativeFederation(conn, &event))
	var rows []BridgeFederationDelivery
	require.NoError(t, conn.Where("event_id = ?", event.EventID).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "remote.example", rows[0].ServerName)
	require.True(t, rows[0].Native)
}

func TestWebGroupNativeOutboxRetriesOfflinePeerBeyondTenAttempts(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}))
	roomID := createRoomWithUser(t, hs, "@alice:test.local")
	sent, me := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "offline test"}, nil)
	require.Nil(t, me)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	old := newBridgeFederationClient
	t.Cleanup(func() { newBridgeFederationClient = old })
	newBridgeFederationClient = func(hs *HomeServer) *FederationClient {
		c := NewFederationClient(hs)
		c.fedHTTPClient = server.Client()
		return c
	}
	row := BridgeFederationDelivery{ID: "native-offline", Native: true, EventID: sent.EventID, ServerName: strings.TrimPrefix(server.URL, "https://"), State: "pending", Attempts: 10, NextAttemptAt: time.Now().Add(-time.Minute), CreatedAt: time.Now().Add(-time.Hour)}
	require.NoError(t, conn.Create(&row).Error)
	require.Error(t, hs.ProcessBridgeFederation(context.Background(), row.ID))
	require.NoError(t, conn.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "pending", row.State)
	require.Equal(t, 11, row.Attempts)
	require.True(t, row.NextAttemptAt.After(time.Now().Add(55*time.Minute)))
	require.NoError(t, conn.Model(&row).Updates(map[string]any{"created_at": time.Now().Add(-8 * 24 * time.Hour), "next_attempt_at": time.Now().Add(-time.Minute)}).Error)
	require.Error(t, hs.ProcessBridgeFederation(context.Background(), row.ID))
	require.NoError(t, conn.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "failed", row.State)
}

func TestWebGroupNativeRetryDelayIsBounded(t *testing.T) {
	require.Equal(t, time.Minute, nativeFederationRetryDelay(-1))
	require.Equal(t, 2*time.Minute, nativeFederationRetryDelay(1))
	require.Equal(t, time.Hour, nativeFederationRetryDelay(10000))
}

func TestWebGroupOfflinePeerDoesNotBlockHealthyPeer(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}))
	roomID := createRoomWithUser(t, hs, "@alice:test.local")
	event, me := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "parallel peers"}, nil)
	require.Nil(t, me)
	slowStarted, healthyReached := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(slowStarted)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	defer close(release)
	healthy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(healthyReached)
		_ = json.NewEncoder(w).Encode(map[string]any{"pdus": map[string]any{event.EventID: map[string]any{}}})
	}))
	defer healthy.Close()
	old := newBridgeFederationClient
	t.Cleanup(func() { newBridgeFederationClient = old })
	newBridgeFederationClient = func(hs *HomeServer) *FederationClient {
		c := NewFederationClient(hs)
		c.fedHTTPClient = slow.Client()
		return c
	}
	for i, server := range []*httptest.Server{slow, healthy} {
		row := BridgeFederationDelivery{ID: []string{"slow", "healthy"}[i], Native: true, EventID: event.EventID, ServerName: strings.TrimPrefix(server.URL, "https://"), State: "pending", NextAttemptAt: time.Now().Add(-time.Minute), CreatedAt: time.Now().Add(time.Duration(i-2) * time.Minute)}
		require.NoError(t, conn.Create(&row).Error)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { hs.processBridgeFederationJobs(ctx); close(done) }()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("offline peer did not start")
	}
	select {
	case <-healthyReached:
	case <-time.After(time.Second):
		t.Fatal("healthy peer stalled behind offline peer")
	}
	cancel()
	<-done
}
