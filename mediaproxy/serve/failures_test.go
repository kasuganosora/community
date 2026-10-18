package serve

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memFailures struct {
	mu   sync.Mutex
	keys map[string]bool
}

func (m *memFailures) mark(k string) { m.mu.Lock(); m.keys[k] = true; m.mu.Unlock() }
func (m *memFailures) failed(k string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keys[k]
}

func withSharedFailures(t *testing.T, store failureStore) {
	old := sharedFailures
	sharedFailures = func() failureStore { return store }
	t.Cleanup(func() { sharedFailures = old })
}

// A failure seen by another process (or before a restart) is honoured
// without contacting the origin again.
func TestSharedFailureMemo(t *testing.T) {
	var hits atomic.Int32
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	store := &memFailures{keys: map[string]bool{}}
	withSharedFailures(t, store)
	raw := srv.URL + "/down.mp4"
	require.Equal(t, 502, do(r, "GET", proxyPath("media", raw), nil).Code)
	require.True(t, store.failed(failureKey(raw)), "failure published to the shared memo")

	// Fresh process: empty local memo, shared memo still knows.
	failures.Range(func(k, _ any) bool { failures.Delete(k); return true })
	failN = 0
	require.Equal(t, 502, do(r, "GET", proxyPath("media", raw), nil).Code)
	require.EqualValues(t, 1, hits.Load())
}

// A relay whose origin stops sending is cut off instead of holding the
// request (and a proxy slot) open.
func TestStalledUpstreamIsAborted(t *testing.T) {
	old := stallTimeout
	stallTimeout = 200 * time.Millisecond
	t.Cleanup(func() { stallTimeout = old })
	release := make(chan struct{})
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(make([]byte, 1024))
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(func() { close(release) })
	withSharedFailures(t, nil)
	start := time.Now()
	w := do(r, "GET", proxyPath("media", srv.URL+"/stall.mp4"), nil)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, 200, w.Code)
	require.Equal(t, 1024, w.Body.Len())
}

func TestAvatarFetchHasATotalDeadline(t *testing.T) {
	old := fetchTimeout
	fetchTimeout = 200 * time.Millisecond
	t.Cleanup(func() { fetchTimeout = old })
	release := make(chan struct{})
	srv, _ := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(func() { close(release) })
	start := time.Now()
	_, err := fetchImage(t.Context(), srv.URL+"/slow.png")
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
}
