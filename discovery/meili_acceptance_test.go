package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMeiliAcceptanceSemanticRequiresEveryExplicitConfiguration(t *testing.T) {
	old := config.C.Other
	currentMeili.RLock()
	previous := currentMeili.p
	currentMeili.RUnlock()
	t.Cleanup(func() {
		config.C.Other = old
		currentMeili.Lock()
		currentMeili.p = previous
		currentMeili.Unlock()
	})
	valid := map[string]string{
		"RECOMMENDATION_EMBEDDER_NAME":               "public-multilingual",
		"RECOMMENDATION_EMBEDDER_MODEL_VERSION":      "frozen-model-revision",
		"RECOMMENDATION_EMBEDDER_CONFIG":             `{"source":"rest","url":"http://model.invalid/embed"}`,
		"RECOMMENDATION_EMBEDDER_BUDGET_APPROVED":    "true",
		"RECOMMENDATION_EMBEDDER_MAX_DOCUMENTS":      "100",
		"RECOMMENDATION_EMBEDDER_MAX_UNITS_PER_HOUR": "200",
	}
	for missing := range valid {
		t.Run("missing_"+missing, func(t *testing.T) {
			config.C.Other = map[string]string{}
			for key, value := range valid {
				if key != missing {
					config.C.Other[key] = value
				}
			}
			require.Nil(t, NewMeiliProvider().embedding)
		})
	}
	for _, invalid := range []struct{ key, value string }{
		{"RECOMMENDATION_EMBEDDER_BUDGET_APPROVED", "false"},
		{"RECOMMENDATION_EMBEDDER_MAX_DOCUMENTS", "5001"},
		{"RECOMMENDATION_EMBEDDER_MAX_UNITS_PER_HOUR", "1000001"},
		{"RECOMMENDATION_EMBEDDER_CONFIG", `{}`},
		{"RECOMMENDATION_EMBEDDER_CONFIG", `not-json`},
	} {
		t.Run("invalid_"+invalid.key+"_"+invalid.value, func(t *testing.T) {
			config.C.Other = map[string]string{}
			for key, value := range valid {
				config.C.Other[key] = value
			}
			config.C.Other[invalid.key] = invalid.value
			require.Nil(t, NewMeiliProvider().embedding)
		})
	}
	config.C.Other = valid
	require.NotNil(t, NewMeiliProvider().embedding)
}

func TestMeiliAcceptanceModelChangeCannotReuseApproval(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	defer server.Close()
	for _, field := range []string{"name", "version", "configuration", "removed"} {
		t.Run(field, func(t *testing.T) {
			p, settings := meiliTestProvider(t, server.URL)
			p.embedding = &meiliEmbedding{Name: "model", ModelVersion: "rev1", Configuration: json.RawMessage(`{"dimensions":384}`), MaxUnitsPerHour: 100}
			active := meiliTestManifest(t, p, settings)
			active.Mode = "meili_hybrid"
			ctx, token, release, err := p.lease(context.Background(), time.Second)
			require.NoError(t, err)
			require.NoError(t, p.writeState(ctx, token, meiliState{Active: &active}))
			release()
			switch field {
			case "name":
				p.embedding.Name = "new-model"
			case "version":
				p.embedding.ModelVersion = "rev2"
			case "configuration":
				p.embedding.Configuration = json.RawMessage(`{"dimensions":768}`)
			case "removed":
				p.embedding = nil
			}
			require.False(t, p.Status().Ready)
			require.Equal(t, "configuration_changed", p.Status().Reason)
			batch, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "public", Query: "public"}})
			require.Error(t, err)
			require.Empty(t, batch.Lists)
		})
	}
	require.Zero(t, calls.Load())
}

func TestMeiliAcceptanceApprovalRevokedDuringSearchDoesNotPublish(t *testing.T) {
	var revoke func()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			revoke()
		}
		_, _ = w.Write([]byte(`{"hits":[{"id":"1"}]}`))
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	meiliTestManifest(t, p, settings)
	revoke = func() { settings.ApprovedVersion = "" }
	batch, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: "public", Query: "public"}})
	require.Error(t, err)
	require.Equal(t, "rules", batch.Mode)
	require.Empty(t, batch.Lists)
	require.Equal(t, "quality_not_approved", p.Status().Reason)
}

