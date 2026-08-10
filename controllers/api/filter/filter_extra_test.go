package filter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestFilter creates a filter owned by accountId with the given keywords.
func createTestFilter(t *testing.T, accountId uint64, title string, keywords []*models.FilterKeyword) *models.Filter {
	t.Helper()
	if keywords == nil {
		keywords = []*models.FilterKeyword{{Uuid: "kw-uuid-1", Keyword: "spam", WholeWord: true}}
	}
	filter := &models.Filter{
		Title:        title,
		Context:      []string{"home", "public"},
		AccountId:    accountId,
		FilterAction: models.FilterActionWarn,
		Keywords:     keywords,
	}
	require.NoError(t, testDB.GormDB.Create(filter).Error)
	return filter
}

// jsonRequest builds an *http.Request from a value that can be marshalled to JSON.
func jsonRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	var reader *bytes.Buffer
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewBuffer(raw)
	} else {
		reader = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, target, reader) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	return req
}

// ----------------- Create handler tests -----------------

func TestCreate_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "creator")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = jsonRequest(t, http.MethodPost, "/filters", CreateFilterRequest{
		Title:     "My Filter",
		Context:   []string{"home"},
		ExpiresIn: 3600,
		Keywords: []*models.FilterKeyword{
			{Keyword: "badword", WholeWord: true},
		},
	})

	Create(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "My Filter", resp["title"])
	assert.NotEmpty(t, resp["id"])
	assert.NotNil(t, resp["expires_at"])
	kws, ok := resp["keywords"].([]any)
	require.True(t, ok)
	require.Len(t, kws, 1)
	// Uuid should have been assigned by the handler (json:"-" so it appears in id only)
	idStr, _ := resp["id"].(string)
	assert.NotEmpty(t, idStr)

	// Verify it is persisted. NOTE: FilterKeyword.Uuid has json:"-" so it is
	// stripped by the json serializer on save; only the keyword text/value survive.
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, account.Id).Error)
	assert.Equal(t, "My Filter", got.Title)
	require.Len(t, got.Keywords, 1)
	assert.Equal(t, "badword", got.Keywords[0].Keyword)
	assert.True(t, got.Keywords[0].WholeWord)
}

func TestCreate_NoExpiry(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "creator2")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = jsonRequest(t, http.MethodPost, "/filters", CreateFilterRequest{
		Title:    "NoExpiry",
		Context:  []string{"notifications"},
		Keywords: []*models.FilterKeyword{{Keyword: "x", WholeWord: false}},
	})

	Create(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got).Error)
	assert.Nil(t, got.ExpiresAt)
}

