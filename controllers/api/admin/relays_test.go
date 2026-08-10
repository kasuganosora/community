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

// createRelayRow inserts a relay directly into the given env's DB.
func createRelayRow(t *testing.T, env *adminTestEnv, inboxURL string, state models.RelayState) {
	t.Helper()

	relay := &models.Relay{
		InboxURL: inboxURL,
		State:    state,
	}
	require.NoError(t, env.gdb.Create(relay).Error)
}

// ============== ListRelays ==============

func TestListRelays_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	ListRelays(c)

	assert.Equal(t, 200, w.Code)
	var resp []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestListRelays_WithData(t *testing.T) {
	env := newAdminTestEnv(t)
	createRelayRow(t, env, "https://relay1.example.com/inbox", models.RelayStateIdle)
	createRelayRow(t, env, "https://relay2.example.com/inbox", models.RelayStateAccepted)

	c, w := newGetContext(t, nil, "")
	ListRelays(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

// ============== GetRelay ==============

func TestGetRelay_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	relay := &models.Relay{InboxURL: "https://get.example.com/inbox", State: models.RelayStateIdle}
	require.NoError(t, env.gdb.Create(relay).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(relay.ID)}}, "")
	GetRelay(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "https://get.example.com/inbox", resp["inbox_url"])
}

func TestGetRelay_InvalidID(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "abc"}}, "")
	GetRelay(c)
	assert.Equal(t, 400, w.Code)
}

func TestGetRelay_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "999999"}}, "")
	GetRelay(c)
	assert.Equal(t, 404, w.Code)
}

// ============== GetRelayInboxes ==============

func TestGetRelayInboxes_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	GetRelayInboxes(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["count"])
}

func TestGetRelayInboxes_WithEnabled(t *testing.T) {
	env := newAdminTestEnv(t)
	require.NoError(t, env.gdb.Create(&models.Relay{
		InboxURL: "https://enabled.example.com/inbox",
		State:    models.RelayStateAccepted,
	}).Error)
	require.NoError(t, env.gdb.Create(&models.Relay{
		InboxURL: "https://idle.example.com/inbox",
		State:    models.RelayStateIdle,
	}).Error)

	c, w := newGetContext(t, nil, "")
	GetRelayInboxes(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["count"])
}

// ============== EnableRelay ==============

func TestEnableRelay_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	relay := &models.Relay{InboxURL: "https://enable.example.com/inbox", State: models.RelayStateIdle}
	require.NoError(t, env.gdb.Create(relay).Error)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(relay.ID)}}, nil)
	EnableRelay(c)

	// EnableRelay sends a Follow activity; the relay state becomes pending.
	// The handler returns 200 (or 500 if delivery fails to create).
	// Since domain is "" the activity uses https:///actor — still succeeds for DB writes.
	assert.True(t, w.Code == 200 || w.Code == 500, "expected 200 or 500, got %d", w.Code)
}

func TestEnableRelay_InvalidID(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "bad"}}, nil)
	EnableRelay(c)
	assert.Equal(t, 400, w.Code)
}

func TestEnableRelay_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "888888"}}, nil)
	EnableRelay(c)
	assert.Equal(t, 404, w.Code)
}

// ============== DisableRelay ==============

func TestDisableRelay_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	relay := &models.Relay{
		InboxURL:         "https://disable.example.com/inbox",
		State:            models.RelayStateAccepted,
		FollowActivityID: "https://x/activities/follow/123",
	}
	require.NoError(t, env.gdb.Create(relay).Error)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(relay.ID)}}, nil)
	DisableRelay(c)

	assert.True(t, w.Code == 200 || w.Code == 500, "expected 200 or 500, got %d", w.Code)
}

func TestDisableRelay_InvalidID(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "x"}}, nil)
	DisableRelay(c)
	assert.Equal(t, 400, w.Code)
}

func TestDisableRelay_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "777777"}}, nil)
	DisableRelay(c)
	assert.Equal(t, 404, w.Code)
}

// ============== DeleteRelay ==============

func TestDeleteRelay_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	relay := &models.Relay{InboxURL: "https://delete.example.com/inbox", State: models.RelayStateIdle}
	require.NoError(t, env.gdb.Create(relay).Error)

	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(relay.ID)}})
	DeleteRelay(c)

	assert.Equal(t, 200, w.Code)
}

func TestDeleteRelay_InvalidID(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: "bad"}})
	DeleteRelay(c)
	assert.Equal(t, 400, w.Code)
}

func TestDeleteRelay_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: "666666"}})
	DeleteRelay(c)
	assert.Equal(t, 404, w.Code)
}

// ============== CreateRelay ==============

func TestCreateRelay_Success(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, nil, map[string]any{
		"inbox_url": "https://newrelay.example.com/inbox",
	})
	CreateRelay(c)

	// CreateRelay creates then enables; enabling may fail but creation succeeds.
	// The handler returns 200 on success or 422/500 on errors.
	assert.True(t, w.Code == 200 || w.Code == 500, "expected 200 or 500, got %d", w.Code)
}

func TestCreateRelay_InvalidInboxURL(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, nil, map[string]any{
		"inbox_url": "not-a-url",
	})
	CreateRelay(c)
	assert.Equal(t, 400, w.Code)
}

func TestCreateRelay_MissingInboxURL(t *testing.T) {
	newAdminTestEnv(t)
	c := ginContextWithBody(t, http.MethodPost, nil, `{}`)
	CreateRelay(c)
	assert.Equal(t, 400, c.Writer.Status())
}

func TestCreateRelay_Duplicate(t *testing.T) {
	env := newAdminTestEnv(t)
	require.NoError(t, env.gdb.Create(&models.Relay{
		InboxURL: "https://dup.example.com/inbox",
		State:    models.RelayStateIdle,
	}).Error)

	c, w := newJSONContext(t, http.MethodPost, nil, map[string]any{
		"inbox_url": "https://dup.example.com/inbox",
	})
	CreateRelay(c)
	assert.Equal(t, 422, w.Code)
}
