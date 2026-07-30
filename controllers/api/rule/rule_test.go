package rule

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// callHandler invokes a gin handler and recovers from panic("abort") used by AbortIf/AbortIfError.
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(controllers.AbortSentinel); !ok {
				panic(r)
			}
		}
	}()

	fn(c)
}

var testDB *testutil.TestDB

func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func setupTestDB(t *testing.T) {
	var err error

	testDB, err = testutil.NewIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}

	database.SetTestDB(testDB.GormDB)

	if err := testDB.AutoMigrate(&models.Rule{}); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
}

func createTestRule(t *testing.T, text string, priority uint) *models.Rule {
	rule := &models.Rule{
		Text:     text,
		Priority: priority,
	}
	err := testDB.GormDB.Create(rule).Error
	require.NoError(t, err)

	return rule
}

// --- CreateRequest Tests ---

func TestCreateRequest_Binding(t *testing.T) {
	tests := []struct {
		name        string
		body        CreateRequest
		expectError bool
	}{
		{
			name:        "valid request with priority",
			body:        CreateRequest{Text: "No spam", Priority: 1},
			expectError: false,
		},
		{
			name:        "valid request without priority (defaults to 0)",
			body:        CreateRequest{Text: "Be nice"},
			expectError: false,
		},
		{
			name:        "empty text should fail",
			body:        CreateRequest{Text: ""},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/rules", bytes.NewBuffer(body)) //nolint:noctx
			c.Request.Header.Set("Content-Type", "application/json")

			var parsed CreateRequest

			err := c.ShouldBindJSON(&parsed)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.body.Text, parsed.Text)
				assert.Equal(t, tt.body.Priority, parsed.Priority)
			}
		})
	}
}

// --- UpdateRequest Tests ---

func TestUpdateRequest_Binding(t *testing.T) {
	text := "Updated rule"
	priority := uint(5)

	t.Run("nil text field triggers min validation", func(t *testing.T) {
		req := UpdateRequest{}
		body, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/rules/1", bytes.NewBuffer(body)) //nolint:noctx
		c.Request.Header.Set("Content-Type", "application/json")

		var parsed UpdateRequest

		err := c.ShouldBindJSON(&parsed)
		require.Error(t, err)
		assert.Nil(t, parsed.Text)
		assert.Nil(t, parsed.Priority)
	})

	t.Run("text pointer binding", func(t *testing.T) {
		req := UpdateRequest{Text: &text}
		body, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/rules/1", bytes.NewBuffer(body)) //nolint:noctx
		c.Request.Header.Set("Content-Type", "application/json")

		var parsed UpdateRequest

		err := c.ShouldBindJSON(&parsed)
		require.NoError(t, err)
		assert.NotNil(t, parsed.Text)
		assert.Equal(t, "Updated rule", *parsed.Text)
	})

	t.Run("priority pointer binding", func(t *testing.T) {
		req := UpdateRequest{Text: &text, Priority: &priority}
		body, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/rules/1", bytes.NewBuffer(body)) //nolint:noctx
		c.Request.Header.Set("Content-Type", "application/json")

		var parsed UpdateRequest

		err := c.ShouldBindJSON(&parsed)
		require.NoError(t, err)
		assert.NotNil(t, parsed.Priority)
		assert.Equal(t, uint(5), *parsed.Priority)
	})

	t.Run("text too short should fail", func(t *testing.T) {
		emptyText := ""
		req := UpdateRequest{Text: &emptyText}
		body, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/rules/1", bytes.NewBuffer(body)) //nolint:noctx
		c.Request.Header.Set("Content-Type", "application/json")

		var parsed UpdateRequest

		err := c.ShouldBindJSON(&parsed)
		require.Error(t, err)
	})

	t.Run("text too long should fail", func(t *testing.T) {
		longText := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" +
			"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" +
			"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC" +
			"DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"
		req := UpdateRequest{Text: &longText}
		body, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/rules/1", bytes.NewBuffer(body)) //nolint:noctx
		c.Request.Header.Set("Content-Type", "application/json")

		var parsed UpdateRequest

		err := c.ShouldBindJSON(&parsed)
		require.Error(t, err)
	})
}

// --- Create Handler Tests ---