func TestCreate_MissingFields(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "creator3")

	// Missing required title, context, keywords
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = jsonRequest(t, http.MethodPost, "/filters", map[string]any{})

	Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreate_InvalidJSON(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "creator4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = httptest.NewRequest(http.MethodPost, "/filters", bytes.NewBufferString("not-json")) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ----------------- List handler tests -----------------

func TestList_Empty(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "lister")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = httptest.NewRequest(http.MethodGet, "/filters", nil) //nolint:noctx

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestList_WithFilters(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "lister2")
	createTestFilter(t, account.Id, "Filter One", nil)
	createTestFilter(t, account.Id, "Filter Two", []*models.FilterKeyword{
		{Uuid: "u-a", Keyword: "a", WholeWord: true},
		{Uuid: "u-b", Keyword: "b", WholeWord: false},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = httptest.NewRequest(http.MethodGet, "/filters", nil) //nolint:noctx

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

func TestList_ReturnsOwnFilter(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "owner")
	createTestFilter(t, account.Id, "My filter", nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Request = httptest.NewRequest(http.MethodGet, "/filters", nil) //nolint:noctx

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// The account's own filter must be present in the result.
	require.NotEmpty(t, resp)
	foundOwn := false
	for _, r := range resp {
		if r["title"] == "My filter" {
			foundOwn = true
		}
	}
	assert.True(t, foundOwn, "account's own filter should be returned by List")
}

// ----------------- Show handler tests -----------------

func TestShow_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "shower")
	filter := createTestFilter(t, account.Id, "Show me", nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/"+strconv.FormatUint(filter.Id, 10), nil) //nolint:noctx

	Show(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Show me", resp["title"])
	assert.Equal(t, strconv.FormatUint(filter.Id, 10), resp["id"])
}

func TestShow_NotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "shower2")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/999999", nil) //nolint:noctx

	Show(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestShow_OtherAccountFilter(t *testing.T) {
	setupTestDB(t)
	accountA := createTestAccount(t, "ownerA")
	accountB := createTestAccount(t, "ownerB")
	filter := createTestFilter(t, accountA.Id, "private", nil)

	// accountB should NOT be able to see accountA's filter
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", accountB)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/"+strconv.FormatUint(filter.Id, 10), nil) //nolint:noctx

	Show(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ----------------- Delete handler tests -----------------

func TestDelete_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "deleter")
	filter := createTestFilter(t, account.Id, "Delete me", nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/filters/"+strconv.FormatUint(filter.Id, 10), nil) //nolint:noctx

	Delete(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Verify deletion
	var got models.Filter
	err := testDB.GormDB.First(&got, filter.Id).Error
	require.Error(t, err)
}

func TestDelete_NotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "deleter2")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/filters/999999", nil) //nolint:noctx

	Delete(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ----------------- Update handler tests -----------------

func TestUpdate_Title(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater")
	filter := createTestFilter(t, account.Id, "Original", nil)

	newTitle := "Updated Title"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+strconv.FormatUint(filter.Id, 10), UpdateFilterRequest{
		Title: &newTitle,
	})

	Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Updated Title", resp["title"])
}

func TestUpdate_Context(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater2")
	filter := createTestFilter(t, account.Id, "CtxFilter", nil)

	newContext := []string{"notifications", "public"}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+strconv.FormatUint(filter.Id, 10), UpdateFilterRequest{
		Context: &newContext,
	})

	Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	ctx, ok := resp["context"].([]any)
	require.True(t, ok)
	assert.Len(t, ctx, 2)
}

func TestUpdate_ExpiresInZeroClearsExpiry(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater3")
	// Filter with an expiry
	expiresAt := time.Now().Add(1 * time.Hour)
	filter := &models.Filter{
		Title:     "Expiring",
		Context:   []string{"home"},
		AccountId: account.Id,
		ExpiresAt: &expiresAt,
		Keywords:  []*models.FilterKeyword{{Uuid: "u", Keyword: "x", WholeWord: true}},
	}
	require.NoError(t, testDB.GormDB.Create(filter).Error)
	require.NotNil(t, filter.ExpiresAt)

	zero := uint64(0)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+strconv.FormatUint(filter.Id, 10), UpdateFilterRequest{
		ExpiresIn: &zero,
	})

	Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Verify expiry was cleared in the DB
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	assert.Nil(t, got.ExpiresAt)
}

func TestUpdate_ExpiresInNonZero(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater4")
	filter := createTestFilter(t, account.Id, "NoExp", nil)
	require.Nil(t, filter.ExpiresAt)

	expiresIn := uint64(7200)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+strconv.FormatUint(filter.Id, 10), UpdateFilterRequest{
		ExpiresIn: &expiresIn,
	})

	Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	require.NotNil(t, got.ExpiresAt)
}

func TestUpdate_Keywords(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater5")
	filter := createTestFilter(t, account.Id, "KwFilter",
		[]*models.FilterKeyword{{Uuid: "keep-uuid", Keyword: "keep", WholeWord: true}})

	newKeywords := []*models.FilterKeyword{
		{Keyword: "keep", WholeWord: false}, // existing keyword text
		{Keyword: "added", WholeWord: true}, // new keyword
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+strconv.FormatUint(filter.Id, 10), UpdateFilterRequest{
		Keywords: &newKeywords,
	})

	Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// The response should reflect the updated keyword set (both "keep" and "added").
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	kws, ok := resp["keywords"].([]any)
	require.True(t, ok)
	require.Len(t, kws, 2)
	byText := map[string]any{}
	for _, k := range kws {
		km := k.(map[string]any)
		byText[km["keyword"].(string)] = km
	}
	_, hasKeep := byText["keep"]
	_, hasAdded := byText["added"]
	assert.True(t, hasKeep)
	assert.True(t, hasAdded)
	// "keep" should have its WholeWord updated to false.
	assert.Equal(t, false, byText["keep"].(map[string]any)["whole_word"])

	// Verify persisted: two keywords survive a DB round-trip with updated values.
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	require.Len(t, got.Keywords, 2)
}