func TestMeiliAcceptanceBudgetSharedAcrossReplicasAndRedisHour(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Date(2026, 10, 14, 10, 59, 59, 0, time.UTC))
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first, _ := meiliTestProvider(t, "http://unused")
	second, _ := meiliTestProvider(t, "http://unused")
	second.connection.Prefix = first.connection.Prefix
	for _, p := range []*MeiliProvider{first, second} {
		p.redis = func() redis.UniversalClient { return client }
		p.embedding = &meiliEmbedding{MaxUnitsPerHour: 10}
	}
	var successes atomic.Int32
	var workers sync.WaitGroup
	for i := range 24 {
		workers.Add(1)
		p := first
		if i%2 == 1 {
			p = second
		}
		go func() {
			defer workers.Done()
			if p.reserveEmbedding(context.Background(), 1) {
				successes.Add(1)
			}
		}()
	}
	workers.Wait()
	require.EqualValues(t, 10, successes.Load())
	// Provider replacement cannot refund failed work or reset the shared hour.
	restarted, _ := meiliTestProvider(t, "http://unused")
	restarted.connection.Prefix = first.connection.Prefix
	restarted.redis = first.redis
	restarted.embedding = first.embedding
	require.False(t, restarted.reserveEmbedding(context.Background(), 1))
	server.SetTime(time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC))
	require.True(t, restarted.reserveEmbedding(context.Background(), 10))
	require.False(t, second.reserveEmbedding(context.Background(), 1))
	server.Close()
	require.False(t, first.reserveEmbedding(context.Background(), 1))
}

func TestMeiliAcceptanceRealRedisBudgetSharedAcrossConnections(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("DISCOVERY_TEST_REDIS_ADDR"))
	if address == "" {
		t.Skip("set DISCOVERY_TEST_REDIS_ADDR to an isolated Redis instance")
	}
	first, _ := meiliTestProvider(t, "http://unused")
	second, _ := meiliTestProvider(t, "http://unused")
	second.connection.Prefix = first.connection.Prefix
	for _, p := range []*MeiliProvider{first, second} {
		client := redis.NewClient(&redis.Options{Addr: address})
		t.Cleanup(func() { _ = client.Close() })
		p.redis = func() redis.UniversalClient { return client }
		p.embedding = &meiliEmbedding{MaxUnitsPerHour: 10}
	}
	_, key := first.keys()
	t.Cleanup(func() { _ = first.redis().Del(context.Background(), key+":embedding-budget").Err() })
	var successes atomic.Int32
	var workers sync.WaitGroup
	for i := range 24 {
		workers.Add(1)
		p := first
		if i%2 == 1 {
			p = second
		}
		go func() {
			defer workers.Done()
			if p.reserveEmbedding(context.Background(), 1) {
				successes.Add(1)
			}
		}()
	}
	workers.Wait()
	require.EqualValues(t, 10, successes.Load())
	require.False(t, first.reserveEmbedding(context.Background(), 1))
	require.False(t, second.reserveEmbedding(context.Background(), 1))
}

func TestMeiliAcceptanceDiagnosticsDoNotExposeCredentialsOrQuery(t *testing.T) {
	const secret = "acceptance-api-secret-do-not-publish"
	const query = "acceptance-interest-do-not-publish"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(secret + " " + query))
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	p.connection.APIKey = secret
	meiliTestManifest(t, p, settings)
	batch, err := p.BuildBuckets(context.Background(), time.Now(), []ContentBucket{{Key: query, Query: query}})
	require.Error(t, err)
	require.Empty(t, batch.Lists)
	status, jsonErr := json.Marshal(p.Status())
	require.NoError(t, jsonErr)
	for _, value := range []string{err.Error(), string(status)} {
		require.NotContains(t, value, secret)
		require.NotContains(t, value, query)
	}
}

