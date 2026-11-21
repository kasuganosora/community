package filter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestLegacyFilterUsesKeywordIDsAndAccountOwnership(t *testing.T) {
	setupTestDB(t)
	owner := createTestAccount(t, "owner")
	visitor := createTestAccount(t, "visitor")
	group := &models.Filter{
		AccountId: owner.Id, Title: "Words", Context: []string{"home"}, FilterAction: models.FilterActionWarn,
		Keywords: []*models.FilterKeyword{{Keyword: "one"}, {Keyword: "two", WholeWord: true}},
	}
	require.NoError(t, testDB.GormDB.Create(group).Error)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		viewer := owner
		if c.Query("viewer") == "visitor" {
			viewer = visitor
		}
		c.Set("account", viewer)
	})
	router.GET("/filters", LegacyList)
	router.POST("/filters", LegacyCreate)
	router.GET("/filters/:id", LegacyShow)
	router.PUT("/filters/:id", LegacyUpdate)
	router.DELETE("/filters/:id", LegacyDelete)
	router.PUT("/keywords/:id", KeywordUpdate)
	call := func(method, path, body string, wantStatus int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, request)
		require.Equal(t, wantStatus, result.Code, result.Body.String())
		return result
	}
	var entries []legacyFilter
	require.NoError(t, json.Unmarshal(call(http.MethodGet, "/filters", "", 200).Body.Bytes(), &entries))
	require.Len(t, entries, 2)
	require.NotEqual(t, entries[0].Id, entries[1].Id)
	first := "/filters/" + entries[0].Id
	call(http.MethodGet, first+"?viewer=visitor", "", 404)
	call(http.MethodDelete, first+"?viewer=visitor", "", 404)
	// A v2 edit of a pre-UUID keyword must preserve its v1 URL and word matching.
	second := "/keywords/" + entries[1].Id
	call(http.MethodPut, second, `{"keyword":"renamed"}`, 200)
	var renamed legacyFilter
	require.NoError(t, json.Unmarshal(call(http.MethodGet, "/filters/"+entries[1].Id, "", 200).Body.Bytes(), &renamed))
	require.Equal(t, "renamed", renamed.Phrase)
	require.True(t, renamed.WholeWord)
	call(http.MethodPut, first, `{"context":["public"]}`, 422)
	call(http.MethodPut, first, `{"context":["home"],"whole_word":true,"expires_in":0,"irreversible":false}`, 200)
	require.JSONEq(t, `{}`, call(http.MethodDelete, first, "", 200).Body.String())
	var saved models.Filter
	require.NoError(t, testDB.GormDB.First(&saved, group.Id).Error)
	require.Len(t, saved.Keywords, 1)
	require.Equal(t, "renamed", saved.Keywords[0].Keyword)

	created := call(http.MethodPost, "/filters", `{"phrase":"original","context":["public"],"irreversible":true}`, 200)
	var item legacyFilter
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &item))
	require.JSONEq(t, `{"id":"`+item.Id+`","phrase":"original","context":["public"],"whole_word":true,"expires_at":null,"irreversible":true}`, created.Body.String())
	updated := call(http.MethodPut, "/filters/"+item.Id, `{"phrase":"changed","whole_word":false}`, 200)
	require.JSONEq(t, `{"id":"`+item.Id+`","phrase":"changed","context":["public"],"whole_word":false,"expires_at":null,"irreversible":true}`, updated.Body.String())
	call(http.MethodDelete, "/filters/"+entries[1].Id, "", 200)
	require.NoError(t, testDB.GormDB.First(&saved, group.Id).Error)
	require.Empty(t, saved.Keywords)
	call(http.MethodGet, "/filters/"+entries[1].Id, "", 404)
}
