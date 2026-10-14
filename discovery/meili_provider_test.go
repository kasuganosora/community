package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/search"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func meiliTestProvider(t *testing.T, host string) (*MeiliProvider, *systemsettings.DiscoveryConfiguration) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	suffix, err := feedGeneration()
	require.NoError(t, err)
	settings := &systemsettings.DiscoveryConfiguration{Enabled: true, Mode: "auto", RequireManualApproval: true}
	p := &MeiliProvider{connection: search.DiscoveryConnection{Enabled: true, Host: host, Prefix: "qa_" + suffix + "_"}, client: &http.Client{Timeout: 3 * time.Second}, conn: func() *gorm.DB { return nil }, redis: func() redis.UniversalClient { return client }, settings: func(context.Context, *gorm.DB) (systemsettings.DiscoveryConfiguration, error) { return *settings, nil }, reason: "not_built"}
	return p, settings
}
func meiliTestManifest(t *testing.T, p *MeiliProvider, settings *systemsettings.DiscoveryConfiguration) meiliManifest {
	t.Helper()
	active := meiliManifest{Index: p.connection.Prefix + "discovery_statuses_v1_test", Generation: "generation", Version: p.version("test-server"), ServerVersion: "test-server", Mode: "meili_lexical", Cutoff: time.Now().UTC().Add(-time.Second)}
	settings.ApprovedVersion = active.Version
	ctx, token, release, err := p.lease(context.Background(), time.Second)
	require.NoError(t, err)
	defer release()
	require.NoError(t, p.writeState(ctx, token, meiliState{Active: &active}))
	return active
}
func TestMeiliQualityGateAndStaleIndexDoNotQuery(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	active := meiliTestManifest(t, p, settings)
	settings.ApprovedVersion = "previous-release"
	_, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "摄影", Query: "摄影"}})
	require.Error(t, err)
	require.Equal(t, int32(0), calls.Load())
	require.Equal(t, "awaiting_manual_approval", p.Status().Reason)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Contains(t, err.Error(), "awaiting_manual_approval", "the warning log names the concrete reason")
	settings.ApprovedVersion = active.Version
	active.Cutoff = time.Now().Add(-PoolMaxAge - time.Second)
	ctx, token, release, err := p.lease(context.Background(), time.Second)
	require.NoError(t, err)
	require.NoError(t, p.writeState(ctx, token, meiliState{Active: &active}))
	release()
	_, err = p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "摄影", Query: "摄影"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "index_expired")
	require.Equal(t, int32(0), calls.Load())
}
func TestMeiliLexicalWireAndSharedQueryConcurrency(t *testing.T) {
	var active, maximum atomic.Int32
	var mu sync.Mutex
	requests := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": []map[string]string{{"id": "2"}, {"id": "2"}, {"id": "1"}}})
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	meiliTestManifest(t, p, settings)
	buckets := []ContentBucket{{Key: "摄影", Query: "摄影"}, {Key: "coding", Query: "coding"}, {Key: "摄影", Query: "摄影"}}
	batch, err := p.BuildBuckets(context.Background(), time.Now(), buckets)
	require.NoError(t, err)
	require.Len(t, batch.Lists, 4)
	require.LessOrEqual(t, maximum.Load(), int32(2))
	require.Equal(t, "meili_lexical", batch.Mode)
	for _, list := range batch.Lists {
		require.Equal(t, []uint64{2, 1}, list.IDs)
	}
	for _, request := range requests {
		require.NotContains(t, request, "hybrid")
		require.EqualValues(t, 80, request["limit"])
	}
	// A second replica with the same namespace cannot get an independent set
	// of two query slots while the deployment lease is owned.
	ctx, _, release, err := p.lease(context.Background(), time.Second)
	require.NoError(t, err)
	defer release()
	_, err = p.BuildBuckets(ctx, time.Now(), buckets)
	require.Error(t, err)
}
func TestMeiliAsyncTaskFailureAndRevokedLease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "failed"})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]int{"taskUid": 7})
		}
	}))
	defer server.Close()
	p, _ := meiliTestProvider(t, server.URL)
	require.Error(t, p.task(context.Background(), http.MethodPost, "/indexes", map[string]string{"uid": "broken"}))
	ctx, token, release, err := p.lease(context.Background(), time.Second)
	require.NoError(t, err)
	defer release()
	lease, _ := p.keys()
	require.NoError(t, p.redis().Set(ctx, lease, "new-owner", time.Minute).Err())
	require.Error(t, p.writeState(ctx, token, meiliState{Pending: "obsolete"}))
	state, err := p.readState(ctx)
	require.NoError(t, err)
	require.Empty(t, state.Pending)
}
func TestMeiliQueryFailureNeverPublishesPartialBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["q"] != "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": []map[string]string{{"id": "1"}}})
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	meiliTestManifest(t, p, settings)
	batch, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "hello", Query: "hello"}})
	require.Error(t, err)
	require.Equal(t, "rules", batch.Mode)
	require.Empty(t, batch.Lists)
}
func meiliSQLFixture(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)}
	var conn *gorm.DB
	var err error
	if dsn := os.Getenv("DISCOVERY_TEST_MYSQL_DSN"); dsn != "" {
		conn, err = gorm.Open(mysql.Open(dsn), cfg)
	} else if pgtest.Enabled() {
		conn = pgtest.Open(t, cfg)
	} else {
		conn, err = gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), cfg)
	}
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.StatusTag{}, &models.DomainBlock{}, &models.Block{}, &models.MuteAccount{}, &models.MuteStatus{}, &models.Filter{}, &models.Follow{}, &models.RecommendationFeedback{}))
	for _, model := range []any{&models.User{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.StatusTag{}, &models.DomainBlock{}, &models.Block{}, &models.MuteAccount{}, &models.MuteStatus{}, &models.Filter{}, &models.Follow{}, &models.RecommendationFeedback{}} {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true, AllowGlobalUpdate: true}).Unscoped().Delete(model).Error)
	}
	require.NoError(t, models.EnsureRecommendationFeedbackSchema(conn))
	tx := conn.Session(&gorm.Session{SkipHooks: true})
	user := &models.User{Id: 1, UUID: "meili-fixture", UserName: "meili-fixture", EmailHash: "meili-fixture", Status: models.UserStatusNormal}
	require.NoError(t, tx.Create(user).Error)
	require.NoError(t, tx.Omit("Extra", "User").Create(&models.Account{Id: 1, UserId: 1, Username: "author", Acct: "author", Discoverable: true}).Error)
	created := time.Now().UTC().Add(-time.Hour)
	for i, visibility := range []uint8{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted, models.StatusVisibilityPrivate, models.StatusVisibilityDirect} {
		id := uint64(i + 1)
		s := &models.Status{Id: id, AccountId: 1, Visibility: visibility, CreatedAt: &created, Language: "zh"}
		require.NoError(t, tx.Omit("Content").Create(s).Error)
		require.NoError(t, tx.Create(&models.StatusContent{StatusId: id, Content: "相机拍摄山川摄影 public"}).Error)
	}
	return conn
}
func TestMeiliExtractionUsesCurrentPublicEligibility(t *testing.T) {
	p, _ := meiliTestProvider(t, "http://unused")
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	docs, err := p.documents(context.Background(), time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "1", docs[0].ID)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("discoverable", false).Error)
	docs, err = p.documents(context.Background(), time.Now().UTC())
	require.NoError(t, err)
	require.Empty(t, docs)
}
func TestMeiliRealEngineBuildQueryAndRetire(t *testing.T) {
	host := strings.TrimRight(os.Getenv("DISCOVERY_TEST_MEILI_URL"), "/")
	if host == "" {
		t.Skip("set DISCOVERY_TEST_MEILI_URL to an isolated real Meilisearch")
	}
	p, settings := meiliTestProvider(t, host)
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		state, _ := p.readState(cleanup)
		for _, index := range []string{state.Pending, state.Retired} {
			if index != "" {
				_ = p.retire(cleanup, index)
			}
		}
		if state.Active != nil {
			_ = p.retire(cleanup, state.Active.Index)
		}
	})
	require.NoError(t, p.rebuild(ctx))
	state, err := p.readState(ctx)
	require.NoError(t, err)
	require.NotNil(t, state.Active)
	require.Equal(t, 1, state.Active.Documents)
	require.Equal(t, "awaiting_manual_approval", p.Status().Reason)
	settings.ApprovedVersion = state.Active.Version
	require.True(t, p.Status().Ready)
	batch, err := p.BuildBuckets(ctx, time.Now().UTC(), []ContentBucket{{Key: "摄影", Query: "摄影"}})
	require.NoError(t, err)
	require.Len(t, batch.Lists, 2)
	require.Equal(t, []uint64{1}, batch.Lists[1].IDs)
	// Retirement clears the previous generation only after all async tasks
	// have completed, and an empty eligible set may still become ready.
	first := state.Active.Index
	state.Active.Cutoff = time.Now().Add(-3 * time.Minute)
	owned, token, release, err := p.lease(ctx, time.Second)
	require.NoError(t, err)
	require.NoError(t, p.writeState(owned, token, state))
	release()
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("discoverable", false).Error)
	require.NoError(t, p.rebuild(ctx))
	state, err = p.readState(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, state.Active.Documents)
	require.NotEqual(t, first, state.Active.Index)
	require.Empty(t, state.Retired)
	var metadata map[string]any
	require.True(t, meiliNotFound(p.http(ctx, http.MethodGet, "/indexes/"+first, nil, &metadata)))
}

