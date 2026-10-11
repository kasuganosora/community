package datatransfer

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPersonalImportInvalidatesOnlyAffectedHotTimelines(t *testing.T) {
	conn, b, o := personalFixture(t)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	mine := "feed:v3:{home:" + strconv.FormatUint(o.OwnerAccountID, 10) + "}:items"
	for _, key := range []string{mine + ":empty", "feed:v3:{home:999}:items:empty", "feed:v3:{public:all}:items:empty", "auth:session"} {
		require.NoError(t, client.Set(ctx, key, "cached", 0).Err())
	}
	for _, key := range []string{mine, "feed:v3:{home:999}:items", "feed:v3:{public:all}:items"} {
		require.NoError(t, client.HSet(ctx, key+":meta", "epoch", "test", "generation", 0, "ready", 1).Err())
		require.NoError(t, client.HSet(ctx, key+":versions", "_epoch", "test", "9", "2:1").Err())
	}
	runner := &importer{bundle: b, options: o, ids: map[string]map[string]uint64{}}
	selector, err := runner.cacheSelector(ctx, conn)
	require.NoError(t, err)
	require.NoError(t, invalidateImportedKeys(ctx, client, selector))
	require.False(t, server.Exists(mine+":empty"))
	require.Equal(t, "0", client.HGet(ctx, mine+":meta", "ready").Val())
	require.Equal(t, "2:1", client.HGet(ctx, mine+":versions", "9").Val())
	require.True(t, server.Exists("feed:v3:{home:999}:items:empty"))
	require.True(t, server.Exists("feed:v3:{public:all}:items:empty"))
	require.True(t, server.Exists("auth:session"))
	o.Scope = "site"
	runner.options = o
	selector, err = runner.cacheSelector(ctx, conn)
	require.NoError(t, err)
	require.NoError(t, invalidateImportedKeys(ctx, client, selector))
	require.False(t, server.Exists("feed:v3:{home:999}:items:empty"))
	require.False(t, server.Exists("feed:v3:{public:all}:items:empty"))
	require.True(t, server.Exists("auth:session"))
}

func TestImportCacheSelectorV3ListAndCanonicalTag(t *testing.T) {
	conn, b, o := personalFixture(t)
	b.Posts = []Post{{ID: "fixture-post", Visibility: "public", Tags: []string{"#GoLang"}}}
	runner := &importer{bundle: b, options: o, ids: map[string]map[string]uint64{"post": {"fixture-post": 1}, "list": {"fixture-list": 21}}}
	selector, err := runner.cacheSelector(context.Background(), conn)
	require.NoError(t, err)
	require.True(t, selector("feed:v3:{hashtag:local:golang}:items"))
	require.False(t, selector("feed:v3:{hashtag:unrelated}:items"))
	require.True(t, selector("feed:v3:{list:media:21}:items"))
	require.False(t, selector("feed:v3:{list:media:22}:items"))
}
