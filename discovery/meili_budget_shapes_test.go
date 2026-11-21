package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func budgetServer(t *testing.T, body string) *MeiliProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	p, _ := meiliTestProvider(t, server.URL)
	return p
}

func TestMeiliDiskBudgetStatsShapes(t *testing.T) {
	// Production v1.12.8: databaseSize only, per-index entries without sizes,
	// usedDatabaseSize zero. Must fall back to databaseSize, not fail.
	p := budgetServer(t, `{"databaseSize":297689088,"usedDatabaseSize":0,"indexes":{"statuses":{"numberOfDocuments":5,"isIndexing":false},"qa_discovery_statuses_v1_g":{"numberOfDocuments":1,"isIndexing":false}}}`)
	require.NoError(t, p.checkDiskBudget(context.Background()))
	p.maxDatabaseBytes = 256 << 20
	require.ErrorContains(t, p.checkDiskBudget(context.Background()), "283 MiB database")

	// Nothing readable: proceed (bounded by the row limit), never block.
	p = budgetServer(t, `{"indexes":{}}`)
	require.NoError(t, p.checkDiskBudget(context.Background()))
	p = budgetServer(t, `{"databaseSize":0}`)
	require.NoError(t, p.checkDiskBudget(context.Background()))
	// Document counts are usable even if this server omits byte sizes.
	p = budgetServer(t, `{"indexes":{"statuses":{"numberOfDocuments":500000},"qa_discovery_statuses_v1_g":{"numberOfDocuments":10001}}}`)
	p.connection.Prefix = "qa_"
	require.ErrorContains(t, p.checkDiskBudget(context.Background()), "document budget exceeded")

	var stats meiliStats
	require.NoError(t, json.Unmarshal([]byte(`{"databaseSize":900000000,"usedDatabaseSize":100,"indexes":{"x_discovery_statuses_v1_a":{"usedIndexSize":0,"indexSize":0}}}`), &stats))
	used, source, ok := stats.budgetBytes(stats.indexUsage("x_discovery_statuses_v1_"), DefaultOwnedMeiliIndexMaxBytes)
	require.True(t, ok)
	require.Equal(t, "used_database", source)
	require.EqualValues(t, 100, used)
}

func TestMeiliDiskBudgetRetainsDedicatedLimit(t *testing.T) {
	p := budgetServer(t, `{"databaseSize":809553920,"indexes":{"discovery_statuses_v1_gen1":{"numberOfDocuments":10}}}`)
	p.connection.Prefix = ""
	p.maxDatabaseBytes = 0
	require.ErrorContains(t, p.checkDiskBudgetWithSetting(context.Background(), 8192, ""), "772 MiB database of 512 MiB")
}

func TestMeiliDiskBudgetHandlesIncompleteOwnedSizes(t *testing.T) {
	ctx := context.Background()
	p := budgetServer(t, `{"databaseSize":10737418240,"indexes":{"statuses":{"usedIndexSize":314572800},"discovery_statuses_v1_a":{"usedIndexSize":41943040},"discovery_statuses_v1_b":{"numberOfDocuments":10}}}`)
	p.connection.Prefix = ""
	require.ErrorContains(t, p.checkDiskBudget(ctx), "10240 MiB database of 8192 MiB")

	p = budgetServer(t, `{"databaseSize":1073741824,"indexes":{"statuses":{},"discovery_statuses_v1_a":{"usedIndexSize":524288000},"discovery_statuses_v1_b":{"numberOfDocuments":10}}}`)
	p.connection.Prefix = ""
	require.ErrorContains(t, p.checkDiskBudget(ctx), "500 MiB owned_indexes of 512 MiB")
}

