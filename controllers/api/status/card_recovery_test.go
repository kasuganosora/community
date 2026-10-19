package status

import (
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCardEndpointAuthorizationAndCanonicalPreview(t *testing.T) {
	t.Setenv("LINK_CARDS_ENABLED", "false")
	conn, fix, owner := replacementDB(t)
	other := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "card_outsider" }), func(a *models.Account) { a.Username = "card_outsider"; a.Acct = "card_outsider" })
	status := fix.CreateStatus(owner, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
		s.Content = models.StatusContent{Content: "https://example.org/page"}
	})
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.StatusContent{}).Where("status_id = ?", status.Id).Select("card").Updates(&models.StatusContent{Card: map[string]any{"url": "https://example.org/page", "title": "Actual title", "type": "unexpected", "image": "https://user:password@example.org/image.png"}}).Error)
	call := func(viewer *models.Account) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/statuses/"+strconv.FormatUint(status.Id, 10)+"/card", nil)
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(status.Id, 10)}}
		if viewer != nil {
			c.Set("account", viewer)
		}
		safeHandler(c, Card)
		return w
	}
	require.Equal(t, 404, call(other).Code)
	require.Equal(t, 404, call(nil).Code)
	w := call(owner)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"type":"link"`)
	require.Contains(t, w.Body.String(), `"image":null`)
	require.NotContains(t, w.Body.String(), "password")
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	now := time.Now()
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", status.Id).UpdateColumn("scheduled_at", now).Error)
	require.Equal(t, 404, call(owner).Code)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", status.Id).UpdateColumn("scheduled_at", nil).Error)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", status.Id).UpdateColumn("deleted_at", now).Error)
	require.Equal(t, 404, call(owner).Code)
}
