package activitypub

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// Live streams reload a status from SQL when its event arrives. Statuses
// created while handling an inbox activity (remote Create/Announce) must
// therefore only be announced after the inbox transaction commits; before,
// remote posts never reached public WebSocket/SSE subscribers.
func TestInboxStatusEventsFireAfterCommit(t *testing.T) {
	w := newRelayWorld(t)
	domain := "remote.example"
	author := &models.Account{Username: "inbox-author", Domain: &domain}
	require.NoError(t, w.conn.Create(author).Error)

	var mu sync.Mutex
	var fired, visible bool
	id := event.Register("status", "after_create", &event.Handler{Exec: func(e event.Event) bool {
		s, ok := e.Data.(*models.Status)
		if !ok || s == nil || s.AccountId != author.Id {
			return true
		}
		var n int64
		db.GetDB().Model(&models.Status{}).Where("id = ?", s.Id).Count(&n)
		mu.Lock()
		fired, visible = true, n == 1
		mu.Unlock()
		return true
	}})
	t.Cleanup(func() { event.Remove("status", "after_create", id) })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/inbox", nil)
	processInboxTransaction(context.Background(), c, "https://remote.example/activities/1", func(ctx context.Context) {
		status := &models.Status{AccountId: author.Id, Domain: &domain, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "remote"}}
		require.NoError(t, activityDB(ctx).Create(status).Error)
		mu.Lock()
		require.False(t, fired, "status event fired inside the open inbox transaction")
		mu.Unlock()
		c.JSON(202, gin.H{})
	})
	mu.Lock()
	defer mu.Unlock()
	require.True(t, fired, "status event must fire after commit")
	require.True(t, visible, "subscribers must see the committed row")
}
