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
	mine := "feed:home:" + strconv.FormatUint(o.OwnerAccountID, 10)
	for _, key := range []string{mine + ":empty", "feed:home:999:empty", "feed:public:all:empty", "auth:session"} {
		require.NoError(t, client.Set(ctx, key, "cached", 0).Err())
	}
	runner := &importer{bundle: b, options: o, ids: map[string]map[string]uint64{}}
	selector, err := runner.cacheSelector(ctx, conn)
	require.NoError(t, err)
	require.NoError(t, invalidateImportedKeys(ctx, client, selector))
	require.False(t, server.Exists(mine+":empty"))
	require.True(t, server.Exists("feed:home:999:empty"))
	require.True(t, server.Exists("feed:public:all:empty"))
	require.True(t, server.Exists("auth:session"))
	o.Scope = "site"
	runner.options = o
	selector, err = runner.cacheSelector(ctx, conn)
	require.NoError(t, err)
	require.NoError(t, invalidateImportedKeys(ctx, client, selector))
	require.False(t, server.Exists("feed:home:999:empty"))
	require.False(t, server.Exists("feed:public:all:empty"))
	require.True(t, server.Exists("auth:session"))
}