func TestMeiliHybridWireRequiresExplicitEmbedding(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer server.Close()
	p, _ := meiliTestProvider(t, server.URL)
	p.embedding = &meiliEmbedding{Name: "public-text", ModelVersion: "model-v1", MaxUnitsPerHour: 10}
	active := meiliManifest{Index: "example", Mode: "meili_hybrid"}
	lists, err := p.searchBucket(context.Background(), active, time.Now(), ContentBucket{Key: "topic", Query: "topic"})
	require.NoError(t, err)
	require.Len(t, lists, 3)
	require.NotContains(t, requests[0], "hybrid")
	require.NotContains(t, requests[1], "hybrid")
	require.Equal(t, map[string]any{"embedder": "public-text", "semanticRatio": 0.5}, requests[2]["hybrid"])
	p.embedding = nil
	_, err = p.searchBucket(context.Background(), active, time.Now(), ContentBucket{Key: "topic", Query: "topic"})
	require.Error(t, err)
}

func TestMeiliRawContentAndTagBudgetsBeforeNormalization(t *testing.T) {
	p, _ := meiliTestProvider(t, "http://unused")
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	require.NoError(t, conn.Model(&models.StatusContent{}).Where("status_id = ?", 1).UpdateColumns(map[string]any{"content": strings.Repeat("<b></b>", 5000), "spoiler_text": strings.Repeat("<b></b>", 5000)}).Error)
	// An oversized root is skipped rather than failing the whole generation.
	docs, err := p.documents(context.Background(), time.Now())
	require.NoError(t, err)
	require.Empty(t, docs)
	require.NoError(t, conn.Model(&models.StatusContent{}).Where("status_id = ?", 1).UpdateColumns(map[string]any{"content": "small", "spoiler_text": ""}).Error)
	tx := conn.Session(&gorm.Session{SkipHooks: true})
	for i := 1; i <= 17; i++ {
		tag := models.Tag{Id: uint64(i), Name: fmt.Sprintf("tag%d", i), NameKey: fmt.Sprintf("tag%d", i)}
		require.NoError(t, tx.Create(&tag).Error)
		require.NoError(t, tx.Create(&models.StatusTag{StatusId: 1, TagId: tag.Id}).Error)
	}
	docs, err = p.documents(context.Background(), time.Now())
	require.NoError(t, err)
	require.Empty(t, docs)
}
func TestMeiliEmbeddingHourlyReservationCountsFailures(t *testing.T) {
	p, _ := meiliTestProvider(t, "http://unused")
	p.embedding = &meiliEmbedding{MaxUnitsPerHour: 5}
	require.True(t, p.reserveEmbedding(context.Background(), 3))
	// A timeout/failure after reservation is intentionally not refunded.
	require.False(t, p.reserveEmbedding(context.Background(), 3))
	require.True(t, p.reserveEmbedding(context.Background(), 2))
	require.False(t, p.reserveEmbedding(context.Background(), 1))
}
func TestMeiliSearchLostLeaseCannotPublishInWatchdogWindow(t *testing.T) {
	var provider *MeiliProvider
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			key, _ := provider.keys()
			_ = provider.redis().Set(context.Background(), key, "takeover", time.Minute).Err()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	provider = p
	meiliTestManifest(t, p, settings)
	batch, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "topic", Query: "topic"}})
	require.Error(t, err)
	require.Empty(t, batch.Lists)
	require.Equal(t, int32(2), calls.Load())
}
func TestMeiliSourceAgeExpiresWhileQueriesRun(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(120 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	active := meiliTestManifest(t, p, settings)
	active.Cutoff = time.Now().Add(-PoolMaxAge + 200*time.Millisecond)
	ctx, token, release, err := p.lease(context.Background(), time.Second)
	require.NoError(t, err)
	require.NoError(t, p.writeState(ctx, token, meiliState{Active: &active}))
	release()
	batch, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "topic", Query: "topic"}})
	require.Error(t, err)
	require.Empty(t, batch.Lists)
	require.Equal(t, int32(2), calls.Load())
}
func TestMeiliDiskBudgetAdmission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"databaseSize": 500 << 20})
	}))
	defer server.Close()
	p, _ := meiliTestProvider(t, server.URL)
	require.ErrorContains(t, p.checkDiskBudget(context.Background()), "disk budget")
	p.maxDatabaseBytes = 1024 << 20
	require.NoError(t, p.checkDiskBudget(context.Background()))
}

