package community

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/moderation"
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

func TestRobotsTxtAdvertisesSitemap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevURL := config.C.AppBaseURL
	prevConfigured := config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://social.example", true)
	t.Cleanup(func() { config.SetAppBaseURL(prevURL, prevConfigured) })
	restore := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: moderation.UGCAll})
	t.Cleanup(restore)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	RobotsTxt(c)
	body := w.Body.String()
	require.Contains(t, body, "Disallow: /admin\n")
	require.Contains(t, body, "Disallow: /settings\n")
	require.Contains(t, body, "Sitemap: https://social.example/sitemap.xml\n")
	require.NotContains(t, body, "Disallow: /tags")
	require.NotContains(t, body, "noindex")
}
