package community

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

const recordNotFoundBody = `{"error":"Record not found"}`

type hiddenStatusFixture struct {
	signinFixture
	private, direct, public, hiddenPrivate models.Status
	privatePoll, publicPoll, signinPoll    models.Poll
	readWrite, readOnly, noStatusScope     string
}

func newHiddenStatusFixture(t *testing.T) hiddenStatusFixture {
	t.Helper()
	f := hiddenStatusFixture{signinFixture: newSigninFixture(t)}
	conn := f.conn
	f.private = models.Status{AccountId: f.open.Id, Visibility: models.StatusVisibilityPrivate, Content: models.StatusContent{Content: "followers only"}}
	f.direct = models.Status{AccountId: f.open.Id, Visibility: models.StatusVisibilityDirect, Content: models.StatusContent{Content: "direct only"}}
	f.public = models.Status{AccountId: f.open.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "public poll"}}
	f.hiddenPrivate = models.Status{AccountId: f.hidden.Id, Visibility: models.StatusVisibilityPrivate, Content: models.StatusContent{Content: hiddenText}}
	for _, s := range []*models.Status{&f.private, &f.direct, &f.public, &f.hiddenPrivate} {
		require.NoError(t, conn.Create(s).Error)
	}
	for _, p := range []struct {
		poll   *models.Poll
		status models.Status
	}{{&f.privatePoll, f.private}, {&f.publicPoll, f.public}, {&f.signinPoll, f.hiddenPost}} {
		*p.poll = models.Poll{AccountId: p.status.AccountId, StatusId: p.status.Id, Options: models.PollOptions{{Index: 0, Content: "yes"}, {Index: 1, Content: "no"}}}
		require.NoError(t, conn.Create(p.poll).Error)
	}
	viewer := uint64(9103) // the fixture's third user: follows nobody
	mint := func(read, write uint64) string {
		row, token := models.NewAuthToken(viewer, models.AuthTokenTypeOauth, 0, 0, &models.ApplicationScope{ReadScope: read, WriteScope: write}, 3600, config.C.EncryptedKey)
		require.NoError(t, conn.Create(row).Error)
		return token
	}
	f.readWrite = mint(models.AppScopeRead, models.AppScopeWrite)
	f.readOnly = mint(models.AppScopeRead, 0)
	f.noStatusScope = mint(models.AppScopeReadAccounts, 0)
	return f
}

func (f hiddenStatusFixture) do(method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	f.app.ServeHTTP(response, request)
	return response
}

// Mastodon answers a private or direct status the viewer may not see with the
// same 404 as a missing id, so its existence cannot be probed.
func TestHiddenStatusesAnswerRecordNotFound(t *testing.T) {
	f := newHiddenStatusFixture(t)
	reads := []string{"", "/context", "/card", "/reactions", "/source", "/history", "/favourited_by", "/reblogged_by"}
	writes := []string{"/favourite", "/unfavourite", "/reblog", "/unreblog", "/bookmark", "/unbookmark", "/pin", "/unpin", "/mute", "/unmute"}
	for _, id := range []uint64{f.private.Id, f.direct.Id, 987654321} {
		for _, suffix := range reads {
			path := fmt.Sprintf("/api/v1/statuses/%d%s", id, suffix)
			r := f.do(http.MethodGet, path, f.readWrite)
			require.Equal(t, http.StatusNotFound, r.Code, "%s: %s", path, r.Body.String())
			require.JSONEq(t, recordNotFoundBody, r.Body.String(), path)
		}
		for _, suffix := range writes {
			path := fmt.Sprintf("/api/v1/statuses/%d%s", id, suffix)
			r := f.do(http.MethodPost, path, f.readWrite)
			require.Equal(t, http.StatusNotFound, r.Code, "%s: %s", path, r.Body.String())
			require.JSONEq(t, recordNotFoundBody, r.Body.String(), path)
		}
		r := f.do(http.MethodDelete, fmt.Sprintf("/api/v1/statuses/%d", id), f.readWrite)
		require.Equal(t, http.StatusNotFound, r.Code, r.Body.String())
		r = f.do(http.MethodPost, fmt.Sprintf("/api/v1/statuses/%d/translate/en", id), f.readWrite)
		require.Equal(t, http.StatusNotFound, r.Code, r.Body.String())
		// Logged out: the same 404 on the optional-auth reads.
		for _, suffix := range []string{"", "/context", "/card", "/reactions"} {
			path := fmt.Sprintf("/api/v1/statuses/%d%s", id, suffix)
			r := f.do(http.MethodGet, path, "")
			require.Equal(t, http.StatusNotFound, r.Code, "%s: %s", path, r.Body.String())
			require.JSONEq(t, recordNotFoundBody, r.Body.String(), path)
		}
	}
	// A visible status by someone else keeps the ownership answers.
	require.Equal(t, http.StatusBadRequest, f.do(http.MethodPost, fmt.Sprintf("/api/v1/statuses/%d/pin", f.public.Id), f.readWrite).Code)
	require.Equal(t, http.StatusBadRequest, f.do(http.MethodPost, fmt.Sprintf("/api/v1/statuses/%d/mute", f.public.Id), f.readWrite).Code)
	require.Equal(t, http.StatusForbidden, f.do(http.MethodDelete, fmt.Sprintf("/api/v1/statuses/%d", f.public.Id), f.readWrite).Code)
	// A token without the write scope is still a 403 insufficient_scope.
	r := f.do(http.MethodPost, fmt.Sprintf("/api/v1/statuses/%d/favourite", f.private.Id), f.readOnly)
	require.Equal(t, http.StatusForbidden, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "insufficient_scope")
	// Visible statuses still work.
	require.Equal(t, http.StatusOK, f.do(http.MethodGet, fmt.Sprintf("/api/v1/statuses/%d", f.public.Id), "").Code)
	require.Equal(t, http.StatusOK, f.do(http.MethodPost, fmt.Sprintf("/api/v1/statuses/%d/favourite", f.public.Id), f.readWrite).Code)
}

