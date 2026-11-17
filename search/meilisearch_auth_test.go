package search

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/meilisearch/meilisearch-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func captureSearchLogs(t *testing.T) *bytes.Buffer {
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

func withMeiliKeyEnv(t *testing.T, canonical, fallback string) {
	t.Helper()
	origCanonical, hadCanonical := config.C.Other["MEILISEARCH_API_KEY"]
	origFallback, hadFallback := config.C.Other["MEILI_MASTER_KEY"]
	t.Cleanup(func() {
		if hadCanonical {
			config.C.Other["MEILISEARCH_API_KEY"] = origCanonical
		} else {
			delete(config.C.Other, "MEILISEARCH_API_KEY")
		}
		if hadFallback {
			config.C.Other["MEILI_MASTER_KEY"] = origFallback
		} else {
			delete(config.C.Other, "MEILI_MASTER_KEY")
		}
		resetMeiliFallbackNoticeForTest()
	})
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
	resetMeiliFallbackNoticeForTest()
}

func TestMeilisearchAPIKeyCanonicalAndCompatibilityFallback(t *testing.T) {
	const canonicalSecret = "synthkey_7f9a2b1c_canonical_9001"
	const fallbackSecret = "synthkey_8d4e5f6a_compat_9002"

	t.Run("canonical takes precedence over fallback", func(t *testing.T) {
		buf := captureSearchLogs(t)
		withMeiliKeyEnv(t, canonicalSecret, fallbackSecret)
		require.Equal(t, canonicalSecret, getMeilisearchAPIKey())
		conn := DiscoveryConnectionConfig()
		require.Equal(t, canonicalSecret, conn.APIKey)
		require.NotContains(t, buf.String(), canonicalSecret)
		require.NotContains(t, buf.String(), fallbackSecret)
	})

	t.Run("compatibility fallback accepted when canonical unset", func(t *testing.T) {
		buf := captureSearchLogs(t)
		withMeiliKeyEnv(t, "   ", fallbackSecret)
		require.Equal(t, fallbackSecret, getMeilisearchAPIKey())
		// Calling again does not spam the compatibility notice or leak the secret.
		require.Equal(t, fallbackSecret, getMeilisearchAPIKey())
		out := buf.String()
		require.Contains(t, out, "MEILISEARCH_API_KEY")
		require.Contains(t, out, "MEILI_MASTER_KEY")
		require.Equal(t, 1, strings.Count(out, "MEILI_MASTER_KEY"))
		require.NotContains(t, out, fallbackSecret)
		require.NotContains(t, out, fallbackSecret[:8])
	})
}

func TestMeilisearchClientMissingKeyAuthRequiredRateLimitedDiagnostic(t *testing.T) {
	const serverSecret = "synthkey_4e1c9d0b_expected_7788"
	const wrongSecret = "synthkey_3c2b1a0f_wrong_1122"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+serverSecret {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"The Authorization header is missing.","code":"missing_authorization_header","type":"auth"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pkgVersion":"1.12.8"}`))
	}))
	defer srv.Close()

	buf := captureSearchLogs(t)
	m := &MeilisearchClient{
		host: srv.URL,
		client: meilisearch.New(srv.URL,
			meilisearch.WithAPIKey(""),
			meilisearch.WithCustomClient(&http.Client{Timeout: 2 * time.Second}),
		),
	}

	start := time.Now()
	m.tryConnectAt(start)
	require.False(t, m.IsEnabled(), "must degrade to database search when auth fails")
	for i := 1; i <= 4; i++ {
		m.tryConnectAt(start.Add(time.Duration(i) * time.Minute))
		require.False(t, m.IsEnabled())
	}

	firstWindow := buf.String()
	require.Contains(t, firstWindow, "MEILISEARCH_API_KEY")
	require.Contains(t, firstWindow, "401")
	require.Equal(t, 1, strings.Count(firstWindow, "MEILISEARCH_API_KEY"), "repeated 401s within window must be rate-limited")

	// After 10 minutes, logs again and reports suppressed count.
	m.tryConnectAt(start.Add(11 * time.Minute))
	afterWindow := buf.String()
	require.Equal(t, 2, strings.Count(afterWindow, "MEILISEARCH_API_KEY"))
	require.Contains(t, afterWindow, "4 similar")

	// Wrong non-empty key never leaks into logs.
	buf.Reset()
	mWrong := &MeilisearchClient{
		host:   srv.URL,
		apiKey: wrongSecret,
		client: meilisearch.New(srv.URL,
			meilisearch.WithAPIKey(wrongSecret),
			meilisearch.WithCustomClient(&http.Client{Timeout: 2 * time.Second}),
		),
	}
	mWrong.tryConnectAt(start)
	require.False(t, mWrong.IsEnabled())
	require.Contains(t, buf.String(), "MEILISEARCH_API_KEY")
	require.NotContains(t, buf.String(), wrongSecret)
	require.NotContains(t, buf.String(), wrongSecret[:8])
}
