package homeserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestGroupHotScoreSpamAndDecay(t *testing.T) {
	now := time.Now()
	spam := &groupHotActivity{posts: map[string]float64{"@s:x": 500}, posterAge: map[string]float64{"@s:x": 1}, joins: map[string]float64{}}
	crowd := &groupHotActivity{posts: map[string]float64{}, posterAge: map[string]float64{}, joins: map[string]float64{}}
	for _, u := range []string{"a", "b", "c", "d", "e"} {
		crowd.posts[u], crowd.posterAge[u] = 2, 1
	}
	require.Greater(t, groupHotScore(crowd, 5, time.Time{}, now), groupHotScore(spam, 5, time.Time{}, now))
	require.InDelta(t, 0.5, groupHotDecay(now.Add(-groupHotHalfLife).UnixMilli(), now), 0.001)
	require.Greater(t, groupHotScore(nil, 0, now.Add(-time.Hour), now), groupHotScore(nil, 0, now.Add(-4*24*time.Hour), now))
}

func TestExploreGroupsListingFiltersAndOrder(t *testing.T) {
	hs, conn, _ := setupWebGroupCore(t)
	require.NoError(t, conn.AutoMigrate(&MatrixGroupHotScore{}, &models.DomainBlock{}, &models.Block{}))
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	mk := func(id, creator, name, vis, rule string, direct bool, created time.Time) {
		require.NoError(t, conn.Create(&MatrixRoom{RoomID: id, Creator: creator, Name: new(name), Visibility: vis, JoinRule: rule, IsDirect: direct, CreatedAt: &created}).Error)
	}
	mk("!quiet:test.local", "@alice:test.local", "Quiet local", "public", "public", false, old)
	mk("!busy:test.local", "@alice:test.local", "Busy local", "public", "public", false, old)
	mk("!unlisted:test.local", "@alice:test.local", "Unlisted", "private", "public", false, old)
	mk("!private:test.local", "@alice:test.local", "Private chat", "private", "invite", false, old)
	mk("!dm:test.local", "@alice:test.local", "DM", "private", "public", true, old)
	mk("!remote:remote.example", "@x:remote.example", "Remote group", "", "public", false, old)
	mk("!remotepriv:remote.example", "@x:remote.example", "Remote private", "", "invite", false, old)
	mk("!bad:evil.example", "@x:evil.example", "Suspended", "", "public", false, old)
	mk("!new:test.local", "@alice:test.local", "Brand new", "public", "public", false, now.Add(-time.Hour))
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "evil.example", Severity: "suspend"}).Error)
	ev := func(room, sender string, ts time.Time, i int) {
		require.NoError(t, conn.Create(&MatrixEvent{EventID: "$" + room + sender + time.Duration(i).String(), RoomID: room, EventType: "m.room.message", Sender: sender, Depth: 1, OriginServerTS: ts.UnixMilli(), Content: map[string]any{"body": "x"}}).Error)
	}
	for i, u := range []string{"@a:test.local", "@b:test.local", "@c:test.local", "@d:test.local"} {
		ev("!busy:test.local", u, now.Add(-time.Duration(i)*time.Hour), i)
		ev("!remote:remote.example", u, now.Add(-3*24*time.Hour), i)
	}
	ev("!quiet:test.local", "@a:test.local", now.Add(-20*24*time.Hour), 9) // outside window
	require.NoError(t, RefreshGroupHotScores(context.Background(), hs, now))

	get := func(q string) ([]map[string]any, string) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/explore/groups"+q, nil)
		HandleExploreGroups(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		var out []map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out, w.Header().Get("Link")
	}
	all, _ := get("?limit=40")
	names := []string{}
	for _, g := range all {
		names = append(names, g["name"].(string))
	}
	require.Equal(t, []string{"Busy local", "Remote group", "Brand new", "Quiet local"}, names)
	// Stable cursor pagination returns the same order with no gaps or repeats.
	paged := []string{}
	q := "?limit=1"
	for range 10 {
		page, link := get(q)
		for _, g := range page {
			paged = append(paged, g["name"].(string))
		}
		if link == "" {
			break
		}
		q = "?" + link[strings.Index(link, "?")+1:strings.Index(link, ">")]
	}
	require.Equal(t, names, paged)
	local, _ := get("?local=true")
	require.Len(t, local, 3)
	// Becoming private removes it on the next refresh.
	require.NoError(t, conn.Model(&MatrixRoom{}).Where("room_id = ?", "!busy:test.local").Update("join_rule", "invite").Error)
	require.NoError(t, RefreshGroupHotScores(context.Background(), hs, now.Add(time.Minute)))
	all, _ = get("")
	require.Len(t, all, 3)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/explore/groups?cursor=zzz", nil)
	HandleExploreGroups(c)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "EXPLORE_GROUPS_INVALID_CURSOR")
}