// A private status by an account that requires sign-in must not be confirmed
// to visitors with "please sign in": it is a plain 404.
func TestSigninOnlyAuthorPrivateStatusIsNotFoundForVisitors(t *testing.T) {
	f := newHiddenStatusFixture(t)
	for _, suffix := range []string{"", "/context", "/card"} {
		r := f.do(http.MethodGet, fmt.Sprintf("/api/v1/statuses/%d%s", f.hiddenPrivate.Id, suffix), "")
		require.Equal(t, http.StatusNotFound, r.Code, r.Body.String())
		require.JSONEq(t, recordNotFoundBody, r.Body.String())
		require.NotContains(t, r.Body.String(), hiddenText)
	}
	// The public post of that author still asks the visitor to sign in.
	r := f.do(http.MethodGet, fmt.Sprintf("/api/v1/statuses/%d", f.hiddenPost.Id), "")
	require.Equal(t, http.StatusUnauthorized, r.Code)
	require.Contains(t, r.Body.String(), "SIGNIN_REQUIRED")
}

// GET /api/v1/polls/:id is public in Mastodon (authorize_if_got_token!);
// hidden polls are 404, voting still needs a token.
func TestPollReadIsPublicAndHiddenPollsAreNotFound(t *testing.T) {
	f := newHiddenStatusFixture(t)
	for _, prefix := range []string{"/api/v1", "/api/v2"} {
		poll := func(id uint64) string { return fmt.Sprintf("%s/polls/%d", prefix, id) }
		r := f.do(http.MethodGet, poll(f.publicPoll.Id), "")
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		require.Contains(t, r.Body.String(), `"yes"`)
		for _, id := range []uint64{f.privatePoll.Id, 987654321} {
			for _, token := range []string{"", f.readOnly} {
				r = f.do(http.MethodGet, poll(id), token)
				require.Equal(t, http.StatusNotFound, r.Code, r.Body.String())
				require.JSONEq(t, recordNotFoundBody, r.Body.String())
			}
		}
		require.Equal(t, http.StatusOK, f.do(http.MethodGet, poll(f.publicPoll.Id), f.readOnly).Code)
		r = f.do(http.MethodGet, poll(f.publicPoll.Id), f.noStatusScope)
		require.Equal(t, http.StatusForbidden, r.Code, "a token still needs read:statuses")
		require.Equal(t, http.StatusUnauthorized, f.do(http.MethodGet, poll(f.publicPoll.Id), "invalid-token").Code)
		r = f.do(http.MethodGet, poll(f.signinPoll.Id), "")
		require.Equal(t, http.StatusUnauthorized, r.Code, r.Body.String())
		require.Contains(t, r.Body.String(), "SIGNIN_REQUIRED")
		require.Equal(t, http.StatusOK, f.do(http.MethodGet, poll(f.signinPoll.Id), f.readOnly).Code)
		require.Equal(t, http.StatusUnauthorized, f.do(http.MethodPost, poll(f.publicPoll.Id)+"/votes", "").Code, "voting needs a token")
	}
}
