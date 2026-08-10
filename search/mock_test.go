package search

import (
	"context"
	"encoding/json"

	ms "github.com/meilisearch/meilisearch-go"
)

// --- mockServiceManager -------------------------------------------------------
//
// meilisearch.ServiceManager is a huge interface. To avoid implementing hundreds
// of irrelevant methods, we embed the interface (left nil) and only override the
// handful of methods that the search package actually calls. Calls to any other
// method will panic, which is the desired behaviour for a focused unit test.

// mockServiceManager is a test double for meilisearch.ServiceManager.
type mockServiceManager struct {
	ms.ServiceManager // nil-embedded: only overridden methods are usable

	idx          *mockIndexManager // returned by Index() for any uid
	versionErr   error             // when set, VersionWithContext returns this
	healthErr    error             // when set, HealthWithContext returns this
	stats        *ms.Stats         // returned by GetStatsWithContext
	statsErr     error             // when set, GetStatsWithContext returns this
	versionCalls int
}

func (m *mockServiceManager) Index(_ string) ms.IndexManager {
	if m.idx == nil {
		return &mockIndexManager{}
	}
	return m.idx
}

func (m *mockServiceManager) VersionWithContext(_ context.Context) (*ms.Version, error) {
	m.versionCalls++
	if m.versionErr != nil {
		return nil, m.versionErr
	}
	return &ms.Version{PkgVersion: "test"}, nil
}

func (m *mockServiceManager) HealthWithContext(_ context.Context) (*ms.Health, error) {
	if m.healthErr != nil {
		return nil, m.healthErr
	}
	return &ms.Health{Status: "available"}, nil
}

func (m *mockServiceManager) GetStatsWithContext(_ context.Context, _ *ms.StatsParams) (*ms.Stats, error) {
	if m.statsErr != nil {
		return nil, m.statsErr
	}
	if m.stats != nil {
		return m.stats, nil
	}
	return &ms.Stats{}, nil
}

func (m *mockServiceManager) Close() {}

// --- mockIndexManager ---------------------------------------------------------

// mockIndexManager is a test double for meilisearch.IndexManager.
type mockIndexManager struct {
	ms.IndexManager // nil-embedded

	searchResp  *ms.SearchResponse
	searchErr   error
	addErr      error
	addBatchErr error
	deleteErr   error
	settingsErr error

	// call recorders (for asserting behavior)
	addDocsCalls        int
	addBatchCalls       int
	deleteDocsCalls     int
	searchCalls         int
	updateSettingsCalls int
	lastSearchRequest   *ms.SearchRequest
	lastAddDocs         interface{}
	lastDeletedID       string
}

func (m *mockIndexManager) SearchWithContext(_ context.Context, _ string, req *ms.SearchRequest) (*ms.SearchResponse, error) {
	m.searchCalls++
	m.lastSearchRequest = req
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	if m.searchResp != nil {
		return m.searchResp, nil
	}
	return &ms.SearchResponse{}, nil
}

func (m *mockIndexManager) AddDocumentsWithContext(_ context.Context, docs interface{}, _ *ms.DocumentOptions) (*ms.TaskInfo, error) {
	m.addDocsCalls++
	m.lastAddDocs = docs
	if m.addErr != nil {
		return nil, m.addErr
	}
	return &ms.TaskInfo{TaskUID: 1}, nil
}

func (m *mockIndexManager) AddDocumentsInBatchesWithContext(_ context.Context, docs interface{}, _ int, _ *ms.DocumentOptions) ([]ms.TaskInfo, error) {
	m.addBatchCalls++
	m.lastAddDocs = docs
	if m.addBatchErr != nil {
		return nil, m.addBatchErr
	}
	return []ms.TaskInfo{{TaskUID: 1}}, nil
}

func (m *mockIndexManager) DeleteDocumentWithContext(_ context.Context, id string, _ *ms.DocumentOptions) (*ms.TaskInfo, error) {
	m.deleteDocsCalls++
	m.lastDeletedID = id
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return &ms.TaskInfo{TaskUID: 1}, nil
}

func (m *mockIndexManager) UpdateSettingsWithContext(_ context.Context, _ *ms.Settings) (*ms.TaskInfo, error) {
	m.updateSettingsCalls++
	if m.settingsErr != nil {
		return nil, m.settingsErr
	}
	return &ms.TaskInfo{TaskUID: 1}, nil
}

// --- helpers ------------------------------------------------------------------

// newEnabledClient creates a MeilisearchClient backed by a mock service manager.
// The client is enabled and ready for unit testing.
func newEnabledClient(svc *mockServiceManager) *MeilisearchClient {
	return &MeilisearchClient{
		client:  svc,
		host:    "http://localhost:7700",
		apiKey:  "test-key",
		enabled: true,
	}
}

// makeHits builds meilisearch.Hits from a list of field maps.
// Each map's keys are JSON field names (e.g. "id", "name").
func makeHits(docs ...map[string]string) ms.Hits {
	hits := make(ms.Hits, 0, len(docs))
	for _, d := range docs {
		h := ms.Hit{}
		for k, v := range d {
			raw, _ := json.Marshal(v)
			h[k] = raw // json.RawMessage is []byte
		}
		hits = append(hits, h)
	}
	return hits
}
