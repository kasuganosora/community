package status

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The poll.id returned by POST /api/v1/statuses is the poll's own id, and
// GET /api/v1/polls/:id and POST /api/v1/polls/:id/votes accept it, through
// the real auth and scope middleware with a personal access token, as the
// author and as another account. Status ids are pushed ahead of poll ids so
// a status id leaking into poll.id would be caught.
func TestCreatedPollIdResolvesForViewAndVote(t *testing.T) {
	ts := newTestSetup(t)
	require.NoError(t, ts.db.AutoMigrate(&models.PersonalAccessToken{}))
	mr := miniredis.RunT(t)
	t.Cleanup(middleware.SetTokenCacheRedisForTest(redis.NewClient(&redis.Options{Addr: mr.Addr()})))
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })
	personalToken := func(userID uint64) string {
		row, raw := models.NewAuthToken(userID, models.AuthTokenTypeOauth, 0, 0, &models.ApplicationScope{ReadScope: models.AppScopeRead, WriteScope: models.AppScopeWrite}, 0, config.C.EncryptedKey)
		row.ExternalClient = models.PersonalTokenClient
		require.NoError(t, ts.db.Create(row).Error)
		return raw
	}
	author := personalToken(ts.user.Id)
	otherUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "voter" })
	ts.fix.CreateAccount(otherUser, func(a *models.Account) { a.Username = "voter" })
	voter := personalToken(otherUser.Id)
	for i := 0; i < 20; i++ {
		ts.fix.CreateStatus(ts.acct)
	}

	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	g := r.Group("/api/v1", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	write := middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses})
	read := middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses})
	g.POST("/statuses", write, Create)
	g.GET("/polls/:id", read, ViewPoll)
	g.POST("/polls/:id/votes", write, VotePoll)
	do := func(token, method, path, contentType, body string) (int, map[string]any) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		r.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	for _, tc := range []struct{ name, contentType, body string }{
		{"json", "application/json", `{"status":"poll","visibility":"public","poll":{"options":["a","b"],"expires_in":3600}}`},
		{"json string expires_in", "application/json", `{"status":"poll","visibility":"unlisted","poll":{"options":["a","b","c"],"expires_in":"86400","multiple":true}}`},
		{"form", "application/x-www-form-urlencoded", `status=poll&poll%5Boptions%5D%5B%5D=a&poll%5Boptions%5D%5B%5D=b&poll%5Bexpires_in%5D=300`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, status := do(author, "POST", "/api/v1/statuses", tc.contentType, tc.body)
			require.Equal(t, 200, code, status)
			poll, _ := status["poll"].(map[string]any)
			require.NotNil(t, poll, status)
			pollID, _ := poll["id"].(string)
			require.NotEmpty(t, pollID)
			require.NotEqual(t, status["id"], pollID, "poll.id must not be the status id")
			var stored models.Poll
			require.NoError(t, ts.db.Where("status_id = ?", status["id"]).First(&stored).Error)
			require.Equal(t, pollID, jsonID(stored.Id), "poll.id is the polls row id")

			for _, token := range []string{author, voter} {
				code, got := do(token, "GET", "/api/v1/polls/"+pollID, "", "")
				require.Equal(t, 200, code, got)
				require.Equal(t, pollID, got["id"])
			}
			code, voted := do(voter, "POST", "/api/v1/polls/"+pollID+"/votes", "application/json", `{"choices":[1]}`)
			require.Equal(t, 200, code, voted)
			require.Equal(t, pollID, voted["id"])
			require.Equal(t, true, voted["voted"])
			require.EqualValues(t, 1, voted["votes_count"])
			require.Equal(t, []any{float64(1)}, voted["own_votes"])
		})
	}
}

func jsonID(id uint64) string {
	b, _ := json.Marshal(id)
	return string(b)
}
