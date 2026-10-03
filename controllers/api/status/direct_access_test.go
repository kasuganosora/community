package status

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDirectThreadAuthorizesEachBranchAndReply(t *testing.T) {
	conn, fix, alice := replacementDB(t)
	bob := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "dm_bob" }))
	carol := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "dm_carol" }))
	outsider := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "dm_outsider" }))
	parent := fix.CreateStatus(alice, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityDirect
		s.Content.Content = "group"

	})
	child := fix.CreateStatus(bob, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityDirect
		s.Content.Content = "only alice"
		s.InReplyToId = &parent.Id

	})
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: parent.Id, AccountId: bob.Id}).Error)
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: parent.Id, AccountId: carol.Id}).Error)
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: child.Id, AccountId: alice.Id}).Error)
	call := func(viewer *models.Account, id uint64, handler gin.HandlerFunc, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/statuses/"+strconv.FormatUint(id, 10), bytes.NewReader(data))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(id, 10)}}
		c.Set("account", viewer)
		safeHandler(c, handler)
		return w
	}
	w := call(carol, parent.Id, Context, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "only alice")
	w = call(alice, parent.Id, Context, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "only alice")
	w = call(outsider, child.Id, Create, map[string]any{"status": "guess", "visibility": "direct", "in_reply_to_id": strconv.FormatUint(child.Id, 10)})
	require.Equal(t, 403, w.Code, w.Body.String())
	w = call(alice, child.Id, Create, map[string]any{"status": "leak", "visibility": "public", "in_reply_to_id": strconv.FormatUint(child.Id, 10)})
	require.Equal(t, 422, w.Code, w.Body.String())
	// Both directions of a block revoke an existing direct grant.
	for _, block := range []*models.Block{{AccountId: alice.Id, TargetAccountId: bob.Id}, {AccountId: bob.Id, TargetAccountId: alice.Id}} {
		require.NoError(t, conn.Create(block).Error)
		require.NoError(t, conn.Preload("ToAccounts").First(child, child.Id).Error)
		allowed, err := child.CanViewEx(conn, alice)
		require.NoError(t, err)
		require.False(t, allowed)
		w = call(alice, child.Id, Show, nil)
		require.Equal(t, 403, w.Code, w.Body.String())
		require.NoError(t, conn.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.Block{}).Error)
	}
}
