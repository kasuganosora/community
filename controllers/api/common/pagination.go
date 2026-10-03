package common

import (
	"github.com/gin-gonic/gin"
	"strconv"
)

// SetNextPage links to the next page using the collection relation's ID,
// which need not match any account or status ID in the response.
func SetNextPage(c *gin.Context, lastID uint64) {
	next := *c.Request.URL
	query := next.Query()
	query.Set("max_id", strconv.FormatUint(lastID, 10))
	next.RawQuery = query.Encode()
	c.Header("Link", "<"+next.String()+">; rel=\"next\"")
}