func TestMeiliDiskTelemetryAcrossUpgrade(t *testing.T) {
	p, _ := meiliTestProvider(t, "http://unused")
	ctx := context.Background()
	// A new replica can start before any upgraded worker has sampled /stats.
	// Previously deployed workers stored disk data in the build health record.
	_, state := p.keys()
	legacy, err := json.Marshal(map[string]any{
		"policy": p.PolicyVersion(), "code": "resource_budget_exceeded", "at": time.Now().UTC(),
		"database_bytes": 809553920, "database_budget_bytes": 700 << 20, "database_size_source": "database",
	})
	require.NoError(t, err)
	require.NoError(t, p.redis().HSet(ctx, state+":health", "build", string(legacy)).Err())
	status := p.Status()
	require.Equal(t, "resource_budget_exceeded", status.LastBuildError)
	require.EqualValues(t, 700<<20, status.DatabaseBudgetBytes)

	// An unreadable sample from an upgraded worker supersedes the old reading.
	_, token, release, err := p.lease(ctx, time.Second)
	require.NoError(t, err)
	defer release()
	p.recordDiskTelemetry(0, 0, "", token)
	require.Zero(t, p.Status().DatabaseBudgetBytes)
}

func TestMeiliRebuildLogSuppressionCounts(t *testing.T) {
	p, _ := meiliTestProvider(t, "http://unused")
	start := time.Now()
	failure := errors.New("boom")
	p.logRebuildAt(failure, start)
	for i := 1; i <= 5; i++ {
		p.logRebuildAt(failure, start.Add(time.Duration(i)*time.Minute))
	}
	require.Equal(t, 5, p.suppressed)
	p.logRebuildAt(failure, start.Add(11*time.Minute))
	require.Zero(t, p.suppressed, "periodic warning reports and resets the count")
	p.logRebuildAt(nil, start.Add(12*time.Minute))
	require.Empty(t, p.lastWarnReason)
}

