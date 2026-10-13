package homeserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func TestNativeOutboxRetriesAfterCachedTransactionPDUFailure(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}))
	room := createRoomWithUser(t, hs, "@alice:test.local")
	event, merr := hs.Event.SendRoomEvent(room, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "durable retry"}, nil)
	require.Nil(t, merr)
	var mutex sync.Mutex
	ready := false
	cached := map[string]bool{}
	paths := []string{}
	pdus := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PDUs []json.RawMessage `json:"pdus"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.PDUs) != 1 {
			http.Error(w, "invalid PDU", 400)
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		paths = append(paths, r.URL.Path)
		pdus = append(pdus, string(body.PDUs[0]))
		accepted, seen := cached[r.URL.Path]
		if !seen {
			accepted = ready
			cached[r.URL.Path] = accepted
		}
		response := map[string]any{}
		if !accepted {
			response["error"] = "temporary missing auth event"
		}
		// Matrix receivers cache the entire transaction response, including
		// per-PDU errors inside an HTTP 200, under the incoming transaction ID.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"pdus": map[string]any{event.EventID: response}})
	}))
	defer server.Close()
	old := newBridgeFederationClient
	t.Cleanup(func() { newBridgeFederationClient = old })
	newBridgeFederationClient = func(hs *HomeServer) *FederationClient {
		c := NewFederationClient(hs)
		c.fedHTTPClient = server.Client()
		return c
	}
	row := BridgeFederationDelivery{ID: "native-cached-failure", Native: true, EventID: event.EventID, ServerName: strings.TrimPrefix(server.URL, "https://"), State: "pending", NextAttemptAt: time.Now().Add(-time.Minute)}
	require.NoError(t, conn.Create(&row).Error)
	require.Error(t, hs.ProcessBridgeFederation(context.Background(), row.ID))
	mutex.Lock()
	ready = true
	mutex.Unlock()
	require.NoError(t, conn.Model(&row).Update("next_attempt_at", time.Now().Add(-time.Second)).Error)
	require.NoError(t, hs.ProcessBridgeFederation(context.Background(), row.ID), "a cached transaction failure must not poison every later retry")
	require.NoError(t, conn.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "delivered", row.State)
	require.NoError(t, hs.ProcessBridgeFederation(context.Background(), row.ID))
	mutex.Lock()
	defer mutex.Unlock()
	require.Len(t, paths, 2, "delivered jobs are not sent again")
	require.NotEqual(t, paths[0], paths[1], "new attempt needs a fresh transaction ID")
	require.Equal(t, pdus[0], pdus[1], "signed PDU and event ID remain unchanged across attempts")
}

func TestNativeOutboxOfflineBacklogCannotHideHealthyPeer(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}))
	room := createRoomWithUser(t, hs, "@alice:test.local")
	slowStarted, healthyReached := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := make(chan struct{})
	var releaseOnce sync.Once
	releasePeer := func() { releaseOnce.Do(func() { close(release) }) }
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(slowStarted) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	defer releasePeer()
	var healthyEvent string
	healthy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(healthyReached)
		_ = json.NewEncoder(w).Encode(map[string]any{"pdus": map[string]any{healthyEvent: map[string]any{}}})
	}))
	defer healthy.Close()
	old := newBridgeFederationClient
	t.Cleanup(func() { newBridgeFederationClient = old })
	newBridgeFederationClient = func(hs *HomeServer) *FederationClient {
		c := NewFederationClient(hs)
		c.fedHTTPClient = slow.Client()
		return c
	}
	for i := 0; i < 101; i++ {
		event, merr := hs.Event.SendRoomEvent(room, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": fmt.Sprintf("backlog %d", i)}, nil)
		require.Nil(t, merr)
		peer := strings.TrimPrefix(slow.URL, "https://")
		if i == 100 {
			peer = strings.TrimPrefix(healthy.URL, "https://")
			healthyEvent = event.EventID
		}
		row := BridgeFederationDelivery{ID: fmt.Sprintf("backlog-%03d", i), Native: true, EventID: event.EventID, ServerName: peer, State: "pending", NextAttemptAt: time.Now().Add(-time.Minute), CreatedAt: time.Now().Add(time.Duration(i-200) * time.Second)}
		require.NoError(t, conn.Create(&row).Error)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { hs.processBridgeFederationJobs(ctx); close(done) }()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("offline destination not started")
	}
	select {
	case <-healthyReached:
	case <-time.After(time.Second):
		t.Error("100 old offline events hid the healthy destination")
	}
	releasePeer()
	<-done
	var delivered BridgeFederationDelivery
	require.NoError(t, conn.First(&delivered, "id = ?", "backlog-100").Error)
	require.Equal(t, "delivered", delivered.State)
	var untouched int64
	require.NoError(t, conn.Model(&BridgeFederationDelivery{}).Where("server_name = ? AND state = ? AND attempts = ?", strings.TrimPrefix(slow.URL, "https://"), "pending", 0).Count(&untouched).Error)
	require.EqualValues(t, 99, untouched, "only one offline request is claimed; the rest remain pending")
}

func TestNativeOutboxRotatesBeyondSixteenBackloggedPeers(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}))
	roomID := createRoomWithUser(t, hs, "@alice:test.local")
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", roomID).Error)
	events := make([]*SendMessageResponse, 0, 100)
	for i := 0; i < 100; i++ {
		event, merr := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": fmt.Sprintf("rotation %d", i)}, nil)
		require.Nil(t, merr)
		events = append(events, event)
	}
	jobs := make([]BridgeFederationDelivery, 0, 1800)
	now := time.Now()
	for peer := 0; peer < 18; peer++ {
		for i, event := range events {
			jobs = append(jobs, BridgeFederationDelivery{ID: fmt.Sprintf("rotate-%02d-%03d", peer, i), Native: true, EventID: event.EventID, ServerName: fmt.Sprintf("peer%02d.example", peer), State: "pending", NextAttemptAt: now.Add(time.Duration(peer-30) * time.Minute), CreatedAt: now.Add(time.Duration(i-200) * time.Second)})
		}
	}
	require.NoError(t, conn.CreateInBatches(jobs, 100).Error)
	old := newBridgeFederationClient
	t.Cleanup(func() { newBridgeFederationClient = old })
	newBridgeFederationClient = func(hs *HomeServer) *FederationClient {
		c := NewFederationClient(hs)
		c.fedHTTPClient = &http.Client{Transport: joinBudgetTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() < "peer16.example" {
				return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			}
			var body struct {
				PDUs []json.RawMessage `json:"pdus"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			if len(body.PDUs) != 1 {
				return nil, fmt.Errorf("expected one PDU")
			}
			pdu, err := parseAndValidatePDU(room.RoomVersion, body.PDUs[0])
			if err != nil {
				return nil, err
			}
			response, _ := json.Marshal(map[string]any{"pdus": map[string]any{pdu.EventID(): map[string]any{}}})
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(response))), Request: r}, nil
		})}
		return c
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		hs.processBridgeFederationJobs(ctx)
		cancel()
	}
	for _, peer := range []string{"peer16.example", "peer17.example"} {
		var delivered int64
		require.NoError(t, conn.Model(&BridgeFederationDelivery{}).Where("server_name = ? AND state = ?", peer, "delivered").Count(&delivered).Error)
		require.Positive(t, delivered, "a healthy destination beyond the first peer page must run on the next round")
	}
	var oldPending int64
	require.NoError(t, conn.Model(&BridgeFederationDelivery{}).Where("server_name < ? AND state = ? AND attempts = 0", "peer16.example", "pending").Count(&oldPending).Error)
	require.Greater(t, oldPending, int64(1500), "rotation must not drain or bulk-update the offline backlog")
}
