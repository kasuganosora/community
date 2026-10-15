package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func instanceMetaDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, _ := conn.DB()
	raw.SetMaxOpenConns(1)
	old := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(old); raw.Close() })
	require.NoError(t, conn.AutoMigrate(&models.InstanceMetadata{}, &models.Account{}))
	return conn
}

func TestInstanceBackoff(t *testing.T) {
	require.Equal(t, 10*time.Minute, instanceBackoff(1))
	require.Equal(t, 20*time.Minute, instanceBackoff(2))
	require.Equal(t, 80*time.Minute, instanceBackoff(4))
	require.Equal(t, instanceMetadataBackoffMax, instanceBackoff(40))
}

func TestInstanceMetadataSharedCacheAndBackoff(t *testing.T) {
	conn := instanceMetaDB(t)
	var calls int32
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if fail.Load() {
			http.Error(w, "down", 503)
			return
		}
		switch r.URL.Path {
		case "/.well-known/nodeinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{"links": []any{map[string]any{"rel": "http://nodeinfo.diaspora.software/ns/schema/2.1", "href": "http://" + r.Host + "/nodeinfo/2.1"}}})
		case "/nodeinfo/2.1":
			_ = json.NewEncoder(w).Encode(map[string]any{"software": map[string]any{"name": "Misskey", "version": "2025.4.0"}, "metadata": map[string]any{"nodeName": "Kitchen"}})
		case "/api/meta":
			_ = json.NewEncoder(w).Encode(map[string]any{"iconUrl": "https://kitchen.example/icon.png", "name": "Kitchen"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldClient, oldBase := remoteReactionClient, remoteReactionBase
	remoteReactionClient = ssrfHTTPClientForTesting()
	remoteReactionBase = func(string) string { return srv.URL }
	resetRemoteReactionCaches()
	defer func() { remoteReactionClient, remoteReactionBase = oldClient, oldBase; resetRemoteReactionCaches() }()

	dom := "kitchen.example"
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: 5, Username: "a", Domain: &dom, OriginId: "https://kitchen.example/u/a"}).Error)

	// Worker fills the cache (with icon) for the missing domain.
	require.Equal(t, 1, RefreshInstanceMetadataBatch(context.Background()))
	var row models.InstanceMetadata
	require.NoError(t, conn.Take(&row, "domain = ?", dom).Error)
	require.Equal(t, "misskey", row.Software)
	require.Equal(t, "2025.4.0", row.Version)
	require.Equal(t, "Kitchen", row.Name)
	require.Equal(t, "https://kitchen.example/icon.png", row.IconURL)
	require.NotNil(t, row.FetchedAt)

	// Remote reaction detection reads the same cache: no new requests.
	before := atomic.LoadInt32(&calls)
	require.Equal(t, softwareMisskey, remoteSoftware(context.Background(), dom))
	require.Equal(t, before, atomic.LoadInt32(&calls))
	require.Zero(t, RefreshInstanceMetadataBatch(context.Background()), "fresh rows are not due")

	// Expired + failing: failure recorded with backoff, good data kept.
	past := time.Now().Add(-time.Minute)
	require.NoError(t, conn.Model(&models.InstanceMetadata{}).Where("domain = ?", dom).Update("next_attempt_at", past).Error)
	fail.Store(true)
	require.Equal(t, 1, RefreshInstanceMetadataBatch(context.Background()))
	require.NoError(t, conn.Take(&row, "domain = ?", dom).Error)
	require.Equal(t, 1, row.FailCount)
	require.NotNil(t, row.FailedAt)
	require.Equal(t, "misskey", row.Software)
	require.True(t, row.NextAttemptAt.After(time.Now().Add(9*time.Minute)))
	require.Zero(t, RefreshInstanceMetadataBatch(context.Background()), "backed-off rows are skipped")
}