func TestMeiliSharedV112DatabaseBudgetAndTelemetry(t *testing.T) {
	ctx := context.Background()

	// Bundled Meilisearch v1.12.8 sharing one LMDB database across accounts,
	// statuses, hashtags, and discovery_statuses_v1_* reports only whole-DB
	// databaseSize (772 MiB) and per-index document counts. Without an explicit
	// ceiling override, Discovery must use the shared-engine ceiling (8 GiB)
	// rather than blocking rebuilds at 512 MiB.
	sharedPayload := `{"databaseSize":809553920,"indexes":{"accounts":{"numberOfDocuments":8341,"isIndexing":false},"statuses":{"numberOfDocuments":140740,"isIndexing":false},"hashtags":{"numberOfDocuments":8146,"isIndexing":false},"discovery_statuses_v1_gen1":{"numberOfDocuments":1811,"isIndexing":false}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sharedPayload))
	}))
	t.Cleanup(server.Close)
	p, cfg := meiliTestProvider(t, server.URL)
	p.connection.Prefix = ""
	p.maxDatabaseBytes = 0
	require.NoError(t, p.checkDiskBudget(ctx))

	st := p.Status()
	require.EqualValues(t, 809553920, st.DatabaseBytes)
	require.EqualValues(t, DefaultSharedMeiliDatabaseMaxBytes, st.DatabaseBudgetBytes)
	require.Equal(t, "database", st.DatabaseSizeSource)

	// Switching to rules mode hides stale Meilisearch disk telemetry.
	cfg.Mode = "rules"
	require.Zero(t, p.Status().DatabaseBudgetBytes)
	cfg.Mode = "auto"

	replica := func() *MeiliProvider {
		return &MeiliProvider{
			connection: p.connection,
			client:     p.client,
			conn:       p.conn,
			redis:      p.redis,
			settings:   p.settings,
			reason:     "not_built",
		}
	}
	replicaB := replica()
	require.NoError(t, replicaB.checkDiskBudget(ctx))
	require.Equal(t, DefaultSharedMeiliDatabaseMaxBytes, replicaB.Status().DatabaseBudgetBytes)

	// Both an already-running replica and a newly started one need to report
	// the measurement attached to the latest fenced build operation.
	leaseCtx, token, release, err := p.lease(ctx, time.Second)
	require.NoError(t, err)
	budgetErr := p.checkDiskBudgetWithSetting(leaseCtx, 700, token)
	require.ErrorContains(t, budgetErr, "772 MiB database of 700 MiB")
	p.recordOperation(token, "build", budgetErr)
	release()

	for _, reader := range []*MeiliProvider{replicaB, replica()} {
		status := reader.Status()
		require.EqualValues(t, 809553920, status.DatabaseBytes)
		require.EqualValues(t, 700<<20, status.DatabaseBudgetBytes)
		require.Equal(t, "database", status.DatabaseSizeSource)
		require.Equal(t, "resource_budget_exceeded", status.LastBuildError)
	}

	// The recent-index fast path records a successful operation without a
	// new /stats probe. It must not publish this replica's older measurement.
	_, token, release, err = replicaB.lease(ctx, time.Second)
	require.NoError(t, err)
	replicaB.recordOperation(token, "build", nil)
	release()
	require.EqualValues(t, 700<<20, replica().Status().DatabaseBudgetBytes)

	require.NoError(t, p.checkDiskBudgetWithSetting(ctx, 1024, ""))

	// Small/young instance with high-water-mark LMDB free pages (e.g. 1.2 GiB
	// data.mdb with 2,000 statuses and 2,000 discovery docs) must NOT false-positive.
	highWater := budgetServer(t, `{"databaseSize":1258291200,"indexes":{"statuses":{"numberOfDocuments":2000,"isIndexing":false},"discovery_statuses_v1_gen1":{"numberOfDocuments":2000,"isIndexing":false}}}`)
	highWater.connection.Prefix = ""
	require.NoError(t, highWater.checkDiskBudget(ctx))

	// Runaway owned Discovery document accumulation (> 2 * meiliRowLimit) is rejected.
	runawayDocs := budgetServer(t, `{"databaseSize":209715200,"indexes":{"statuses":{"numberOfDocuments":100000,"isIndexing":false},"discovery_statuses_v1_gen1":{"numberOfDocuments":10001,"isIndexing":false}}}`)
	runawayDocs.connection.Prefix = ""
	err = runawayDocs.checkDiskBudget(ctx)
	require.ErrorContains(t, err, "document budget exceeded")
	require.Equal(t, "resource_budget_exceeded", meiliSafeBudgetReason(err))

	// v1.15.2 reports rawDocumentDbSize, which omits index structures.
	v115 := budgetServer(t, `{"databaseSize":12884901888,"usedDatabaseSize":10737418240,"indexes":{"statuses":{"rawDocumentDbSize":524288000},"discovery_statuses_v1_gen1":{"numberOfDocuments":1811,"rawDocumentDbSize":41943040}}}`)
	v115.connection.Prefix = ""
	require.ErrorContains(t, v115.checkDiskBudget(ctx), "10240 MiB used_database of 8192 MiB")

	// When full per-index sizes are available, unrelated indexes do not
	// count against the owned index budget.
	perIndex := budgetServer(t, `{"databaseSize":12884901888,"usedDatabaseSize":10737418240,"indexes":{"statuses":{"usedIndexSize":10737418240},"discovery_statuses_v1_gen1":{"numberOfDocuments":1811,"usedIndexSize":41943040}}}`)
	perIndex.connection.Prefix = ""
	require.NoError(t, perIndex.checkDiskBudgetWithSetting(ctx, 4096, ""))
	require.Equal(t, "owned_indexes", perIndex.Status().DatabaseSizeSource)
	require.EqualValues(t, DefaultOwnedMeiliIndexMaxBytes, perIndex.Status().DatabaseBudgetBytes)
	// On the first build, per-index byte support is visible on the full-text
	// indexes and the owned footprint is zero. Their size must not block setup.
	initial := budgetServer(t, `{"databaseSize":12884901888,"usedDatabaseSize":10737418240,"indexes":{"statuses":{"usedIndexSize":10737418240}}}`)
	require.NoError(t, initial.checkDiskBudgetWithSetting(ctx, 4096, ""))
	initialStatus := initial.Status()
	require.Equal(t, "owned_indexes", initialStatus.DatabaseSizeSource)
	require.Zero(t, initialStatus.DatabaseBytes)
	require.EqualValues(t, DefaultOwnedMeiliIndexMaxBytes, initialStatus.DatabaseBudgetBytes)

	// Dedicated engine (no shared non-discovery indexes) keeps the 512 MiB default.
	dedicated := budgetServer(t, `{"databaseSize":809553920,"indexes":{"discovery_statuses_v1_gen1":{"numberOfDocuments":1811,"isIndexing":false}}}`)
	dedicated.connection.Prefix = ""
	dedicated.maxDatabaseBytes = 0
	require.ErrorContains(t, dedicated.checkDiskBudget(ctx), "772 MiB database of 512 MiB")
}