func TestUpdate_NotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater6")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/999999", UpdateFilterRequest{})

	Update(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdate_InvalidJSON(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater7")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/filters/1", bytes.NewBufferString("not-json")) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	Update(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdate_EmptyBodyKeepsValues(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "updater8")
	filter := createTestFilter(t, account.Id, "Untouched", nil)

	// Send an empty UpdateFilterRequest (all nil) -> values should be unchanged
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("account", account)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = jsonRequest(t, http.MethodPut, "/filters/"+strconv.FormatUint(filter.Id, 10), UpdateFilterRequest{})

	Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	assert.Equal(t, "Untouched", got.Title)
}

// ----------------- KeywordsList handler tests -----------------

func TestKeywordsList_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwlister")
	filter := createTestFilter(t, account.Id, "KwList",
		[]*models.FilterKeyword{
			{Uuid: "u1", Keyword: "alpha", WholeWord: true},
			{Uuid: "u2", Keyword: "beta", WholeWord: false},
		})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/"+strconv.FormatUint(filter.Id, 10)+"/keywords", nil) //nolint:noctx

	KeywordsList(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

func TestKeywordsList_InvalidID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwlister2")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "not-a-number"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/x/keywords", nil) //nolint:noctx

	KeywordsList(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordsList_ZeroID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwlister3")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "0"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/0/keywords", nil) //nolint:noctx

	KeywordsList(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordsList_NotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwlister4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/999999/keywords", nil) //nolint:noctx

	KeywordsList(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ----------------- KeywordCreate handler tests -----------------

func TestKeywordCreate_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwcreator")
	filter := createTestFilter(t, account.Id, "KwCreate", []*models.FilterKeyword{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "filter_id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = jsonRequest(t, http.MethodPost, "/x", CreateKeywordRequest{
		FilterKeyword: models.FilterKeyword{Keyword: "newkeyword", WholeWord: true},
	})

	KeywordCreate(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "newkeyword", resp["keyword"])
	assert.Equal(t, true, resp["whole_word"])
	assert.NotEmpty(t, resp["id"])

	// Verify persisted (Uuid has json:"-" so it is not persisted, but the
	// keyword text/whole_word must survive the round-trip).
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	require.Len(t, got.Keywords, 1)
	assert.Equal(t, "newkeyword", got.Keywords[0].Keyword)
	assert.True(t, got.Keywords[0].WholeWord)
}

func TestKeywordCreate_InvalidFilterID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwcreator2")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "filter_id", Value: "abc"}}
	c.Request = jsonRequest(t, http.MethodPost, "/x", CreateKeywordRequest{})

	KeywordCreate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordCreate_ZeroFilterID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwcreator3")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "filter_id", Value: "0"}}
	c.Request = jsonRequest(t, http.MethodPost, "/x", CreateKeywordRequest{})

	KeywordCreate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordCreate_FilterNotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwcreator4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "filter_id", Value: "999999"}}
	c.Request = jsonRequest(t, http.MethodPost, "/x", CreateKeywordRequest{
		FilterKeyword: models.FilterKeyword{Keyword: "x"},
	})

	KeywordCreate(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestKeywordCreate_InvalidJSON(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwcreator5")
	filter := createTestFilter(t, account.Id, "KwCreateBad", nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "filter_id", Value: strconv.FormatUint(filter.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString("not-json")) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	KeywordCreate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ----------------- KeywordView handler tests -----------------

func TestKeywordView_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwviewer")
	// Keyword lookup matches on the keyword *text*, so the URL id's second
	// segment must be the keyword value.
	filter := createTestFilter(t, account.Id, "KwView",
		[]*models.FilterKeyword{{Uuid: "view-uuid", Keyword: "seeme", WholeWord: true}})

	keywordID := strconv.FormatUint(filter.Id, 10) + "-seeme"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: keywordID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil) //nolint:noctx

	KeywordView(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "seeme", resp["keyword"])
}

func TestKeywordView_InvalidID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwviewer2")

	// No '-' separator
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "invalid"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil) //nolint:noctx

	KeywordView(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordView_BadFilterID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwviewer3")

	// filter id part is not a number
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "abc-keyword"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil) //nolint:noctx

	KeywordView(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordView_FilterNotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwviewer4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "999999-somekeyword"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil) //nolint:noctx

	KeywordView(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestKeywordView_KeywordNotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwviewer5")
	filter := createTestFilter(t, account.Id, "KwViewMissing",
		[]*models.FilterKeyword{{Uuid: "exists", Keyword: "exists", WholeWord: true}})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10) + "-missing"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil) //nolint:noctx

	KeywordView(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestKeywordView_KeywordWithDash(t *testing.T) {
	// The keyword id itself can contain dashes; the parser splits on '-' and
	// rejoins the rest. Verify a keyword whose value contains a dash resolves.
	setupTestDB(t)
	account := createTestAccount(t, "kwviewer6")
	filter := createTestFilter(t, account.Id, "DashKw",
		[]*models.FilterKeyword{{Uuid: "dash-uuid", Keyword: "has-dash", WholeWord: false}})

	keywordID := strconv.FormatUint(filter.Id, 10) + "-has-dash"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: keywordID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil) //nolint:noctx

	KeywordView(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "has-dash", resp["keyword"])
}

