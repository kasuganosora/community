package community

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/stretchr/testify/require"
)

// A public remote post that is already stored is what the HTML permalink
// serves. Logged-out visitors in "local" mode must still be able to read
// that post and a parent that is already stored. Timelines stay local.
func TestVisitorLocalModeReturnsRemoteStatusPermalink(t *testing.T) {
	f := newSigninFixture(t)
	restore := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: moderation.UGCLocal})
	t.Cleanup(restore)

	domain := "remote.example"
	author := models.Account{Id: 9401, Username: "remoteauthor", Domain: &domain, Acct: "remoteauthor@remote.example", DisplayName: "Remote", OriginId: "https://remote.example/users/remoteauthor"}
	require.NoError(t, f.conn.Create(&author).Error)

	parentURI := "https://remote.example/notes/parent"
	parent := models.Status{
		Id:         80,
		AccountId:  author.Id,
		Domain:     &domain,
		Visibility: models.StatusVisibilityPublic,
		OriginUri:  &parentURI,
		Content:    models.StatusContent{Content: "parent-body"},
	}
	parent.MarkBackfilled()
	parent.Properties[models.StatusPropReplyParentChecked] = "1"
	require.NoError(t, f.conn.Create(&parent).Error)
	child := models.Status{
		Id:         81,
		AccountId:  author.Id,
		Domain:     &domain,
		Visibility: models.StatusVisibilityPublic,
		Content:    models.StatusContent{Content: "child-body"},
	}
	child.MarkBackfilled()
	child.Properties[models.StatusPropReplyToURI] = parentURI
	require.NoError(t, f.conn.Create(&child).Error)
	hidden := models.Status{
		Id:         82,
		AccountId:  author.Id,
		Domain:     &domain,
		Visibility: models.StatusVisibilityPrivate,
		Content:    models.StatusContent{Content: "private-remote-body"},
	}
	hidden.MarkBackfilled()
	require.NoError(t, f.conn.Create(&hidden).Error)

	for _, prefix := range []string{"/api/v1", "/api/v2"} {
		show := f.get(fmt.Sprintf("%s/statuses/%d", prefix, child.Id), "")
		require.Equal(t, http.StatusOK, show.Code, show.Body.String())
		require.Contains(t, show.Body.String(), "child-body")
		require.Contains(t, show.Body.String(), "remoteauthor@remote.example")

		ctx := f.get(fmt.Sprintf("%s/statuses/%d/context", prefix, child.Id), "")
		require.Equal(t, http.StatusOK, ctx.Code, ctx.Body.String())
		require.Contains(t, ctx.Body.String(), "parent-body", "stored parent belongs in ancestors")
		require.Contains(t, ctx.Body.String(), fmt.Sprintf(`"id":"%d"`, parent.Id))

		missing := f.get(fmt.Sprintf("%s/statuses/%d", prefix, hidden.Id), "")
		require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
		require.NotContains(t, missing.Body.String(), "private-remote-body")
	}

	var linked models.Status
	require.NoError(t, f.conn.First(&linked, child.Id).Error)
	require.NotNil(t, linked.InReplyToId)
	require.Equal(t, parent.Id, *linked.InReplyToId)
}

func TestVisitorLocalModeStillHidesRemoteTimelinePosts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: moderation.UGCLocal})
	t.Cleanup(restore)
	body := `[{"id":"1","content":"local-post","account":{"username":"a","acct":"a"}},{"id":"2","content":"remote-post","account":{"username":"b","acct":"b@remote.example"}}]`
	app := gin.New()
	app.GET("/api/v1/timelines/public", UGCVisitorGuard(), func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(body))
	})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/timelines/public", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "local-post")
	require.NotContains(t, response.Body.String(), "remote-post")
}
