package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailDomainBlocksAPI(t *testing.T) {
	conn := newAdminTestEnv(t).gdb
	require.NoError(t, conn.AutoMigrate(&models.EmailDomainBlock{}))

	call := func(h gin.HandlerFunc, method, body string, params gin.Params) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = params
		h(c)
		return w
	}
	w := call(CreateEmailDomainBlock, "POST", `{"domain":"@Spam.Example"}`, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.Equal(t, "spam.example", created["domain"])
	id := created["id"].(string)

	assert.Equal(t, 422, call(CreateEmailDomainBlock, "POST", `{"domain":"SPAM.example"}`, nil).Code, "duplicate")
	assert.Equal(t, 422, call(CreateEmailDomainBlock, "POST", `{"domain":"not a domain"}`, nil).Code)
	assert.Equal(t, 422, call(CreateEmailDomainBlock, "POST", `{"domain":"localhost"}`, nil).Code)

	w = call(ListEmailDomainBlocks, "GET", "", nil)
	require.Equal(t, 200, w.Code)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list, 1)

	assert.Equal(t, 200, call(GetEmailDomainBlock, "GET", "", gin.Params{{Key: "id", Value: id}}).Code)
	assert.Equal(t, 404, call(GetEmailDomainBlock, "GET", "", gin.Params{{Key: "id", Value: "999"}}).Code)
	blocked, err := models.EmailDomainBlocked(conn, "x@a.spam.example")
	require.NoError(t, err)
	assert.True(t, blocked)

	assert.Equal(t, 200, call(DeleteEmailDomainBlock, "DELETE", "", gin.Params{{Key: "id", Value: id}}).Code)
	assert.Equal(t, 404, call(DeleteEmailDomainBlock, "DELETE", "", gin.Params{{Key: "id", Value: id}}).Code)
	blocked, _ = models.EmailDomainBlocked(conn, "x@spam.example")
	assert.False(t, blocked)
}
