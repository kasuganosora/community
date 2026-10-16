package community

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterRemoteUGC(t *testing.T) {
	local := map[string]any{"id": "1", "content": "x", "account": map[string]any{"username": "a", "acct": "a"}}
	remote := map[string]any{"id": "2", "content": "y", "account": map[string]any{"username": "b", "acct": "b@remote.example"}}
	boost := map[string]any{"id": "3", "content": "", "account": map[string]any{"username": "a", "acct": "a"}, "reblog": remote}
	list, _ := json.Marshal([]any{local, remote, boost})
	out, gone := filterRemoteUGC(list)
	require.False(t, gone)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	require.Len(t, got, 1)
	require.Equal(t, "1", got[0]["id"])

	single, _ := json.Marshal(remote)
	_, gone = filterRemoteUGC(single)
	require.True(t, gone, "a remote status is not found for visitors")
	acct, _ := json.Marshal(map[string]any{"id": "9", "username": "b", "acct": "b@remote.example"})
	_, gone = filterRemoteUGC(acct)
	require.True(t, gone)

	ctx, _ := json.Marshal(map[string]any{"ancestors": []any{local}, "descendants": []any{remote, local}})
	out, gone = filterRemoteUGC(ctx)
	require.False(t, gone)
	var c map[string][]any
	require.NoError(t, json.Unmarshal(out, &c))
	require.Len(t, c["descendants"], 1)

	unchanged, _ := json.Marshal([]any{local})
	out, _ = filterRemoteUGC(unchanged)
	require.Nil(t, out)
}
