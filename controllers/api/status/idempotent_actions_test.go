package status

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// assertIdempotentStatus checks a Mastodon status action answered 200 with
// the (original) Status entity carrying the expected viewer flag.
func assertIdempotentStatus(t *testing.T, w *httptest.ResponseRecorder, statusID uint64, field string, want bool) {
	t.Helper()
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, strconv.FormatUint(statusID, 10), resp["id"], "the original status is returned")
	require.Equal(t, want, resp[field], "%s in %s", field, w.Body.String())
}

// Repeating a status action (or its undo) is idempotent in Mastodon: every
// call answers 200 with the status, and counters change only once.
func TestStatusActionsAreIdempotent(t *testing.T) {
	type action struct {
		name    string
		do, undo func(*gin.Context)
		field   string
		post    bool
	}
	actions := []action{
		{"favourite", Favourite, Unfavourite, "favourited", false},
		{"reblog", Reblog, Unreblog, "reblogged", true},
		{"bookmark", Bookmark, Unbookmark, "bookmarked", false},
		{"pin", Pin, UnPin, "pinned", false},
		{"mute", MuteStatus, UnmuteStatus, "muted", false},
	}
	for _, a := range actions {
		t.Run(a.name, func(t *testing.T) {
			ts := newTestSetup(t)
			params := gin.Params{{Key: "id", Value: strconv.FormatUint(ts.status.Id, 10)}}
			call := func(f func(*gin.Context)) *httptest.ResponseRecorder {
				var c *gin.Context
				var w *httptest.ResponseRecorder
				if a.post {
					c, w = newGinContextPost(t, params, nil)
				} else {
					c, w = newGinContext(t, params)
				}
				setAccount(c, ts.acct)
				safeHandler(c, f)
				return w
			}
			// Undo before anything was done: no-op, 200.
			assertIdempotentStatus(t, call(a.undo), ts.status.Id, a.field, false)
			for i := 0; i < 2; i++ {
				assertIdempotentStatus(t, call(a.do), ts.status.Id, a.field, true)
			}
			st := statusCounts(t, ts)
			switch a.name {
			case "favourite":
				require.EqualValues(t, 1, st.FavouritesCount)
			case "reblog":
				require.EqualValues(t, 1, st.ReblogsCount)
				var n int64
				require.NoError(t, ts.db.Model(&models.Status{}).Where("reblog_id = ?", ts.status.Id).Count(&n).Error)
				require.EqualValues(t, 1, n, "a second reblog must not create another boost")
			}
			for i := 0; i < 2; i++ {
				assertIdempotentStatus(t, call(a.undo), ts.status.Id, a.field, false)
			}
			st = statusCounts(t, ts)
			require.EqualValues(t, 0, st.FavouritesCount)
			require.EqualValues(t, 0, st.ReblogsCount)
		})
	}
}

// The idempotent no-op paths must not reveal statuses the caller cannot see.
func TestIdempotentNoOpsStillCheckVisibility(t *testing.T) {
	ts := newTestSetup(t)
	require.NoError(t, ts.db.Model(&models.Status{}).Where("id = ?", ts.status.Id).Update("visibility", models.StatusVisibilityDirect).Error)
	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "outsider" })
	other := ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "outsider" })
	params := gin.Params{{Key: "id", Value: strconv.FormatUint(ts.status.Id, 10)}}
	for name, f := range map[string]func(*gin.Context){"unfavourite": Unfavourite, "unreblog": Unreblog, "unbookmark": Unbookmark, "favourite": Favourite, "reblog": Reblog, "bookmark": Bookmark} {
		c, w := newGinContextPost(t, params, nil)
		setAccount(c, other)
		safeHandler(c, f)
		require.Equal(t, 403, w.Code, "%s: %s", name, w.Body.String())
	}
}
