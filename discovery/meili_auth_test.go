package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func captureDiscoveryLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logger := log.GetLogObject()
	prevOut := logger.Out
	prevLevel := logger.GetLevel()
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	logger.SetLevel(logrus.InfoLevel)
	t.Cleanup(func() {
		logger.SetOutput(prevOut)
		logger.SetLevel(prevLevel)
	})
	return &buf
}

func newFakeAuthMeiliServer(t *testing.T, requiredKey string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	indexes := map[string]bool{}
	var taskSeq int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+requiredKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": "The Authorization header is missing or invalid.",
				"code":    "missing_authorization_header",
				"type":    "auth",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/version":
			_ = json.NewEncoder(w).Encode(map[string]any{"pkgVersion": "1.12.8"})
		case r.Method == http.MethodGet && r.URL.Path == "/stats":
			_ = json.NewEncoder(w).Encode(map[string]any{"databaseSize": 1024, "indexes": map[string]any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/indexes":
			results := make([]map[string]any, 0, len(indexes))
			for uid := range indexes {
				results = append(results, map[string]any{"uid": uid})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "total": len(results)})
		case r.Method == http.MethodPost && r.URL.Path == "/indexes":
			var body struct {
				UID string `json:"uid"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			indexes[body.UID] = true
			taskSeq++
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": taskSeq})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "succeeded"})
		case r.Method == http.MethodGet && r.URL.Path == "/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case strings.HasPrefix(r.URL.Path, "/indexes/") && strings.HasSuffix(r.URL.Path, "/settings"):
			taskSeq++
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": taskSeq})
		case strings.HasPrefix(r.URL.Path, "/indexes/") && strings.HasSuffix(r.URL.Path, "/documents"):
			taskSeq++
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": taskSeq})
		case strings.HasPrefix(r.URL.Path, "/indexes/") && strings.HasSuffix(r.URL.Path, "/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": []map[string]string{{"id": "1"}}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/indexes/"):
			uid := strings.TrimPrefix(r.URL.Path, "/indexes/")
			if !indexes[uid] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"uid": uid})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/indexes/"):
			uid := strings.TrimPrefix(r.URL.Path, "/indexes/")
			delete(indexes, uid)
			taskSeq++
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": taskSeq})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMeiliProviderMissingKeyAuthRequiredRateLimitedError(t *testing.T) {
	const requiredSecret = "meili-synth-auth-secret-3344"
	srv := newFakeAuthMeiliServer(t, requiredSecret)
	buf := captureDiscoveryLogs(t)

	p, _ := meiliTestProvider(t, srv.URL)
	p.connection.APIKey = ""
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }

	start := time.Now()
	err := p.rebuild(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
	require.Contains(t, err.Error(), "MEILISEARCH_API_KEY")
	require.False(t, p.Status().Ready, "missing key must never produce a ready index")

	p.logRebuildAt(err, start)
	for i := 1; i <= 3; i++ {
		errRetry := p.rebuild(context.Background())
		require.Error(t, errRetry)
		p.logRebuildAt(errRetry, start.Add(time.Duration(i)*time.Minute))
	}
	require.Equal(t, 3, p.suppressed)
	logs := buf.String()
	require.Equal(t, 1, strings.Count(logs, "Discovery Meili index rebuild failed:"), "401 rebuild warnings must be rate-limited")
	require.Contains(t, logs, "MEILISEARCH_API_KEY")
	require.NotContains(t, logs, requiredSecret)
}

func TestMeiliProviderRebuildSucceedsWithKeyAndFallbackAndNeverLogsSecret(t *testing.T) {
	const secretKey = "synthkey_5b8e2d9a_master_5566"
	srv := newFakeAuthMeiliServer(t, secretKey)

	origHost, hadHost := config.C.Other["MEILISEARCH_HOST"]
	origEnabled, hadEnabled := config.C.Other["MEILISEARCH_ENABLED"]
	origKey, hadKey := config.C.Other["MEILISEARCH_API_KEY"]
	origMaster, hadMaster := config.C.Other["MEILI_MASTER_KEY"]
	t.Cleanup(func() {
		restore := func(k, v string, had bool) {
			if had {
				config.C.Other[k] = v
			} else {
				delete(config.C.Other, k)
			}
		}
		restore("MEILISEARCH_HOST", origHost, hadHost)
		restore("MEILISEARCH_ENABLED", origEnabled, hadEnabled)
		restore("MEILISEARCH_API_KEY", origKey, hadKey)
		restore("MEILI_MASTER_KEY", origMaster, hadMaster)
	})

	runCase := func(t *testing.T, canonical, fallback string) {
		buf := captureDiscoveryLogs(t)
		config.C.Other["MEILISEARCH_ENABLED"] = "auto"
		config.C.Other["MEILISEARCH_HOST"] = srv.URL
		if canonical == "" {
			delete(config.C.Other, "MEILISEARCH_API_KEY")
		} else {
			config.C.Other["MEILISEARCH_API_KEY"] = canonical
		}
		if fallback == "" {
			delete(config.C.Other, "MEILI_MASTER_KEY")
		} else {
			config.C.Other["MEILI_MASTER_KEY"] = fallback
		}

		mr := miniredis.RunT(t)
		rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rc.Close() })
		conn := meiliSQLFixture(t)
		settings := &systemsettings.DiscoveryConfiguration{Enabled: true, Mode: "auto", RequireManualApproval: false}

		p := NewMeiliProvider()
		p.conn = func() *gorm.DB { return conn }
		p.redis = func() redis.UniversalClient { return rc }
		p.settings = func(context.Context, *gorm.DB) (systemsettings.DiscoveryConfiguration, error) { return *settings, nil }

		err := p.rebuild(context.Background())
		require.NoError(t, err)
		p.logRebuildAt(nil, time.Now())
		require.True(t, p.Status().Ready)

		out := buf.String()
		require.NotContains(t, out, secretKey)
		require.NotContains(t, out, secretKey[:8])
	}

	t.Run("canonical MEILISEARCH_API_KEY", func(t *testing.T) {
		runCase(t, secretKey, "")
	})
	t.Run("compatibility fallback MEILI_MASTER_KEY", func(t *testing.T) {
		runCase(t, "", secretKey)
	})
}
