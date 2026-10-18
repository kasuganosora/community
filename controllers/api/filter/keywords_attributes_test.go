package filter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeFilter(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return out
}

func keywordTexts(resp map[string]any) []string {
	out := []string{}
	for _, raw := range resp["keywords"].([]any) { //nolint:forcetypeassert
		out = append(out, raw.(map[string]any)["keyword"].(string)) //nolint:forcetypeassert
	}

	return out
}

func TestCreate_KeywordsAttributesDedupesAndTrims(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "multi")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = jsonRequest(t, http.MethodPost, "/filters", map[string]any{
		"title":   "words",
		"context": []string{"home"},
		"keywords_attributes": []map[string]any{
			{"keyword": " spam "}, {"keyword": "eggs", "whole_word": true}, {"keyword": "spam"},
		},
	})
	Create(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{"spam", "eggs"}, keywordTexts(decodeFilter(t, w)))
}

func TestCreate_ErrorCodes(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "codes")
	cases := []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"title": "x", "context": []string{"home"}}, ErrCodeFilterKeywordsRequired},
		{map[string]any{"title": "x", "context": []string{"home"}, "keywords_attributes": []map[string]any{{"keyword": "  "}}}, ErrCodeFilterKeywordEmpty},
		{map[string]any{"title": "x", "context": []string{"nowhere"}, "keywords_attributes": []map[string]any{{"keyword": "a"}}}, ErrCodeFilterContextUnsupported},
		{map[string]any{"title": "x", "context": []string{"home"}, "filter_action": "explode", "keywords_attributes": []map[string]any{{"keyword": "a"}}}, ErrCodeFilterActionUnsupported},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("account", account)
		c.Request = jsonRequest(t, http.MethodPost, "/filters", tc.body)
		Create(c)
		assert.Equal(t, 422, w.Code)
		assert.Equal(t, tc.code, decodeFilter(t, w)["code"])
	}
}

func TestUpdate_KeywordsAttributesDiff(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "differ")
	filter := createTestFilter(t, account.Id, "f", []*models.FilterKeyword{
		{Uuid: "u-1", Keyword: "keep"}, {Uuid: "u-2", Keyword: "drop"},
	})
	id := strconv.FormatUint(filter.Id, 10)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+id, map[string]any{
		"keywords_attributes": []map[string]any{
			{"id": id + "-u-2", "_destroy": "1"},
			{"keyword": "new"},
			{"keyword": "keep"}, // already present: no duplicate
		},
	})
	Update(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{"keep", "new"}, keywordTexts(decodeFilter(t, w)))

	var stored models.Filter
	require.NoError(t, testDB.GormDB.First(&stored, filter.Id).Error)
	require.Len(t, stored.Keywords, 2)
	assert.Equal(t, "u-1", stored.Keywords[0].Uuid, "kept keywords keep their id")

	// Removing every keyword is refused.
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+id, map[string]any{
		"keywords_attributes": []map[string]any{
			{"id": id + "-u-1", "_destroy": true},
			{"id": id + "-" + stored.Keywords[1].Uuid, "_destroy": true},
		},
	})
	Update(c)
	assert.Equal(t, 422, w.Code)
	assert.Equal(t, ErrCodeFilterKeywordsRequired, decodeFilter(t, w)["code"])

	// Unknown keyword id.
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+id, map[string]any{
		"keywords_attributes": []map[string]any{{"id": id + "-nope", "_destroy": true}},
	})
	Update(c)
	assert.Equal(t, 404, w.Code)
	assert.Equal(t, ErrCodeFilterKeywordNotFound, decodeFilter(t, w)["code"])
}