func TestMeiliAlignedIndexAndCandidateRefreshWaitsForWriter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"hits":[{"id":"1"}]}`)) }))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	active := meiliTestManifest(t, p, settings)
	key, _ := p.keys()
	require.NoError(t, p.redis().Set(context.Background(), key, "index-writer", time.Minute).Err())
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(120 * time.Millisecond)
		_ = p.redis().Del(context.Background(), key).Err()
	}()
	batch, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "go", Query: "go"}})
	<-done
	require.NoError(t, err)
	require.Equal(t, active.Generation, batch.Generation)
	require.Equal(t, "meili_lexical", batch.Mode)
}
func TestMeiliExtractionCapsNewestRootsInsteadOfFailingBudget(t *testing.T) {
	p, _ := meiliTestProvider(t, "http://unused")
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	tx := conn.Session(&gorm.Session{SkipHooks: true, CreateBatchSize: 500})
	base := time.Now().UTC().Add(-2 * time.Hour)
	total := meiliRowLimit + 300
	statuses := make([]*models.Status, 0, total)
	contents := make([]*models.StatusContent, 0, total)
	for i := 0; i < total; i++ {
		id := uint64(100 + i)
		created := base.Add(time.Duration(i) * time.Second)
		statuses = append(statuses, &models.Status{Id: id, AccountId: 1, Visibility: models.StatusVisibilityPublic, CreatedAt: &created, Language: "en"})
		body := fmt.Sprintf("post %d", i)
		if i == total-1 {
			// The newest root is far beyond the per-post raw budget.
			body = strings.Repeat("x", 70000)
		}
		contents = append(contents, &models.StatusContent{StatusId: id, Content: body})
	}
	require.NoError(t, tx.Omit("Content").Create(&statuses).Error)
	require.NoError(t, tx.Create(&contents).Error)
	started := time.Now()
	docs, err := p.documents(context.Background(), time.Now().UTC())
	t.Logf("extracted %d documents in %s", len(docs), time.Since(started))
	require.NoError(t, err)
	require.Len(t, docs, meiliRowLimit-1)
	ids := map[string]bool{}
	for _, doc := range docs {
		ids[doc.ID] = true
	}
	require.False(t, ids[fmt.Sprint(100+total-1)], "oversized post is skipped")
	require.True(t, ids[fmt.Sprint(100+total-2)], "newest regular post is indexed")
	require.False(t, ids["100"], "oldest posts beyond the row cap age out")
	require.Equal(t, "resource_budget_exceeded", meiliSafeBudgetReason(fmt.Errorf("x budget exceeded")))
}

func TestMeiliAutomaticApprovalUsesHealthyIndexWithoutManualVersion(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	settings.RequireManualApproval = false
	active := meiliManifest{Index: p.connection.Prefix + "discovery_statuses_v1_test", Generation: "generation", Version: p.version("test-server"), ServerVersion: "test-server", Mode: "meili_lexical", Documents: 3, Cutoff: time.Now().UTC().Add(-time.Second)}
	write := func() {
		ctx, token, release, err := p.lease(context.Background(), time.Second)
		require.NoError(t, err)
		defer release()
		require.NoError(t, p.writeState(ctx, token, meiliState{Active: &active}))
	}
	write()
	// A stale explicit approval does not block the automatic default.
	settings.ApprovedVersion = "stale-version"
	require.True(t, p.Status().Ready)
	_, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "go", Query: "go"}})
	require.NoError(t, err)
	require.Positive(t, calls.Load())
	// Empty indexes are not promoted automatically.
	active.Documents = 0
	write()
	require.Equal(t, "index_empty", p.Status().Reason)
	_, err = p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "go", Query: "go"}})
	require.ErrorIs(t, err, ErrUnavailable)
	require.Contains(t, err.Error(), "index_empty")
	// Turning manual review on restores the explicit gate.
	active.Documents = 3
	write()
	settings.RequireManualApproval = true
	require.Equal(t, "awaiting_manual_approval", p.Status().Reason)
	settings.ApprovedVersion = active.Version
	require.True(t, p.Status().Ready)
}