// ----------------- KeywordUpdate handler tests -----------------

func TestKeywordUpdate_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwupdater")
	filter := createTestFilter(t, account.Id, "KwUpdate",
		[]*models.FilterKeyword{{Uuid: "upd-uuid", Keyword: "orig", WholeWord: false}})

	keywordID := strconv.FormatUint(filter.Id, 10) + "-orig"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: keywordID}}
	c.Request = jsonRequest(t, http.MethodPut, "/x", KeywordUpdateRequest{
		FilterKeyword: models.FilterKeyword{Keyword: "changed", WholeWord: true},
	})

	KeywordUpdate(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "changed", resp["keyword"])
	assert.Equal(t, true, resp["whole_word"])

	// Verify persisted
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	require.Len(t, got.Keywords, 1)
	assert.Equal(t, "changed", got.Keywords[0].Keyword)
	assert.True(t, got.Keywords[0].WholeWord)
}

func TestKeywordUpdate_InvalidID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwupdater2")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "nodash"}}
	c.Request = jsonRequest(t, http.MethodPut, "/x", KeywordUpdateRequest{})

	KeywordUpdate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordUpdate_BadFilterID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwupdater3")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "xx-kw"}}
	c.Request = jsonRequest(t, http.MethodPut, "/x", KeywordUpdateRequest{})

	KeywordUpdate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordUpdate_FilterNotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwupdater4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "999999-kw"}}
	c.Request = jsonRequest(t, http.MethodPut, "/x", KeywordUpdateRequest{})

	KeywordUpdate(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestKeywordUpdate_KeywordNotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwupdater5")
	filter := createTestFilter(t, account.Id, "KwUpdateMissing",
		[]*models.FilterKeyword{{Uuid: "x", Keyword: "present", WholeWord: true}})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10) + "-absent"}}
	c.Request = jsonRequest(t, http.MethodPut, "/x", KeywordUpdateRequest{})

	KeywordUpdate(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestKeywordUpdate_InvalidJSON(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwupdater6")
	filter := createTestFilter(t, account.Id, "KwUpdateBad",
		[]*models.FilterKeyword{{Uuid: "u", Keyword: "kw", WholeWord: true}})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(filter.Id, 10) + "-kw"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/x", bytes.NewBufferString("not-json")) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	KeywordUpdate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ----------------- KeywordDelete handler tests -----------------

func TestKeywordDelete_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwdeleter")
	filter := createTestFilter(t, account.Id, "KwDelete",
		[]*models.FilterKeyword{
			{Uuid: "del-uuid", Keyword: "remove", WholeWord: true},
			{Uuid: "keep-uuid", Keyword: "keep", WholeWord: false},
		})

	keywordID := strconv.FormatUint(filter.Id, 10) + "-remove"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: keywordID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/x", nil) //nolint:noctx

	KeywordDelete(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "keep", resp[0]["keyword"])

	// Verify persisted: only one keyword remains
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	require.Len(t, got.Keywords, 1)
	assert.Equal(t, "keep", got.Keywords[0].Keyword)
}

func TestKeywordDelete_DeleteLastKeyword(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwdeleter2")
	filter := createTestFilter(t, account.Id, "KwDeleteLast",
		[]*models.FilterKeyword{{Uuid: "only", Keyword: "only", WholeWord: true}})

	keywordID := strconv.FormatUint(filter.Id, 10) + "-only"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: keywordID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/x", nil) //nolint:noctx

	KeywordDelete(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)

	// Verify persisted: no keywords remain
	var got models.Filter
	require.NoError(t, testDB.GormDB.First(&got, filter.Id).Error)
	assert.Empty(t, got.Keywords)
}

func TestKeywordDelete_InvalidID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwdeleter3")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "nodash"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/x", nil) //nolint:noctx

	KeywordDelete(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordDelete_BadFilterID(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwdeleter4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "abc-kw"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/x", nil) //nolint:noctx

	KeywordDelete(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKeywordDelete_FilterNotFound(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwdeleter5")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: "999999-kw"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/x", nil) //nolint:noctx

	KeywordDelete(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestKeywordDelete_NonExistentKeywordIsNoOp(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "kwdeleter6")
	filter := createTestFilter(t, account.Id, "KwDeleteNoop",
		[]*models.FilterKeyword{{Uuid: "u", Keyword: "present", WholeWord: true}})

	// Deleting a keyword that does not exist returns 200 with the unchanged list.
	keywordID := strconv.FormatUint(filter.Id, 10) + "-absent"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", &models.User{Id: account.Id})
	c.Params = gin.Params{{Key: "id", Value: keywordID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/x", nil) //nolint:noctx

	KeywordDelete(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
}
