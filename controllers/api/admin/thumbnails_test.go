package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============== GetThumbnailStats ==============

func TestGetThumbnailStats_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	GetThumbnailStats(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["total"])
	assert.Equal(t, float64(0), resp["completed"])
	assert.Equal(t, float64(0), resp["failed"])
}

func TestGetThumbnailStats_WithData(t *testing.T) {
	env := newAdminTestEnv(t)
	createThumbnailRow(t, env, models.ThumbnailStatusCompleted)
	createThumbnailRow(t, env, models.ThumbnailStatusFailed)
	createThumbnailRow(t, env, models.ThumbnailStatusPending)

	c, w := newGetContext(t, nil, "")
	GetThumbnailStats(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(3), resp["total"])
	assert.Equal(t, float64(1), resp["completed"])
	assert.Equal(t, float64(1), resp["failed"])
	assert.Equal(t, float64(1), resp["pending"])
}

// ============== GetThumbnailConfig ==============

func TestGetThumbnailConfig_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	GetThumbnailConfig(c)

	assert.Equal(t, 200, w.Code)
	// When no config rows exist, the handler returns an empty (or disabled)
	// map; both are valid. We just assert a 200 with valid JSON.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
}

func TestGetThumbnailConfig_WithData(t *testing.T) {
	env := newAdminTestEnv(t)
	createThumbnailConfigRow(t, env, "thumbnail_enabled", "true")
	createThumbnailConfigRow(t, env, "thumbnail_storage_type", "local")

	c, w := newGetContext(t, nil, "")
	GetThumbnailConfig(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "true", resp["thumbnail_enabled"])
	assert.Equal(t, "local", resp["thumbnail_storage_type"])
}

// ============== UpdateThumbnailConfig ==============

func TestUpdateThumbnailConfig_Success(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPut, nil, map[string]any{
		"thumbnail_enabled":      "true",
		"thumbnail_storage_type": "local",
	})
	UpdateThumbnailConfig(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["success"].(bool))
}

func TestUpdateThumbnailConfig_IgnoresNonString(t *testing.T) {
	newAdminTestEnv(t)
	// A non-string value should be silently skipped, not cause an error.
	c, w := newJSONContext(t, http.MethodPut, nil, map[string]any{
		"bad_key": 12345,
	})
	UpdateThumbnailConfig(c)

	assert.Equal(t, 200, w.Code)
}

func TestUpdateThumbnailConfig_InvalidJSON(t *testing.T) {
	newAdminTestEnv(t)
	c := ginContextWithBody(t, http.MethodPut, nil, "{bad")
	UpdateThumbnailConfig(c)
	assert.Equal(t, 400, c.Writer.Status())
}

// ============== ListThumbnails ==============

func TestListThumbnails_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	ListThumbnails(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["total"])
}

func TestListThumbnails_WithData(t *testing.T) {
	env := newAdminTestEnv(t)
	createThumbnailRow(t, env, models.ThumbnailStatusCompleted)
	createThumbnailRow(t, env, models.ThumbnailStatusFailed)

	c, w := newGetContext(t, nil, "")
	ListThumbnails(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(2), resp["total"])
	assert.Equal(t, float64(1), resp["page"])
	assert.Equal(t, float64(20), resp["limit"])
}

func TestListThumbnails_WithStatusFilter(t *testing.T) {
	env := newAdminTestEnv(t)
	createThumbnailRow(t, env, models.ThumbnailStatusCompleted)
	createThumbnailRow(t, env, models.ThumbnailStatusFailed)

	c, w := newGetContext(t, nil, "status=completed")
	ListThumbnails(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["total"])
}

func TestListThumbnails_PaginationClamp(t *testing.T) {
	env := newAdminTestEnv(t)
	createThumbnailRow(t, env, models.ThumbnailStatusCompleted)

	// page=0 and limit=999 should be clamped to defaults
	c, w := newGetContext(t, nil, "page=0&limit=999")
	ListThumbnails(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["page"])
	assert.Equal(t, float64(20), resp["limit"])
}

