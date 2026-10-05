package search

import (
	"errors"
	"sync"
	"testing"

	"github.com/kasuganosora/community/config"
	ms "github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// getMeilisearchHost / getMeilisearchAPIKey / isMeilisearchEnabled
// ---------------------------------------------------------------------------

func TestGetMeilisearchHost_Default(t *testing.T) {
	// config.C.Other is empty in tests, so default is returned
	host := getMeilisearchHost()
	assert.Equal(t, "http://127.0.0.1:7700", host)
}

func TestGetMeilisearchHost_Configured(t *testing.T) {
	original := config.C.Other["MEILISEARCH_HOST"]
	config.C.Other["MEILISEARCH_HOST"] = "http://meili:7700"
	t.Cleanup(func() {
		if original == "" {
			delete(config.C.Other, "MEILISEARCH_HOST")
		} else {
			config.C.Other["MEILISEARCH_HOST"] = original
		}
	})

	assert.Equal(t, "http://meili:7700", getMeilisearchHost())
}

func TestGetMeilisearchAPIKey_Default(t *testing.T) {
	delete(config.C.Other, "MEILISEARCH_API_KEY")
	assert.Empty(t, getMeilisearchAPIKey())
}

func TestGetMeilisearchAPIKey_Configured(t *testing.T) {
	original := config.C.Other["MEILISEARCH_API_KEY"]
	config.C.Other["MEILISEARCH_API_KEY"] = "secret-key"
	t.Cleanup(func() {
		if original == "" {
			delete(config.C.Other, "MEILISEARCH_API_KEY")
		} else {
			config.C.Other["MEILISEARCH_API_KEY"] = original
		}
	})

	assert.Equal(t, "secret-key", getMeilisearchAPIKey())
}

func TestIsMeilisearchEnabled_Default(t *testing.T) {
	delete(config.C.Other, "MEILISEARCH_ENABLED")
	assert.False(t, isMeilisearchEnabled())
}

func TestIsMeilisearchEnabled_True(t *testing.T) {
	original := config.C.Other["MEILISEARCH_ENABLED"]
	config.C.Other["MEILISEARCH_ENABLED"] = "true"
	t.Cleanup(func() {
		if original == "" {
			delete(config.C.Other, "MEILISEARCH_ENABLED")
		} else {
			config.C.Other["MEILISEARCH_ENABLED"] = original
		}
	})

	assert.True(t, isMeilisearchEnabled())
}

func TestIsMeilisearchEnabled_TrueUppercase(t *testing.T) {
	original := config.C.Other["MEILISEARCH_ENABLED"]
	config.C.Other["MEILISEARCH_ENABLED"] = "TRUE"
	t.Cleanup(func() {
		if original == "" {
			delete(config.C.Other, "MEILISEARCH_ENABLED")
		} else {
			config.C.Other["MEILISEARCH_ENABLED"] = original
		}
	})

	// ToLower normalizes case
	assert.True(t, isMeilisearchEnabled())
}

func TestIsMeilisearchEnabled_FalseValue(t *testing.T) {
	original := config.C.Other["MEILISEARCH_ENABLED"]
	config.C.Other["MEILISEARCH_ENABLED"] = "false"
	t.Cleanup(func() {
		if original == "" {
			delete(config.C.Other, "MEILISEARCH_ENABLED")
		} else {
			config.C.Other["MEILISEARCH_ENABLED"] = original
		}
	})

	assert.False(t, isMeilisearchEnabled())
}

// ---------------------------------------------------------------------------
// IsEnabled (client != nil checks)
// ---------------------------------------------------------------------------

func TestIsEnabled_EnabledWithClient(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)
	assert.True(t, client.IsEnabled())
}

func TestIsEnabled_Disabled(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	assert.False(t, client.IsEnabled())
}

func TestIsEnabled_EnabledButNilClient(t *testing.T) {
	client := &MeilisearchClient{enabled: true, client: nil}
	assert.False(t, client.IsEnabled())
}

