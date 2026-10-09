package common

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/systemsettings"
)

// pageURL builds an absolute URL on the public base URL for the current
// request path, preserving every query parameter except the id cursors,
// which are replaced by the given overrides.
func pageURL(c *gin.Context, set map[string]string) string {
	query := c.Request.URL.Query()
	for _, k := range []string{"max_id", "since_id", "min_id"} {
		query.Del(k)
	}

	for k, v := range set {
		query.Set(k, v)
	}

	out := strings.TrimRight(systemsettings.GetBaseURL(), "/") + c.Request.URL.Path
	if len(query) > 0 {
		out += "?" + query.Encode()
	}

	return out
}

func writeLink(c *gin.Context, next, prev string) {
	parts := make([]string, 0, 2)
	if next != "" {
		parts = append(parts, "<"+next+`>; rel="next"`)
	}

	if prev != "" {
		parts = append(parts, "<"+prev+`>; rel="prev"`)
	}

	if len(parts) > 0 {
		c.Header("Link", strings.Join(parts, ", "))
	}
}

// SetPageLinks sets Mastodon-style pagination Link headers for an id-paged
// list. firstID/lastID are the ids used as cursors (the collection's own ids,
// which may differ from the ids of the returned entities, e.g. follows or
// favourites). Nothing is emitted for an empty page. rel="next" is only set
// when hasNext is true; rel="prev" whenever the page has items.
func SetPageLinks(c *gin.Context, firstID, lastID string, hasNext bool) {
	if firstID == "" || lastID == "" {
		return
	}

	next := ""
	if hasNext {
		next = pageURL(c, map[string]string{"max_id": lastID})
	}

	writeLink(c, next, pageURL(c, map[string]string{"min_id": firstID}))
}

// SetPageLinksUint is SetPageLinks for numeric cursors.
func SetPageLinksUint(c *gin.Context, firstID, lastID uint64, hasNext bool) {
	SetPageLinks(c, strconv.FormatUint(firstID, 10), strconv.FormatUint(lastID, 10), hasNext)
}

// SetListLinks sets next/prev links from the string ids of the first and last
// returned entities.
func SetListLinks[T any](c *gin.Context, items []T, id func(T) string) {
	if len(items) == 0 {
		return
	}

	SetPageLinks(c, id(items[0]), id(items[len(items)-1]), true)
}

// SetOffsetLinks sets next/prev links for offset-paged lists (trends,
// directory): next when the page is full, prev when offset > 0.
func SetOffsetLinks(c *gin.Context, offset, count, limit int) {
	next, prev := "", ""

	if count > 0 && count >= limit {
		next = pageURL(c, map[string]string{"offset": strconv.Itoa(offset + limit)})
	}

	if offset > 0 {
		p := offset - limit
		if p < 0 {
			p = 0
		}

		prev = pageURL(c, map[string]string{"offset": strconv.Itoa(p)})
	}

	writeLink(c, next, prev)
}