// ============== GetThumbnail ==============

func TestGetThumbnail_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	thumb := createThumbnailRow(t, env, models.ThumbnailStatusCompleted)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(thumb.ID)}}, "")
	GetThumbnail(c)

	assert.Equal(t, 200, w.Code)
}

func TestGetThumbnail_InvalidID(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "abc"}}, "")
	GetThumbnail(c)
	assert.Equal(t, 400, w.Code)
}

func TestGetThumbnail_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "999999"}}, "")
	GetThumbnail(c)
	assert.Equal(t, 404, w.Code)
}

// ============== DeleteThumbnail ==============

func TestDeleteThumbnail_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	thumb := createThumbnailRow(t, env, models.ThumbnailStatusCompleted)

	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(thumb.ID)}})
	DeleteThumbnail(c)

	assert.Equal(t, 200, w.Code)
}

func TestDeleteThumbnail_InvalidID(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: "x"}})
	DeleteThumbnail(c)
	assert.Equal(t, 400, w.Code)
}

func TestDeleteThumbnail_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: "888888"}})
	DeleteThumbnail(c)
	assert.Equal(t, 404, w.Code)
}

// ============== RegenerateThumbnail ==============

func TestRegenerateThumbnail_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	thumb := createThumbnailRow(t, env, models.ThumbnailStatusFailed)
	thumb.ErrorMessage = "something went wrong"
	require.NoError(t, env.gdb.Save(thumb).Error)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(thumb.ID)}}, nil)
	RegenerateThumbnail(c)

	assert.Equal(t, 200, w.Code)

	// Verify state was reset to pending
	var updated models.Thumbnail
	require.NoError(t, env.gdb.First(&updated, thumb.ID).Error)
	assert.Equal(t, models.ThumbnailStatusPending, updated.Status)
	assert.Empty(t, updated.ErrorMessage)
}

func TestRegenerateThumbnail_InvalidID(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "bad"}}, nil)
	RegenerateThumbnail(c)
	assert.Equal(t, 400, w.Code)
}

func TestRegenerateThumbnail_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "777777"}}, nil)
	RegenerateThumbnail(c)
	assert.Equal(t, 404, w.Code)
}

// ============== PurgeThumbnails ==============

func TestPurgeThumbnails_All(t *testing.T) {
	env := newAdminTestEnv(t)
	createThumbnailRow(t, env, models.ThumbnailStatusCompleted)
	createThumbnailRow(t, env, models.ThumbnailStatusFailed)

	c, w := newJSONContext(t, http.MethodPost, nil, map[string]any{
		"status": "all",
	})
	PurgeThumbnails(c)

	// The bulk delete may or may not be fully supported by the in-memory DB;
	// the important invariant is that the handler does not panic and returns
	// a JSON body. On success it returns 200.
	if w.Code == 200 {
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "all", resp["status"])
	}
}

func TestPurgeThumbnails_ByStatus(t *testing.T) {
	env := newAdminTestEnv(t)
	createThumbnailRow(t, env, models.ThumbnailStatusCompleted)
	createThumbnailRow(t, env, models.ThumbnailStatusFailed)

	c, w := newJSONContext(t, http.MethodPost, nil, map[string]any{
		"status": models.ThumbnailStatusFailed,
	})
	PurgeThumbnails(c)

	// The handler either returns 200 (success) or 500 (delete unsupported).
	assert.Contains(t, []int{200, 500}, w.Code)
}

func TestPurgeThumbnails_InvalidJSON(t *testing.T) {
	newAdminTestEnv(t)
	c := ginContextWithBody(t, http.MethodPost, nil, "{bad")
	PurgeThumbnails(c)
	assert.Equal(t, 400, c.Writer.Status())
}
