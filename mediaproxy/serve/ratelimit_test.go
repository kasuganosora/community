package serve

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOrigin429HonoursRetryAfterWithoutPerURLMemo(t *testing.T) {
	oldBound := hostWaitBound
	hostWaitBound = time.Second // the 7s pause outlasts it: answer at once
	t.Cleanup(func() { hostWaitBound = oldBound })
	var throttle atomic.Bool
	throttle.Store(true)
	var hits atomic.Int32
	png := pngBytes()
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if throttle.Load() {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	p := proxyPath("emoji", srv.URL+"/emoji/fictional_blob.png")
	w := do(r, "GET", p, nil)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "7", w.Header().Get("Retry-After"))

	// During a pause longer than the wait bound the origin is not contacted.
	other := proxyPath("emoji", srv.URL+"/emoji/another_fictional.png")
	w = do(r, "GET", other, nil)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	require.EqualValues(t, 1, hits.Load())

	// Once the cooldown is over the same URL works at once: no negative cache.
	cooldowns.Range(func(k, _ any) bool { cooldowns.Delete(k); return true })
	throttle.Store(false)
	require.Equal(t, http.StatusOK, do(r, "GET", p, nil).Code)
}

func TestCoolHostCapsAndDefaults(t *testing.T) {
	cooldowns.Range(func(k, _ any) bool { cooldowns.Delete(k); return true })
	require.Equal(t, "5", coolHost("a.example", ""))
	require.Equal(t, "60", coolHost("b.example", "3600"))
	require.Equal(t, "60", coolHost("c.example", now().Add(2*time.Hour).UTC().Format(http.TimeFormat)))
	_, cooling := hostCooling("a.example")
	require.True(t, cooling)
}

func TestPerHostConcurrencyIsBounded(t *testing.T) {
	var active, peak atomic.Int32
	release := make(chan struct{})
	png := pngBytes()
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := active.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		active.Add(-1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(r, "GET", proxyPath("emoji", srv.URL+"/e"+string(rune('a'+i))+".png"), map[string]string{"X-Forwarded-For": "198.51.100." + string(rune('1'+i))})
		}()
	}
	time.Sleep(300 * time.Millisecond)
	close(release)
	wg.Wait()
	require.LessOrEqual(t, int(peak.Load()), hostConcurrent)
	require.Positive(t, int(peak.Load()))
}

func TestShortHostPauseIsWaitedOut(t *testing.T) {
	oldBound := hostWaitBound
	hostWaitBound = 3 * time.Second
	t.Cleanup(func() { hostWaitBound = oldBound })
	png := pngBytes()
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	coolHost("127.0.0.1", "1")
	start := time.Now()
	w := do(r, "GET", proxyPath("emoji", srv.URL+"/emoji/fictional_wait.png"), nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond)
}
