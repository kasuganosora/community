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

func TestRemoveAccountsDeletesEveryRequestedMember(t *testing.T) {
	setupTestDB(t)
	owner := createTestAccount(t, createTestUser(t).Id)
	list := createTestList(t, owner.Id)
	other := createTestList(t, owner.Id)
	one := createTestAccount(t, createTestUser(t).Id)
	two := createTestAccount(t, createTestUser(t).Id)
	for _, row := range []models.ListAccount{
		{ListId: list.Id, AccountId: one.Id}, {ListId: list.Id, AccountId: two.Id},
		{ListId: other.Id, AccountId: two.Id},
	} {
		require.NoError(t, testDB.Create(&row).Error)
	}
	listID := strconv.FormatUint(list.Id, 10)
	path := "/api/v1/lists/" + listID + "/accounts?account_ids[]=" + strconv.FormatUint(one.Id, 10) + "&account_ids[]=" + strconv.FormatUint(two.Id, 10)
	for range 2 {
		c, w := newContext(t, http.MethodDelete, path, nil, owner)
		setParams(c, gin.Params{{Key: "id", Value: listID}})
		callHandler(RemoveAccountFromList, c)
		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, "{}", w.Body.String())
	}
	var remaining []models.ListAccount
	require.NoError(t, testDB.Find(&remaining).Error)
	require.Len(t, remaining, 1)
	assert.Equal(t, other.Id, remaining[0].ListId)
	assert.Equal(t, two.Id, remaining[0].AccountId)
}
