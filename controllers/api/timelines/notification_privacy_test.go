package timelines

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotificationsDenyHistoricalBlockedSender(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	writer := conn.Session(&gorm.Session{SkipHooks: true})
	author, viewer := makeAccount(1), makeAccount(2)
	require.NoError(t, writer.Create(author).Error)
	require.NoError(t, writer.Create(viewer).Error)
	n := &models.Notification{AccountId: viewer.Id, FromAccountId: author.Id, Type: models.NotificationTypeFollow}
	require.NoError(t, writer.Create(n).Error)
	require.NoError(t, writer.Create(&models.Block{AccountId: author.Id, TargetAccountId: viewer.Id, Type: models.BlockTypeAccount}).Error)
	c, w := newTestContext(t, "GET", "/api/v1/notifications/"+strconv.FormatUint(n.Id, 10), "")
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(n.Id, 10)}}
	setAccount(c, viewer)
	GetNotification(c)
	require.Equal(t, http.StatusNotFound, w.Code)
	c, w = newTestContext(t, "GET", "/api/v1/notifications", "")
	setAccount(c, viewer)
	NotificationTimeline(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, "[]", w.Body.String())
}
