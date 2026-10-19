package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Waiting for a slow recommendation index task backs off instead of polling
// GET /tasks/{uid} every 40ms.
func TestMeiliTaskPollingBacksOff(t *testing.T) {
	var polls atomic.Int32
	done := time.Now().Add(1200 * time.Millisecond)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			polls.Add(1)
			status := "processing"
			if time.Now().After(done) {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"taskUid": 9})
	}))
	defer server.Close()
	p, _ := meiliTestProvider(t, server.URL)
	require.NoError(t, p.task(context.Background(), http.MethodPost, "/indexes", map[string]string{"uid": "slow"}))
	// 0, 50, 150, 350, 750, 1550ms: about 6 polls instead of ~30.
	require.LessOrEqual(t, polls.Load(), int32(8))
	require.Equal(t, time.Second, nextMeiliPoll(800*time.Millisecond))
}
