package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/assert"
)

func linkCtx(target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)

	return c, w
}

func TestSetPageLinksKeepsParamsAndDropsCursors(t *testing.T) {
	systemsettings.Set(systemsettings.KeyDomain, "c.example")
	c, w := linkCtx("/api/v1/timelines/public?local=1&limit=20&max_id=99&since_id=1&min_id=2&exclude_types[]=follow")

	SetListLinks(c, []string{"30", "29", "21"}, func(s string) string { return s })

	link := w.Header().Get("Link")
	assert.Contains(t, link, `<https://c.example/api/v1/timelines/public?`)
	assert.Contains(t, link, `max_id=21>; rel="next"`)
	assert.Contains(t, link, `min_id=30>; rel="prev"`)
	assert.Contains(t, link, "local=1")
	assert.Contains(t, link, "limit=20")
	assert.Contains(t, link, "exclude_types%5B%5D=follow")
	assert.NotContains(t, link, "since_id")
	assert.NotContains(t, link, "max_id=99")
	assert.NotContains(t, link, "min_id=2&")
}

func TestSetPageLinksOmittedForEmptyPage(t *testing.T) {
	c, w := linkCtx("/api/v1/timelines/home")
	SetListLinks(c, []string{}, func(s string) string { return s })
	assert.Empty(t, w.Header().Get("Link"))
}

func TestSetPageLinksNextOnlyWhenMore(t *testing.T) {
	c, w := linkCtx("/api/v1/favourites?limit=2")
	SetPageLinksUint(c, 9, 8, false)
	link := w.Header().Get("Link")
	assert.NotContains(t, link, `rel="next"`)
	assert.Contains(t, link, `min_id=9>; rel="prev"`)
}

func TestSetOffsetLinks(t *testing.T) {
	c, w := linkCtx("/api/v1/trends/tags?limit=10&offset=10")
	SetOffsetLinks(c, 10, 10, 10)
	link := w.Header().Get("Link")
	assert.Contains(t, link, "offset=20>; rel=\"next\"")
	assert.Contains(t, link, "offset=0>; rel=\"prev\"")

	c, w = linkCtx("/api/v1/trends/tags?limit=10")
	SetOffsetLinks(c, 0, 3, 10)
	assert.Empty(t, w.Header().Get("Link"))
}
