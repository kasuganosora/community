package list

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kasuganosora/community/models"
)

// Mastodon clients delete lists without a request body.
func TestDeleteList_NoBody(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id)

	c, w := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), nil, account)
	setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
	callHandler(DeleteList, c)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// Lists created without an explicit visibility (every Mastodon client) must be
// private, not public.
func TestCreateList_DefaultsToPrivate(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	body, _ := json.Marshal(map[string]string{"title": "friends-default"})
	c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
	callHandler(CreateList, c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var list models.List
	require.NoError(t, testDB.Where("title = ?", "friends-default").First(&list).Error)
	assert.Equal(t, models.ListVisibilityPrivate, list.Visibility)
}
