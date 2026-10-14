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
