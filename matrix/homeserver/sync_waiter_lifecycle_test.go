package homeserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func TestSyncLongPollWaitersReleased(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	hs.SyncNotifier = NewSyncNotifier()
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixToDeviceMessage{}))
	t.Cleanup(resetGlobalHS(hs))
	r := gin.New()
	r.GET("/sync", func(c *gin.Context) {
		c.Set("user_id", "@idle:test.local")
		c.Set("device_id", "IDLE")
		handleSync(c)
	})
	for _, mode := range []string{"timeout", "cancel", "immediate"} {
		t.Run(mode, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				path := "/sync?since=s9999999999999&timeout=1&set_presence=offline"
				if mode == "immediate" {
					path = "/sync?timeout=1&set_presence=offline"
				}
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if mode == "cancel" {
					ctx, cancel := context.WithCancel(req.Context())
					cancel()
					req = req.WithContext(ctx)
				}
				r.ServeHTTP(httptest.NewRecorder(), req)
			}
			hs.SyncNotifier.mu.RLock()
			retained := len(hs.SyncNotifier.waiters["@idle:test.local:IDLE"])
			hs.SyncNotifier.mu.RUnlock()
			t.Logf("retained waiters after 20 completed %s requests: %d", mode, retained)
			require.Zero(t, retained)
		})
	}
}

func TestSyncNotifierSubscriptionCleanupIsolation(t *testing.T) {
	n := NewSyncNotifier()
	canceled, releaseCanceled := n.Subscribe("alice", "device")
	live, releaseLive := n.Subscribe("alice", "device")
	releaseCanceled()
	releaseCanceled()
	require.Len(t, n.waiters["alice:device"], 1)
	n.Notify("alice", "device")
	select {
	case <-live:
	default:
		t.Fatal("remaining request was not notified")
	}
	select {
	case <-canceled:
		t.Fatal("cleanup must not signal new sync data")
	default:
	}
	_, releaseNew := n.Subscribe("alice", "device")
	releaseLive()
	require.Len(t, n.waiters["alice:device"], 1, "old cleanup removed a newer request")
	releaseNew()
	require.Empty(t, n.waiters)
}

func TestSyncNotifierConcurrentNotifyAndRelease(t *testing.T) {
	n := NewSyncNotifier()
	for i := 0; i < 100; i++ {
		_, release := n.Subscribe("alice", "device")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); release() }()
		go func() { defer wg.Done(); n.Notify("alice", "device") }()
		wg.Wait()
		release()
		require.Empty(t, n.waiters)
	}
}