func TestSetEnabled_ConcurrentSafe(t *testing.T) {
	client := &MeilisearchClient{enabled: true}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(v bool) {
			defer wg.Done()
			client.setEnabled(v)
		}(i%2 == 0)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// IndexAccount / IndexStatus / IndexHashtag (enabled path with mock)
// ---------------------------------------------------------------------------

func TestIndexAccount_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	doc := &AccountSearchDocument{ID: "1", Username: "alice"}
	err := client.IndexAccount(doc)
	require.NoError(t, err)
	assert.Equal(t, 1, idx.addDocsCalls)
}

func TestIndexAccount_InvalidDoc(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	// nil doc
	err := client.IndexAccount(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid document")

	// empty id
	err = client.IndexAccount(&AccountSearchDocument{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}

func TestIndexAccount_Error(t *testing.T) {
	idx := &mockIndexManager{addErr: errors.New("network error")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.IndexAccount(&AccountSearchDocument{ID: "1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network error")
}

func TestIndexStatus_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	doc := &StatusSearchDocument{ID: "10", Content: "hello"}
	err := client.IndexStatus(doc)
	require.NoError(t, err)
	assert.Equal(t, 1, idx.addDocsCalls)
}

func TestIndexStatus_InvalidDoc(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	err := client.IndexStatus(nil)
	assert.Error(t, err)

	err = client.IndexStatus(&StatusSearchDocument{})
	assert.Error(t, err)
}

func TestIndexStatus_Error(t *testing.T) {
	idx := &mockIndexManager{addErr: errors.New("boom")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.IndexStatus(&StatusSearchDocument{ID: "1"})
	assert.Error(t, err)
}

func TestIndexHashtag_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	doc := &HashtagSearchDocument{ID: "1", Name: "golang"}
	err := client.IndexHashtag(doc)
	require.NoError(t, err)
	assert.Equal(t, 1, idx.addDocsCalls)
}

func TestIndexHashtag_InvalidDoc(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	err := client.IndexHashtag(nil)
	assert.Error(t, err)

	err = client.IndexHashtag(&HashtagSearchDocument{})
	assert.Error(t, err)
}

func TestIndexHashtag_Error(t *testing.T) {
	idx := &mockIndexManager{addErr: errors.New("fail")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.IndexHashtag(&HashtagSearchDocument{ID: "1"})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// DeleteAccount / DeleteStatus / DeleteHashtag
// ---------------------------------------------------------------------------

func TestDeleteAccount_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.DeleteAccount("123")
	require.NoError(t, err)
	assert.Equal(t, 1, idx.deleteDocsCalls)
	assert.Equal(t, "123", idx.lastDeletedID)
}

func TestDeleteAccount_EmptyID(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	err := client.DeleteAccount("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}

func TestDeleteAccount_Error(t *testing.T) {
	idx := &mockIndexManager{deleteErr: errors.New("delete failed")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.DeleteAccount("1")
	assert.Error(t, err)
}

func TestDeleteStatus_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.DeleteStatus("456")
	require.NoError(t, err)
	assert.Equal(t, 1, idx.deleteDocsCalls)
	assert.Equal(t, "456", idx.lastDeletedID)
}

func TestDeleteStatus_EmptyID(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	err := client.DeleteStatus("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}

func TestDeleteStatus_Error(t *testing.T) {
	idx := &mockIndexManager{deleteErr: errors.New("nope")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.DeleteStatus("1")
	assert.Error(t, err)
}

func TestDeleteHashtag_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.DeleteHashtag("789")
	require.NoError(t, err)
	assert.Equal(t, 1, idx.deleteDocsCalls)
	assert.Equal(t, "789", idx.lastDeletedID)
}

func TestDeleteHashtag_EmptyID(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	err := client.DeleteHashtag("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}

func TestDeleteHashtag_Error(t *testing.T) {
	idx := &mockIndexManager{deleteErr: errors.New("can't")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.DeleteHashtag("1")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// BatchIndexAccounts / BatchIndexStatuses
// ---------------------------------------------------------------------------

func TestBatchIndexAccounts_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	docs := []*AccountSearchDocument{
		{ID: "1", Username: "a"},
		{ID: "2", Username: "b"},
	}
	err := client.BatchIndexAccounts(docs)
	require.NoError(t, err)
	assert.Equal(t, 1, idx.addBatchCalls)
}

func TestBatchIndexAccounts_Empty(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	err := client.BatchIndexAccounts(nil)
	assert.NoError(t, err)

	err = client.BatchIndexAccounts([]*AccountSearchDocument{})
	assert.NoError(t, err)
}

func TestBatchIndexAccounts_Error(t *testing.T) {
	idx := &mockIndexManager{addBatchErr: errors.New("batch fail")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.BatchIndexAccounts([]*AccountSearchDocument{{ID: "1"}})
	assert.Error(t, err)
}

func TestBatchIndexStatuses_Success(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	docs := []*StatusSearchDocument{{ID: "1"}, {ID: "2"}}
	err := client.BatchIndexStatuses(docs)
	require.NoError(t, err)
	assert.Equal(t, 1, idx.addBatchCalls)
}

func TestBatchIndexStatuses_Empty(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	err := client.BatchIndexStatuses(nil)
	assert.NoError(t, err)
}

func TestBatchIndexStatuses_Error(t *testing.T) {
	idx := &mockIndexManager{addBatchErr: errors.New("err")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	err := client.BatchIndexStatuses([]*StatusSearchDocument{{ID: "1"}})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// SearchAccounts
// ---------------------------------------------------------------------------

func TestSearchAccounts_Success(t *testing.T) {
	idx := &mockIndexManager{
		searchResp: &ms.SearchResponse{
			Hits: makeHits(
				map[string]string{"id": "1"},
				map[string]string{"id": "2"},
			),
		},
	}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchAccounts("alice", 10, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, ids)
	// verify limit/offset were passed
	assert.Equal(t, int64(10), idx.lastSearchRequest.Limit)
	assert.Equal(t, int64(0), idx.lastSearchRequest.Offset)
}

func TestSearchAccounts_ClampsLimit(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	_, err := client.SearchAccounts("test", 1000, -5, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(maxSearchLimit), idx.lastSearchRequest.Limit)
	assert.Equal(t, int64(0), idx.lastSearchRequest.Offset)
}

func TestSearchAccounts_WithFollowingIDs(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchAccounts("test", 10, 0, []string{"1", "2", "3"})
	require.NoError(t, err)
	assert.NotNil(t, ids)
	// Filter should contain the sanitized IDs
	assert.Contains(t, idx.lastSearchRequest.Filter, "id IN [1, 2, 3]")
}

func TestSearchAccounts_FollowingIDsAllInvalid(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	// All malicious IDs => empty result, search NOT called
	ids, err := client.SearchAccounts("test", 10, 0, []string{"' OR 1=1", "abc"})
	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.Equal(t, 0, idx.searchCalls)
}

func TestSearchAccounts_FollowingIDsMixedValid(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	// Mixed valid/invalid => only valid kept
	_, err := client.SearchAccounts("test", 10, 0, []string{"1", "bad", "2"})
	require.NoError(t, err)
	filter, ok := idx.lastSearchRequest.Filter.(string)
	require.True(t, ok)
	assert.Contains(t, filter, "1")
	assert.Contains(t, filter, "2")
	assert.NotContains(t, filter, "bad")
}

func TestSearchAccounts_SearchError(t *testing.T) {
	idx := &mockIndexManager{searchErr: errors.New("search down")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchAccounts("test", 10, 0, nil)
	assert.Error(t, err)
	assert.Nil(t, ids)
}

func TestSearchAccounts_EmptyHits(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{Hits: ms.Hits{}}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchAccounts("nobody", 10, 0, nil)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

// ---------------------------------------------------------------------------
// SearchStatuses
// ---------------------------------------------------------------------------

func TestSearchStatuses_Success(t *testing.T) {
	idx := &mockIndexManager{
		searchResp: &ms.SearchResponse{
			Hits: makeHits(map[string]string{"id": "100"}),
		},
	}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchStatuses("hello", 5, 0, "1", []string{"0"})
	require.NoError(t, err)
	assert.Equal(t, []string{"100"}, ids)
	// verify sort is set
	assert.Equal(t, []string{"created_at:desc"}, idx.lastSearchRequest.Sort)
}

func TestSearchStatuses_InvalidAccountID(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)

	ids, err := client.SearchStatuses("hello", 5, 0, "1 OR 1=1", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid account_id")
	assert.Nil(t, ids)
}

func TestSearchStatuses_WithVisibilityFilter(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	_, err := client.SearchStatuses("hello", 5, 0, "", []string{"0", "1"})
	require.NoError(t, err)
	filter, ok := idx.lastSearchRequest.Filter.(string)
	require.True(t, ok)
	assert.Contains(t, filter, "visibility = 0 OR visibility = 1")
}

func TestSearchStatuses_VisibilityAllInvalid(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	// All invalid visibility values => no filter for visibility, but search still runs
	_, err := client.SearchStatuses("hello", 5, 0, "", []string{"abc", "xyz"})
	require.NoError(t, err)
	filter, ok := idx.lastSearchRequest.Filter.(string)
	require.True(t, ok)
	// no visibility clause should be present
	assert.Empty(t, filter)
}

func TestSearchStatuses_SearchError(t *testing.T) {
	idx := &mockIndexManager{searchErr: errors.New("down")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchStatuses("hello", 5, 0, "", nil)
	assert.Error(t, err)
	assert.Nil(t, ids)
}

func TestSearchStatuses_ClampsLimit(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	_, err := client.SearchStatuses("test", -1, -10, "", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), idx.lastSearchRequest.Limit)
	assert.Equal(t, int64(0), idx.lastSearchRequest.Offset)
}

// ---------------------------------------------------------------------------
// SearchHashtags
// ---------------------------------------------------------------------------

func TestSearchHashtags_Success(t *testing.T) {
	idx := &mockIndexManager{
		searchResp: &ms.SearchResponse{
			Hits: makeHits(
				map[string]string{"name": "golang"},
				map[string]string{"name": "rust"},
			),
		},
	}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchHashtags("go", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"golang", "rust"}, ids)
	assert.Equal(t, []string{"status_count:desc"}, idx.lastSearchRequest.Sort)
}

func TestSearchHashtags_SearchError(t *testing.T) {
	idx := &mockIndexManager{searchErr: errors.New("nope")}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchHashtags("go", 10, 0)
	assert.Error(t, err)
	assert.Nil(t, ids)
}

func TestSearchHashtags_EmptyResults(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{Hits: ms.Hits{}}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchHashtags("zzz", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestSearchHashtags_ClampsLimit(t *testing.T) {
	idx := &mockIndexManager{searchResp: &ms.SearchResponse{}}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	_, err := client.SearchHashtags("test", 500, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(maxSearchLimit), idx.lastSearchRequest.Limit)
}

// ---------------------------------------------------------------------------
// GetStats
// ---------------------------------------------------------------------------

func TestGetStats_Success(t *testing.T) {
	stats := &ms.Stats{
		DatabaseSize: "1000",
		Indexes: map[string]ms.StatsIndex{
			"accounts": {
				NumberOfDocuments: 42,
				IsIndexing:        false,
				FieldDistribution: map[string]int64{"username": 42},
			},
		},
	}
	svc := &mockServiceManager{stats: stats}
	client := newEnabledClient(svc)

	result, err := client.GetStats()
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "1000", result["database_size"])
	indexes, ok := result["indexes"].(map[string]any)
	require.True(t, ok)
	acct, ok := indexes["accounts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(42), acct["number_of_documents"])
	assert.False(t, acct["is_indexing"].(bool))
}

func TestGetStats_Error(t *testing.T) {
	svc := &mockServiceManager{statsErr: errors.New("stats unavailable")}
	client := newEnabledClient(svc)

	result, err := client.GetStats()
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetStats_EmptyStats(t *testing.T) {
	svc := &mockServiceManager{stats: &ms.Stats{}}
	client := newEnabledClient(svc)

	result, err := client.GetStats()
	require.NoError(t, err)
	require.NotNil(t, result)
	indexes, ok := result["indexes"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, indexes)
}

// ---------------------------------------------------------------------------
// HealthCheck
// ---------------------------------------------------------------------------

func TestHealthCheck_Healthy(t *testing.T) {
	svc := &mockServiceManager{}
	client := newEnabledClient(svc)
	assert.True(t, client.HealthCheck())
}

func TestHealthCheck_Unhealthy(t *testing.T) {
	svc := &mockServiceManager{healthErr: errors.New("unhealthy")}
	client := newEnabledClient(svc)
	assert.False(t, client.HealthCheck())
}

func TestHealthCheck_Disabled(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	assert.False(t, client.HealthCheck())
}

// ---------------------------------------------------------------------------
// ToJSON (more edge cases)
// ---------------------------------------------------------------------------

func TestToJSON_StatusDocument(t *testing.T) {
	doc := &StatusSearchDocument{ID: "1", Content: "hello"}
	s := ToJSON(doc)
	assert.Contains(t, s, `"id"`)
	assert.Contains(t, s, `"content"`)
}

func TestToJSON_HashtagDocument(t *testing.T) {
	doc := &HashtagSearchDocument{ID: "1", Name: "tag"}
	s := ToJSON(doc)
	assert.Contains(t, s, `"name"`)
}

func TestToJSON_SearchResult(t *testing.T) {
	result := &SearchResult{Accounts: []string{"1"}}
	s := ToJSON(result)
	assert.Contains(t, s, `"accounts"`)
}

func TestToJSON_Map(t *testing.T) {
	m := map[string]int{"a": 1}
	s := ToJSON(m)
	assert.Contains(t, s, `"a"`)
}

func TestToJSON_Nil(t *testing.T) {
	s := ToJSON(nil)
	assert.Equal(t, "null", s)
}

// ---------------------------------------------------------------------------
// StringToUint64 (additional cases)
// ---------------------------------------------------------------------------

func TestStringToUint64_Plus(t *testing.T) {
	// ParseUint does not accept a leading "+" for unsigned numbers
	assert.Equal(t, uint64(0), StringToUint64("+123"))
}

func TestStringToUint64_Underscore(t *testing.T) {
	assert.Equal(t, uint64(0), StringToUint64("1_000"))
}

// ---------------------------------------------------------------------------
// ParseSearchQuery (additional coverage)
// ---------------------------------------------------------------------------

func TestParseSearchQuery_TagOnly(t *testing.T) {
	text, filters := ParseSearchQuery("#golang")
	assert.Empty(t, text)
	assert.Equal(t, "golang", filters["tag"])
}

func TestParseSearchQuery_BeforeOnly(t *testing.T) {
	text, filters := ParseSearchQuery("before:2024-06-01")
	assert.Empty(t, text)
	assert.Equal(t, "2024-06-01", filters["before"])
}

func TestParseSearchQuery_AfterOnly(t *testing.T) {
	text, filters := ParseSearchQuery("after:2023-01-01")
	assert.Empty(t, text)
	assert.Equal(t, "2023-01-01", filters["after"])
}

func TestParseSearchQuery_IsOnly(t *testing.T) {
	text, filters := ParseSearchQuery("is:reply")
	assert.Empty(t, text)
	assert.Equal(t, "reply", filters["is"])
}

func TestParseSearchQuery_HasOnly(t *testing.T) {
	text, filters := ParseSearchQuery("has:media")
	assert.Empty(t, text)
	assert.Equal(t, "media", filters["has"])
}

func TestParseSearchQuery_FromOnly(t *testing.T) {
	text, filters := ParseSearchQuery("from:alice")
	assert.Empty(t, text)
	assert.Equal(t, "alice", filters["from"])
}

func TestParseSearchQuery_MultipleSpaces(t *testing.T) {
	text, filters := ParseSearchQuery("hello    world    from:bob")
	// "from:bob" is a filter, so text should be just the plain words (whitespace normalized)
	assert.Equal(t, "hello world", text)
	assert.Equal(t, "bob", filters["from"])
}

func TestParseSearchQuery_OnlyPrefixNoValue(t *testing.T) {
	// "from:" with empty value
	text, filters := ParseSearchQuery("from:")
	assert.Empty(t, text)
	assert.Equal(t, "", filters["from"])
}

// ---------------------------------------------------------------------------
// createIndexWithSettings / initIndexes (via init with mock)
// ---------------------------------------------------------------------------

func TestInitIndexes_WithMock(t *testing.T) {
	idx := &mockIndexManager{}
	svc := &mockServiceManager{idx: idx}
	client := &MeilisearchClient{
		client:  svc,
		enabled: true,
	}

	// initIndexes should call UpdateSettingsWithContext 3 times (one per index)
	client.initIndexes()
	assert.Equal(t, 3, idx.updateSettingsCalls)
}

func TestCreateIndexWithSettings_SettingsError(t *testing.T) {
	idx := &mockIndexManager{settingsErr: errors.New("settings error")}
	svc := &mockServiceManager{idx: idx}
	client := &MeilisearchClient{
		client:  svc,
		enabled: true,
	}

	// Should not panic, just log the error
	assert.NotPanics(t, func() {
		client.createIndexWithSettings("test", &ms.Settings{})
	})
	assert.Equal(t, 1, idx.updateSettingsCalls)
}

// ---------------------------------------------------------------------------
// init / reconnect path
//
// init() creates its own real meilisearch client via meilisearch.New(...),
// overwriting m.client, so the mock cannot be injected into init(). We instead
// exercise the connection-failure branch (no Meilisearch is running in the
// test sandbox), which disables the client and spawns the reconnect loop.
// ---------------------------------------------------------------------------

func TestTryConnect_RefusesDeadPort(t *testing.T) {
	client := &MeilisearchClient{
		host:    "http://127.0.0.1:1",
		apiKey:  "key",
		enabled: true,
	}
	assert.NotPanics(t, func() {
		client.tryConnect()
	})
	assert.False(t, client.IsEnabled())
}

func TestMeiliMode(t *testing.T) {
	original, had := config.C.Other["MEILISEARCH_ENABLED"]
	t.Cleanup(func() {
		if had {
			config.C.Other["MEILISEARCH_ENABLED"] = original
		} else {
			delete(config.C.Other, "MEILISEARCH_ENABLED")
		}
	})

	delete(config.C.Other, "MEILISEARCH_ENABLED")
	assert.Equal(t, "auto", meiliMode())

	config.C.Other["MEILISEARCH_ENABLED"] = "false"
	assert.Equal(t, "off", meiliMode())

	config.C.Other["MEILISEARCH_ENABLED"] = "true"
	assert.Equal(t, "on", meiliMode())

	config.C.Other["MEILISEARCH_ENABLED"] = "auto"
	assert.Equal(t, "auto", meiliMode())
}

// ---------------------------------------------------------------------------
// makeHits helper test
// ---------------------------------------------------------------------------

func TestMakeHits(t *testing.T) {
	hits := makeHits(
		map[string]string{"id": "1"},
		map[string]string{"id": "2"},
	)
	assert.Len(t, hits, 2)

	var doc struct {
		ID string `json:"id"`
	}
	err := hits[0].DecodeInto(&doc)
	require.NoError(t, err)
	assert.Equal(t, "1", doc.ID)
}

func TestSearchAccounts_HitDecodeErrorIgnored(t *testing.T) {
	// A hit with non-string "id" value that cannot decode into string.
	idx := &mockIndexManager{
		searchResp: &ms.SearchResponse{
			Hits: ms.Hits{
				ms.Hit{"id": []byte(`{"nested":true}`)}, // object can't decode into string
				ms.Hit{"id": []byte(`"5"`)},             // valid
			},
		},
	}
	svc := &mockServiceManager{idx: idx}
	client := newEnabledClient(svc)

	ids, err := client.SearchAccounts("test", 10, 0, nil)
	require.NoError(t, err)
	// Only the valid hit should be returned
	assert.Equal(t, []string{"5"}, ids)
}

// ---------------------------------------------------------------------------
// GetMeilisearchClient (with config tweaks)
// ---------------------------------------------------------------------------

func TestGetMeilisearchClient_ReturnsSingleton(t *testing.T) {
	// clientOnce already fired in earlier tests; verify it still returns the same instance
	c1 := GetMeilisearchClient()
	c2 := GetMeilisearchClient()
	assert.Same(t, c1, c2)
}
