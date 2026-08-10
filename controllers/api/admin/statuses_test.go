package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============== serializeAdminStatus ==============

func TestSerializeAdminStatus_NilAccount(t *testing.T) {
	now := parseTimeRFC3339(t, "2024-05-06T07:08:09Z")
	status := &models.Status{
		Id:         10,
		AccountId:  1,
		Sensitive:  false,
		Visibility: uint8(models.StatusVisibilityPublic),
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}

	result := serializeAdminStatus(status)

	assert.Equal(t, "10", result["id"])
	assert.Equal(t, uint8(models.StatusVisibilityPublic), result["visibility"])
	assert.Nil(t, result["uri"]) // OriginUri == nil
	assert.Nil(t, result["url"])
	assert.Equal(t, gin.H{}, result["account"].(gin.H))
}

func TestSerializeAdminStatus_WithOriginURIs(t *testing.T) {
	status := &models.Status{Id: 1}
	uri := "https://remote.social/users/x/statuses/1"
	url := "https://remote.social/@x/1"
	status.OriginUri = &uri
	status.OriginUrl = &url

	result := serializeAdminStatus(status)
	assert.Equal(t, uri, result["uri"])
	assert.Equal(t, url, result["url"])
}

func TestSerializeAdminStatusAccount(t *testing.T) {
	// nil account
	assert.Equal(t, gin.H{}, serializeAdminStatusAccount(nil))

	// local account
	account := &models.Account{Id: 3, Username: "u", DisplayName: "U"}
	account.Domain = nil
	result := serializeAdminStatusAccount(account)
	assert.Equal(t, "3", result["id"])
	assert.Nil(t, result["domain"])
}

func TestSerializeAdminStatusAccount_Remote(t *testing.T) {
	domain := "r.social"
	account := &models.Account{Id: 4, Username: "r", IsBan: true, Locked: true}
	account.Domain = &domain

	result := serializeAdminStatusAccount(account)
	assert.Equal(t, "r.social", result["domain"])
	assert.True(t, result["suspended"].(bool))
}

// ============== GetStatus ==============

func TestGetStatus_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "statowner")
	status := createStatusRow(t, env, account)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(status.Id)}}, "")
	GetStatus(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, uitoa(status.Id), resp["id"])
}

func TestGetStatus_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "999999"}}, "")
	GetStatus(c)
	assert.Equal(t, 404, w.Code)
}

// ============== PerformStatusAction ==============

func TestPerformStatusAction_MarkSensitive(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "sens")
	status := createStatusRow(t, env, account)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(status.Id)}}, StatusActionRequest{
		Type: "mark_as_sensitive",
	})
	PerformStatusAction(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["sensitive"].(bool))
}

func TestPerformStatusAction_MarkNotSensitive(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "nosens")
	status := createStatusRow(t, env, account)
	status.Sensitive = true
	require.NoError(t, env.gdb.Save(status).Error)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(status.Id)}}, StatusActionRequest{
		Type: "mark_as_not_sensitive",
	})
	PerformStatusAction(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["sensitive"].(bool))
}

func TestPerformStatusAction_Delete(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "delact")
	status := createStatusRow(t, env, account)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(status.Id)}}, StatusActionRequest{
		Type: "delete",
	})
	PerformStatusAction(c)

	assert.Equal(t, 200, w.Code)
}

func TestPerformStatusAction_UnknownType(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "unk")
	status := createStatusRow(t, env, account)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(status.Id)}}, StatusActionRequest{
		Type: "unknown_action",
	})
	PerformStatusAction(c)

	// Unknown type is a no-op; returns the serialized status.
	assert.Equal(t, 200, w.Code)
}

func TestPerformStatusAction_InvalidJSON(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "inv")
	status := createStatusRow(t, env, account)

	c := ginContextWithBody(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(status.Id)}}, "{bad")
	PerformStatusAction(c)
	assert.Equal(t, 400, c.Writer.Status())
}

func TestPerformStatusAction_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "888888"}}, StatusActionRequest{Type: "delete"})
	PerformStatusAction(c)
	assert.Equal(t, 404, w.Code)
}

// ============== DeleteStatus ==============

func TestDeleteStatus_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "del")
	status := createStatusRow(t, env, account)

	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(status.Id)}})
	DeleteStatus(c)

	assert.Equal(t, 200, w.Code)
}

func TestDeleteStatus_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: "777777"}})
	DeleteStatus(c)
	assert.Equal(t, 404, w.Code)
}

// ============== Edge: status with reply/reblog fields ==============

func TestSerializeAdminStatus_WithCounts(t *testing.T) {
	status := &models.Status{
		Id:              100,
		RepliesCount:    5,
		ReblogsCount:    3,
		FavouritesCount: 10,
		Sensitive:       true,
		Language:        "en",
	}

	result := serializeAdminStatus(status)
	assert.Equal(t, uint64(5), result["replies_count"])
	assert.Equal(t, uint64(3), result["reblogs_count"])
	assert.Equal(t, uint64(10), result["favourites_count"])
	assert.True(t, result["sensitive"].(bool))
	assert.Equal(t, "en", result["language"])
}

// verify that GetStatus includes the nested account object
func TestGetStatus_IncludesAccount(t *testing.T) {
	env := newAdminTestEnv(t)
	account := createLocalAccount(t, env, "acctincl")
	status := createStatusRow(t, env, account)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(status.Id)}}, "")
	GetStatus(c)

	require.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	acct, ok := resp["account"].(map[string]any)
	require.True(t, ok, "account should be present")
	assert.Equal(t, "acctincl", acct["username"])
}

// ensure a status created_at serialization works
func TestSerializeAdminStatus_NilTimes(t *testing.T) {
	status := &models.Status{Id: 1}
	result := serializeAdminStatus(status)
	assert.Nil(t, result["created_at"])
	assert.Nil(t, result["updated_at"])
}

// quick sanity check of time parsing used elsewhere
func TestParseTimeRFC3339(t *testing.T) {
	tt := parseTimeRFC3339(t, "2024-01-01T00:00:00Z")
	assert.Equal(t, 2024, tt.Year())
	_ = time.Now() // keep import alive if other tests removed
}
