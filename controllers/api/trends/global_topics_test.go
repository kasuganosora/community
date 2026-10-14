package trends

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	discoveryapi "github.com/kasuganosora/community/controllers/api/discovery"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Exercise the Community handler and real SQL aggregation together: omitting
// scope must include eligible remote topics, without changing their privacy
// qualification or allowing cached tags to survive a remote author's opt-out.
func TestDiscoveryTopicsDefaultGlobalAndRecheckRemotePermission(t *testing.T) {
	conn := setupTrendDB(t)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previous := redisdb.GetRedisesForTest()
	var previousUniversal redis.UniversalClient
	if redisdb.HasUniversalRedis() {
		previousUniversal = redisdb.GetUniversalRedis()
	}
	redisdb.SetRedisesForTest(nil)
	redisdb.SetUniversalRedis(client)
	t.Cleanup(func() {
		redisdb.SetUniversalRedis(previousUniversal)
		redisdb.SetRedisesForTest(previous)
		_ = client.Close()
	})

	now := time.Now().UTC()
	local := trendAuthor(t, conn, 1)
	remote := trendAuthor(t, conn, 2)
	domain := "remote.example"
	remote.Domain = &domain
	require.NoError(t, conn.Model(remote).Updates(map[string]any{
		"domain": domain, "remote_discoverable": true, "remote_discovery_checked_at": now,
	}).Error)
	shared := &models.Tag{Name: "shared"}
	remoteOnly := &models.Tag{Name: "remote-only"}
	for _, tag := range []*models.Tag{shared, remoteOnly} {
		require.NoError(t, conn.Create(tag).Error)
	}
	trendPost(t, conn, 1, local, now.Add(-time.Hour), models.StatusVisibilityPublic, shared)
	trendPost(t, conn, 2, remote, now.Add(-time.Hour), models.StatusVisibilityPublic, shared, remoteOnly)
	for i, visibility := range []uint8{models.StatusVisibilityPrivate, models.StatusVisibilityDirect, models.StatusVisibilityUnlisted} {
		trendPost(t, conn, uint64(i+3), remote, now.Add(-time.Hour), visibility, shared, remoteOnly)
	}

	request := func(query string) discovery.TagTrendPage {
		t.Helper()
		c, w := trendContext("/api/v1/discovery/tags" + query)
		discoveryapi.ListTags(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var page discovery.TagTrendPage
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		return page
	}
	global := request("")
	require.Equal(t, "federated", global.Scope)
	require.Equal(t, "24h", global.Window)
	require.Equal(t, "recent", global.Kind)
	require.Len(t, global.Items, 2)
	counts := map[string]int{}
	for _, tag := range global.Items {
		counts[tag.Name] = tag.PostsCount
	}
	require.Equal(t, map[string]int{"shared": 2, "remote-only": 1}, counts)

	// Explicit local remains a supported API parameter; the website no longer
	// needs a scope setting to see its received federated topics.
	localPage := request("?scope=local")
	require.Len(t, localPage.Items, 1)
	require.Equal(t, "shared", localPage.Items[0].Name)
	require.Equal(t, 1, localPage.Items[0].PostsCount)

	require.NoError(t, conn.Model(remote).UpdateColumn("remote_discoverable", false).Error)
	afterOptOut := request("")
	require.Equal(t, global.Version, afterOptOut.Version, "reuse the existing snapshot to verify current eligibility")
	require.Len(t, afterOptOut.Items, 1)
	require.Equal(t, "shared", afterOptOut.Items[0].Name)
	require.Equal(t, 1, afterOptOut.Items[0].PostsCount)
}
