package trends

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// These are public API contracts independent of the replaced cumulative
// counters and shared response cache. Exercise the handlers, not pagination().
func TestLegacyTrendsPaginationAndResponseShape(t *testing.T) {
	conn := setupTrendDB(t)
	now := time.Now().UTC().Add(-time.Hour)
	tags := make([]*models.Tag, 25)
	for i := range tags {
		tags[i] = &models.Tag{Name: fmt.Sprintf("topic%02d", i)}
		require.NoError(t, conn.Create(tags[i]).Error)
	}
	authors := make([]*models.Account, 5)
	for i := range authors {
		authors[i] = trendAuthor(t, conn, uint64(i+1))
	}
	for i := uint64(1); i <= 25; i++ {
		postTags := []*models.Tag{}
		if i <= 5 {
			postTags = tags // Five distinct authors qualify every topic.
		}
		trendPost(t, conn, i, authors[(i-1)%5], now, models.StatusVisibilityPublic, postTags...)
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", i).UpdateColumn("favourites_count", i).Error)
		require.NoError(t, conn.Create(&models.StatusCard{StatusId: i, Url: fmt.Sprintf("https://example.test/%02d", i), Title: "Public link", UrlHash: fmt.Sprintf("link-%d", i)}).Error)
	}
	for _, endpoint := range []struct {
		name     string
		handler  gin.HandlerFunc
		identity string
	}{{"tags", ListTags, "name"}, {"statuses", ListStatuses, "id"}, {"links", ListLinks, "url"}} {
		t.Run(endpoint.name, func(t *testing.T) {
			request := func(query string) ([]map[string]any, string) {
				c, w := trendContext("/api/v1/trends/" + endpoint.name + query)
				endpoint.handler(c)
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				var rows []map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
				require.NotNil(t, rows, "empty pages must be [] rather than null")
				return rows, w.Header().Get("Link")
			}
			baseline, _ := request("?limit=20")
			require.Len(t, baseline, 20)
			for _, query := range []string{"", "?limit=abc", "?limit=0", "?limit=-1", "?limit=100", "?offset=abc", "?offset=-1"} {
				rows, _ := request(query)
				require.Len(t, rows, 10, query)
				require.Equal(t, baseline[0][endpoint.identity], rows[0][endpoint.identity], query)
			}
			one, _ := request("?limit=1")
			require.Len(t, one, 1)
			page, links := request("?limit=2&offset=2")
			require.Len(t, page, 2)
			require.Equal(t, baseline[2][endpoint.identity], page[0][endpoint.identity])
			require.Contains(t, links, "limit=2&offset=4")
			require.Contains(t, links, `rel="next"`)
			require.Contains(t, links, "limit=2&offset=0")
			require.Contains(t, links, `rel="prev"`)
			tail, links := request("?limit=10&offset=20")
			require.Len(t, tail, 5)
			require.NotContains(t, links, `rel="next"`)
			empty, _ := request("?offset=999999")
			require.Empty(t, empty)
			if endpoint.name == "statuses" {
				account, ok := baseline[0]["account"].(map[string]any)
				require.True(t, ok)
				require.NotEmpty(t, account["id"])
				require.NotEmpty(t, account["username"])
				require.Equal(t, "public", baseline[0]["visibility"])
				return
			}
			history, ok := baseline[0]["history"].([]any)
			require.True(t, ok)
			require.Len(t, history, 7)
			for _, raw := range history {
				day := raw.(map[string]any)
				for _, key := range []string{"day", "uses", "accounts"} {
					value, ok := day[key].(string)
					require.True(t, ok, key)
					_, err := strconv.ParseUint(value, 10, 64)
					require.NoError(t, err, key)
				}
			}
			if endpoint.name == "links" {
				require.Equal(t, "Public link", baseline[0]["title"])
				require.Contains(t, baseline[0], "published_at")
				require.Contains(t, baseline[0], "authors")
			}
		})
	}
}

func TestLegacyTrendingStatusesKeepPublicRootEligibility(t *testing.T) {
	conn := setupTrendDB(t)
	author := trendAuthor(t, conn, 1)
	now := time.Now().UTC()
	for id := uint64(1); id <= 12; id++ {
		trendPost(t, conn, id, author, now.Add(-time.Hour), models.StatusVisibilityPublic)
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", id).UpdateColumn("favourites_count", 100).Error)
	}
	for id, score := range map[uint64]int{1: 1, 2: 20, 3: 10} {
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", id).UpdateColumn("favourites_count", score).Error)
	}
	changes := map[uint64]map[string]any{
		4:  {"in_reply_to_id": 1},
		5:  {"reblog_id": 1},
		6:  {"created_at": now.Add(-8 * 24 * time.Hour)},
		7:  {"created_at": now.Add(time.Hour)},
		8:  {"visibility": models.StatusVisibilityUnlisted},
		9:  {"visibility": models.StatusVisibilityPrivate},
		10: {"visibility": models.StatusVisibilityDirect},
		11: {"scheduled_at": now.Add(time.Hour)},
		12: {"deleted_at": now},
	}
	for id, change := range changes {
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", id).UpdateColumns(change).Error)
	}
	c, w := trendContext("/api/v1/trends/statuses")
	ListStatuses(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.Len(t, rows, 3)
	for i, id := range []string{"2", "3", "1"} {
		require.Equal(t, id, rows[i]["id"])
	}
}

func TestLegacyTrendsEmptyArraysAndMissingCardsTable(t *testing.T) {
	conn := setupTrendDB(t)
	for _, handler := range []gin.HandlerFunc{ListTags, ListStatuses, ListLinks} {
		c, w := trendContext("/api/v1/trends")
		handler(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.JSONEq(t, "[]", w.Body.String())
	}
	require.NoError(t, conn.Migrator().DropTable(&models.StatusCard{}))
	c, w := trendContext("/api/v1/trends/links")
	ListLinks(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.JSONEq(t, "[]", w.Body.String())
}