func TestMeiliAcceptanceFailedSemanticRequestIsNotRefunded(t *testing.T) {
	var semanticCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, semantic := body["hybrid"]; semantic {
			semanticCalls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"hits":[{"id":"1"}]}`))
	}))
	defer server.Close()
	p, _ := meiliTestProvider(t, server.URL)
	p.embedding = &meiliEmbedding{Name: "model", ModelVersion: "rev1", MaxUnitsPerHour: 1}
	manifest := meiliManifest{Index: "isolated", Mode: "meili_hybrid"}
	for range 2 {
		lists, err := p.searchBucket(context.Background(), manifest, time.Now(), ContentBucket{Key: "public", Query: "public"})
		require.Error(t, err)
		require.Empty(t, lists, "a failed hybrid query must not publish its preceding partial lexical results as hybrid")
	}
	require.EqualValues(t, 1, semanticCalls.Load(), "the second call must fail closed before another paid model request")
}

func TestMeiliAcceptanceHTTPRedirectCannotForwardAPIKey(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	currentMeili.RLock()
	previous := currentMeili.p
	currentMeili.RUnlock()
	t.Cleanup(func() { currentMeili.Lock(); currentMeili.p = previous; currentMeili.Unlock() })
	p := NewMeiliProvider()
	p.connection.Host = origin.URL
	p.connection.APIKey = "acceptance-redirect-secret"
	err := p.http(context.Background(), http.MethodGet, "/version", nil, nil)
	require.Error(t, err)
	require.Zero(t, targetCalls.Load())
	require.NotContains(t, err.Error(), p.connection.APIKey)
}

func TestMeiliAcceptanceRealEngineRestartAndRemoteWithdrawal(t *testing.T) {
	host := strings.TrimRight(os.Getenv("DISCOVERY_TEST_MEILI_URL"), "/")
	if host == "" {
		t.Skip("set DISCOVERY_TEST_MEILI_URL to an isolated real Meilisearch")
	}
	p, settings := meiliTestProvider(t, host)
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	checked := time.Now().UTC().Add(-time.Minute)
	domain, allow := "acceptance.example", true
	remote := &models.Account{Id: 2, Username: "remote", Acct: "remote@" + domain, Domain: &domain, Discoverable: true, RemoteDiscoverable: &allow, RemoteIndexable: &allow, RemoteDiscoveryCheckedAt: &checked}
	write := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, write.Omit("Extra", "User").Create(remote).Error)
	require.NoError(t, write.Omit("Content").Create(&models.Status{Id: 5, AccountId: 2, Visibility: models.StatusVisibilityPublic, CreatedAt: &checked, Language: "zh"}).Error)
	require.NoError(t, write.Create(&models.StatusContent{StatusId: 5, Content: "摄影 remote-author-public-control"}).Error)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	foreign := p.connection.Prefix + "unrelated-search-index"
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = p.reconcileOwned(cleanup, meiliState{})
		_ = p.task(cleanup, http.MethodDelete, "/indexes/"+foreign, nil)
	})
	require.NoError(t, p.task(ctx, http.MethodPost, "/indexes", map[string]any{"uid": foreign, "primaryKey": "id"}))
	require.NoError(t, p.rebuild(ctx))
	state, err := p.readState(ctx)
	require.NoError(t, err)
	require.NotNil(t, state.Active)
	require.Equal(t, 2, state.Active.Documents)
	firstIndex := state.Active.Index
	settings.ApprovedVersion = state.Active.Version
	// Restart with the same shared Redis state, then simulate its disposable
	// state being lost. Orphans belong to this unique prefix only.
	restarted, _ := meiliTestProvider(t, host)
	restarted.connection = p.connection
	restarted.conn, restarted.redis, restarted.settings = p.conn, p.redis, p.settings
	require.True(t, restarted.Status().Ready)
	_, stateKey := p.keys()
	require.NoError(t, p.redis().Del(ctx, stateKey).Err())
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 2).UpdateColumn("remote_discoverable", false).Error)
	// The old search document can exist until the next bounded rebuild, but
	// SQL authorization must already reject it before that refresh happens.
	eligible, err := models.FilterRecommendableStatuses(conn, nil, models.Statuses{{Id: 5}}, models.RecommendationEligibilityOptions{Context: "public", AsOf: time.Now().UTC(), Window: CandidateWindow, RootWindow: true})
	require.NoError(t, err)
	require.Empty(t, eligible)
	require.NoError(t, restarted.rebuild(ctx))
	state, err = restarted.readState(ctx)
	require.NoError(t, err)
	require.NotNil(t, state.Active)
	require.Equal(t, 1, state.Active.Documents)
	require.NotEqual(t, firstIndex, state.Active.Index)
	var metadata map[string]any
	require.True(t, meiliNotFound(p.http(ctx, http.MethodGet, "/indexes/"+firstIndex, nil, &metadata)))
	require.NoError(t, p.http(ctx, http.MethodGet, "/indexes/"+foreign, nil, &metadata))
	var inventory struct {
		Results []struct {
			UID string `json:"uid"`
		} `json:"results"`
	}
	require.NoError(t, p.http(ctx, http.MethodGet, "/indexes?limit=1000", nil, &inventory))
	owned := 0
	for _, entry := range inventory.Results {
		if strings.HasPrefix(entry.UID, p.connection.Prefix+"discovery_statuses_v1_") {
			owned++
		}
	}
	require.Equal(t, 1, owned)
	var documents struct {
		Results []meiliDocument `json:"results"`
	}
	require.NoError(t, p.http(ctx, http.MethodGet, "/indexes/"+state.Active.Index+"/documents?limit=100", nil, &documents))
	require.Len(t, documents.Results, 1)
	require.Equal(t, "1", documents.Results[0].ID)
}

func TestMeiliAcceptanceProviderChangeRevokesOldPoolAndSnapshot(t *testing.T) {
	for _, changed := range []string{"model_version", "model_config", "host", "prefix", "enabled"} {
		t.Run(changed, func(t *testing.T) {
			_, client := feedTestRedis(t)
			conn, viewer := feedTestDatabase(t)
			feedRuntime.RLock()
			previous := feedRuntime.provider
			feedRuntime.RUnlock()
			SetContentProvider(nil)
			t.Cleanup(func() { SetContentProvider(previous) })
			ctx := context.Background()
			require.NoError(t, RefreshFeedCandidates(ctx))
			pool, err := loadFeedPool(ctx, time.Now())
			require.NoError(t, err)
			var networkCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				networkCalls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			p, _ := meiliTestProvider(t, server.URL)
			p.embedding = &meiliEmbedding{Name: "public-model", ModelVersion: "revision-one", Configuration: json.RawMessage(`{"dimensions":384}`), MaxDocuments: 100, MaxUnitsPerHour: 100}
			SetContentProvider(p)
			pool.PolicyVersion, err = feedPolicy(ctx)
			require.NoError(t, err)
			pool.Mode, pool.ContentDataCutoff = "meili_hybrid", time.Now().UTC()
			pool.Lists = []ContentList{{BucketKey: "go", Family: "semantic", Generation: "approved-model-one", IDs: []uint64{1, 2, 3}}}
			data, err := json.Marshal(pool)
			require.NoError(t, err)
			require.NoError(t, client.Set(ctx, feedPoolKey, data, PoolMaxAge).Err())
			require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Tag{Id: 1, Name: "Go", NameKey: "go"}).Error)
			require.NoError(t, conn.Exec("INSERT INTO followed_tags (id,account_id,tag_id,created_at) VALUES (?,?,?,?)", 1, viewer.Id, 1, time.Now()).Error)
			_, err = models.SetRecommendationPreference(conn, viewer.UserId, models.RecommendationPersonalized)
			require.NoError(t, err)
			first, err := StatusPage(ctx, viewer, "", 1)
			require.NoError(t, err)
			require.Equal(t, "meili_hybrid", first.Mode)
			require.NotNil(t, first.NextCursor)
			// A new process reuses the Redis pool/window but has a new runtime
			// configuration; its first feed read must see the policy boundary.
			restarted, _ := meiliTestProvider(t, server.URL)
			restarted.connection = p.connection
			embedding := *p.embedding
			restarted.embedding = &embedding
			switch changed {
			case "model_version":
				restarted.embedding.ModelVersion = "revision-two"
			case "model_config":
				restarted.embedding.Configuration = json.RawMessage(`{"dimensions":768}`)
			case "host":
				restarted.connection.Host += "/new-host"
			case "prefix":
				restarted.connection.Prefix += "new-"
			case "enabled":
				restarted.connection.Enabled = false
			}
			SetContentProvider(restarted)
			_, err = StatusPage(ctx, viewer, *first.NextCursor, 1)
			require.ErrorIs(t, err, ErrResetRequired)
			fresh, err := StatusPage(ctx, viewer, "", 20)
			require.NoError(t, err)
			require.Equal(t, "rules", fresh.Mode, "old enhanced pool cannot survive provider policy change")
			require.NotEqual(t, first.Generation, fresh.Generation)
			require.Zero(t, networkCalls.Load(), "feed policy evaluation must stay local; timeline reads never call Meili")
		})
	}
}

func TestMeiliAcceptanceRealDiagnosticsRetainFailuresUntilMatchingSuccess(t *testing.T) {
	host := strings.TrimRight(os.Getenv("DISCOVERY_TEST_MEILI_URL"), "/")
	if host == "" {
		t.Skip("set DISCOVERY_TEST_MEILI_URL to an isolated real Meilisearch")
	}
	target, err := url.Parse(host)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var queryFail, buildFail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (strings.HasSuffix(r.URL.Path, "/search") && queryFail.Load()) || (r.URL.Path == "/version" && buildFail.Load()) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("upstream-error-body-secret"))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	p, settings := meiliTestProvider(t, server.URL)
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Cleanup(func() {
		queryFail.Store(false)
		buildFail.Store(false)
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		// Cleanup must bypass the test HTTP proxy, which is closed by defer.
		p.connection.Host = host
		_ = p.reconcileOwned(cleanup, meiliState{})
	})
	require.NoError(t, p.rebuild(ctx))
	state, err := p.readState(ctx)
	require.NoError(t, err)
	require.NotNil(t, state.Active)
	settings.ApprovedVersion = state.Active.Version
	queryFail.Store(true)
	_, err = p.BuildBuckets(ctx, time.Now(), []ContentBucket{{Key: "摄影", Query: "摄影"}})
	require.Error(t, err)
	buildFail.Store(true)
	require.Error(t, p.rebuild(ctx))
	readStatus := func() map[string]any {
		status := p.Status()
		require.True(t, status.Ready, "a recent approved index remains usable while diagnostics retain a failed refresh")
		encoded, err := json.Marshal(status)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "upstream-error-body-secret")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		return decoded
	}
	status := readStatus()
	require.NotEmpty(t, status["last_query_error"])
	require.NotEmpty(t, status["last_query_error_at"])
	require.NotEmpty(t, status["last_build_error"])
	require.NotEmpty(t, status["last_build_error_at"])
	replica, _ := meiliTestProvider(t, server.URL)
	replica.connection = p.connection
	replica.conn, replica.redis, replica.settings = p.conn, p.redis, p.settings
	replicaJSON, err := json.Marshal(replica.Status())
	require.NoError(t, err)
	var replicaStatus map[string]any
	require.NoError(t, json.Unmarshal(replicaJSON, &replicaStatus))
	for _, key := range []string{"last_query_error", "last_query_error_at", "last_build_error", "last_build_error_at"} {
		require.Equal(t, status[key], replicaStatus[key], "operation diagnostics must survive provider replacement")
	}
	queryFail.Store(false)
	_, err = p.BuildBuckets(ctx, time.Now(), []ContentBucket{{Key: "摄影", Query: "摄影"}})
	require.NoError(t, err)
	status = readStatus()
	require.Empty(t, status["last_query_error"])
	require.NotEmpty(t, status["last_build_error"], "successful query must not erase a failed build")
	buildFail.Store(false)
	require.NoError(t, p.rebuild(ctx))
	status = readStatus()
	require.Empty(t, status["last_query_error"])
	require.Empty(t, status["last_build_error"])
}

func TestMeiliAcceptanceLostQueryLeaseCannotEraseNewReplicaFailure(t *testing.T) {
	var failure, hold atomic.Bool
	entered, resume := make(chan struct{}), make(chan struct{})
	var resumeOnce sync.Once
	unblock := func() { resumeOnce.Do(func() { close(resume) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failure.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if hold.CompareAndSwap(true, false) {
			close(entered)
			<-resume
		}
		_, _ = w.Write([]byte(`{"hits":[{"id":"1"}]}`))
	}))
	defer server.Close()
	defer unblock()
	p, settings := meiliTestProvider(t, server.URL)
	meiliTestManifest(t, p, settings)
	replica, _ := meiliTestProvider(t, server.URL)
	replica.connection = p.connection
	replica.conn, replica.redis, replica.settings = p.conn, p.redis, p.settings
	ctx := context.Background()
	buckets := []ContentBucket{{Key: "public", Query: "public"}}
	hold.Store(true)
	completed := make(chan error, 1)
	go func() { _, err := p.BuildBuckets(ctx, time.Now(), buckets); completed <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("old query did not reach the controlled in-flight point")
	}
	// Simulate expiration/takeover while the old request remains in flight.
	lease, _ := p.keys()
	require.NoError(t, p.redis().Del(ctx, lease).Err())
	failure.Store(true)
	_, err := replica.BuildBuckets(ctx, time.Now(), buckets)
	require.Error(t, err)
	readError := func() map[string]any {
		encoded, err := json.Marshal(replica.Status())
		require.NoError(t, err)
		var value map[string]any
		require.NoError(t, json.Unmarshal(encoded, &value))
		return value
	}
	newFailure := readError()
	require.NotEmpty(t, newFailure["last_query_error"])
	require.NotEmpty(t, newFailure["last_query_error_at"])
	failure.Store(false)
	unblock()
	select {
	case err = <-completed:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("expired query failed to stop within its budget")
	}
	afterOldCompletion := readError()
	require.Equal(t, newFailure["last_query_error"], afterOldCompletion["last_query_error"])
	require.Equal(t, newFailure["last_query_error_at"], afterOldCompletion["last_query_error_at"], "obsolete owner may neither clear nor overwrite the successor's failure")
}
