package status

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kasuganosora/community/models"
)

func statusCounts(t *testing.T, ts *testSetup) *models.Status {
	t.Helper()

	st := &models.Status{}
	require.NoError(t, ts.db.First(st, ts.status.Id).Error)

	return st
}

// A single favourite must increase favourites_count by exactly one
// (FavoriteStatus.AfterCreate already increments it).
func TestFavourite_IncrementsCountOnce(t *testing.T) {
	ts := newTestSetup(t)
	params := gin.Params{{Key: "id", Value: strconv.FormatUint(ts.status.Id, 10)}}

	c, w := newGinContext(t, params)
	setAccount(c, ts.acct)
	safeHandler(c, Favourite)
	require.Equal(t, 200, w.Code, w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 1, resp["favourites_count"])
	assert.EqualValues(t, 1, statusCounts(t, ts).FavouritesCount)

	c2, w2 := newGinContext(t, params)
	setAccount(c2, ts.acct)
	safeHandler(c2, Unfavourite)
	require.Equal(t, 200, w2.Code, w2.Body.String())
	assert.EqualValues(t, 0, statusCounts(t, ts).FavouritesCount)
}

func TestReblog_UpdatesCount(t *testing.T) {
	ts := newTestSetup(t)
	params := gin.Params{{Key: "id", Value: strconv.FormatUint(ts.status.Id, 10)}}

	c, w := newGinContextPost(t, params, ReblogRequest{Visibility: "public"})
	setAccount(c, ts.acct)
	safeHandler(c, Reblog)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.EqualValues(t, 1, statusCounts(t, ts).ReblogsCount)

	c2, w2 := newGinContext(t, params)
	setAccount(c2, ts.acct)
	safeHandler(c2, Unreblog)
	require.Equal(t, 200, w2.Code, w2.Body.String())
	assert.EqualValues(t, 0, statusCounts(t, ts).ReblogsCount)
}
