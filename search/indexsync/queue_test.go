package indexsync

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/search"
)

// A status delete event is queued, not sent from the event handler, and the
// graceful shutdown flush delivers it as one batch delete.
func TestStatusDeleteIsQueuedAndFlushedOnShutdown(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/documents/delete-batch") {
			b, _ := io.ReadAll(r.Body)
			bodies = append(bodies, r.URL.Path+" "+string(b))
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"taskUid":1,"indexUid":"statuses","status":"enqueued","type":"documentDeletion","enqueuedAt":"2026-10-19T00:00:00Z"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	restore := search.UseMeilisearchForTest(srv.URL)
	defer restore()

	onStatus(event.Event{Name: "after_delete", Data: &models.Status{Id: 41}})
	onStatus(event.Event{Name: "after_delete", Data: &models.Status{Id: 42}})
	mu.Lock()
	sent := len(bodies)
	mu.Unlock()
	if sent != 0 {
		t.Fatalf("event handler must not call Meilisearch synchronously, got %v", bodies)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "/indexes/statuses/documents/delete-batch") || !strings.Contains(bodies[0], `"41"`) || !strings.Contains(bodies[0], `"42"`) {
		t.Fatalf("expected one batched delete of 41 and 42, got %v", bodies)
	}
}
