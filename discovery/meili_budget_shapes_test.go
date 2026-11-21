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

	var stats meiliStats
	require.NoError(t, json.Unmarshal([]byte(`{"databaseSize":900000000,"usedDatabaseSize":100,"indexes":{"x_discovery_statuses_v1_a":{"usedIndexSize":0,"indexSize":0}}}`), &stats))
	used, source, ok := meiliBudgetBytes(stats, "x_discovery_statuses_v1_")
	require.True(t, ok)
	require.Equal(t, "used_database", source)
	require.EqualValues(t, 100, used)
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

	// Multi-replica health telemetry: when Replica A records a failed build
	// operation in Redis, Replica B (which has never probed /stats) surfaces it.
	leaseCtx, token, release, err := p.lease(ctx, time.Second)
	require.NoError(t, err)
	budgetErr := p.checkDiskBudgetWithSetting(leaseCtx, 700)
	require.ErrorContains(t, budgetErr, "772 MiB database of 700 MiB")
	p.recordOperation(token, "build", budgetErr)
	release()

	replicaB := &MeiliProvider{
		connection: p.connection,
		client:     p.client,
		conn:       p.conn,
		redis:      p.redis,
		settings:   p.settings,
		reason:     "not_built",
	}
	bStatus := replicaB.Status()
	require.EqualValues(t, 809553920, bStatus.DatabaseBytes)
	require.EqualValues(t, 700<<20, bStatus.DatabaseBudgetBytes)
	require.Equal(t, "database", bStatus.DatabaseSizeSource)
	require.Equal(t, "resource_budget_exceeded", bStatus.LastBuildError)

	require.NoError(t, p.checkDiskBudgetWithSetting(ctx, 1024))

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

	// When Meilisearch v1.15+ reports owned_indexes bytes, full-text statuses
	// growth above configuredMaxMiB must never block Discovery.
	v115 := budgetServer(t, `{"databaseSize":12884901888,"usedDatabaseSize":10737418240,"indexes":{"statuses":{"usedIndexSize":10737418240},"discovery_statuses_v1_gen1":{"numberOfDocuments":1811,"usedIndexSize":41943040}}}`)
	v115.connection.Prefix = ""
	require.NoError(t, v115.checkDiskBudgetWithSetting(ctx, 4096))
	require.Equal(t, "owned_indexes", v115.Status().DatabaseSizeSource)
	require.EqualValues(t, DefaultOwnedMeiliIndexMaxBytes, v115.Status().DatabaseBudgetBytes)

	// Dedicated engine (no shared non-discovery indexes) keeps the 512 MiB default.
	dedicated := budgetServer(t, `{"databaseSize":809553920,"indexes":{"discovery_statuses_v1_gen1":{"numberOfDocuments":1811,"isIndexing":false}}}`)
	dedicated.connection.Prefix = ""
	dedicated.maxDatabaseBytes = 0
	require.ErrorContains(t, dedicated.checkDiskBudget(ctx), "772 MiB database of 512 MiB")
}