func TestCreate_Success(t *testing.T) {
	setupTestDB(t)

	body, err := json.Marshal(CreateRequest{Text: "No spam allowed", Priority: 1})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/rules", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Create, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "No spam allowed", response["text"])
	assert.NotEmpty(t, response["id"])
}

func TestCreate_MissingText(t *testing.T) {
	setupTestDB(t)

	body, _ := json.Marshal(map[string]any{"priority": 1}) //nolint:errchkjson
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/rules", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	assert.Panics(t, func() {
		Create(c)
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCreate_InvalidJSON(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/rules", bytes.NewBufferString("not json")) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	assert.Panics(t, func() {
		Create(c)
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// --- Delete Handler Tests ---

func TestDelete_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "invalid"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/rules/invalid", nil) //nolint:noctx

	assert.Panics(t, func() {
		Delete(c)
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDelete_NotFound(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "99999"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/rules/99999", nil) //nolint:noctx

	assert.Panics(t, func() {
		Delete(c)
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDelete_Success(t *testing.T) {
	setupTestDB(t)

	rule := createTestRule(t, "Delete me", 0)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(rule.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/rules/"+strconv.FormatUint(rule.Id, 10), nil) //nolint:noctx

	callHandler(Delete, c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify deletion
	var found models.Rule

	err := testDB.GormDB.First(&found, rule.Id).Error
	require.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}

// --- Get Handler Tests ---

func TestGet_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/rules/abc", nil) //nolint:noctx

	assert.Panics(t, func() {
		Get(c)
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGet_NotFound(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "99999"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/rules/99999", nil) //nolint:noctx

	assert.Panics(t, func() {
		Get(c)
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGet_Success(t *testing.T) {
	setupTestDB(t)

	rule := createTestRule(t, "Get this rule", 2)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(rule.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodGet, "/rules/"+strconv.FormatUint(rule.Id, 10), nil) //nolint:noctx

	callHandler(Get, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "Get this rule", response["text"])
	assert.Equal(t, strconv.FormatUint(rule.Id, 10), response["id"])
}

// --- List Handler Tests ---

func TestList_Empty(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/rules", nil) //nolint:noctx

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Empty(t, response)
}

func TestList_WithRules(t *testing.T) {
	setupTestDB(t)

	createTestRule(t, "Rule A", 2)
	createTestRule(t, "Rule B", 1)
	createTestRule(t, "Rule C", 3)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/rules", nil) //nolint:noctx

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Len(t, response, 3)

	// Rules should be ordered by priority DESC, created_at ASC
	assert.Equal(t, "Rule C", response[0]["text"])
	assert.Equal(t, "Rule A", response[1]["text"])
	assert.Equal(t, "Rule B", response[2]["text"])
}

// --- Update Handler Tests ---

func TestUpdate_InvalidID(t *testing.T) {
	setupTestDB(t)

	body, err := json.Marshal(UpdateRequest{})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "bad"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/rules/bad", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	assert.Panics(t, func() {
		Update(c)
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdate_NotFound(t *testing.T) {
	setupTestDB(t)

	body, err := json.Marshal(UpdateRequest{})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "99999"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/rules/99999", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	assert.Panics(t, func() {
		Update(c)
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdate_Text(t *testing.T) {
	setupTestDB(t)

	rule := createTestRule(t, "Original", 0)

	newText := "Updated text"
	body, err := json.Marshal(UpdateRequest{Text: &newText})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(rule.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodPut, "/rules/"+strconv.FormatUint(rule.Id, 10), bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "Updated text", response["text"])
}

func TestUpdate_Priority(t *testing.T) {
	setupTestDB(t)

	rule := createTestRule(t, "Priority test", 0)

	newPriority := uint(5)
	updateText := "Priority test"
	body, err2 := json.Marshal(UpdateRequest{Text: &updateText, Priority: &newPriority})
	w := httptest.NewRecorder()
	require.NoError(t, err2)
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(rule.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodPut, "/rules/"+strconv.FormatUint(rule.Id, 10), bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, strconv.FormatUint(rule.Id, 10), response["id"])
}

func TestUpdate_EmptyJSON(t *testing.T) {
	setupTestDB(t)

	rule := createTestRule(t, "Keep same", 3)

	body, err := json.Marshal(UpdateRequest{})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(rule.Id, 10)}}
	c.Request = httptest.NewRequest(http.MethodPut, "/rules/"+strconv.FormatUint(rule.Id, 10), bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var response map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
}
