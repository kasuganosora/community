package activitypub

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRemoteSearchIdentifier(t *testing.T) {
	for _, tc := range []struct {
		query, want string
		valid       bool
	}{
		{"@alice@example.org", "alice@example.org", true}, {"acct:alice@example.org", "alice@example.org", true}, {" alice@example.org ", "alice@example.org", true}, {"https://example.org/users/opaque-id", "https://example.org/users/opaque-id", true},
		{"alice", "", false}, {"hello world", "", false}, {"alice@@example.org", "", false}, {"alice@example.org/evil", "", false}, {"alice@example.org?query", "", false}, {"https://user:password@example.org/actor", "", false}, {"https://example.org/actor#fragment", "", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, ok := remoteSearchIdentifier(tc.query)
			require.Equal(t, tc.valid, ok)
			if ok {
				require.Equal(t, tc.want, got)
			}
		})
	}
}
func TestResolveSearchAccountBlocksPrivateURL(t *testing.T) {
	setupWebfingerDB(t)
	_, err := ResolveSearchAccount(context.Background(), "http://127.0.0.1:12345/actor")
	require.Error(t, err)
}
func TestResolveSearchAccountSkipsPlainKeywords(t *testing.T) {
	account, err := ResolveSearchAccount(context.Background(), "普通 搜索")
	require.NoError(t, err)
	require.Nil(t, account)
}
