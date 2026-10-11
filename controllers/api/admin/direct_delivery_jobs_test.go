package admin

import (
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestDirectJobsMetadataOnlyAndPermission(t *testing.T) {
	conn, account := operationsDB(t)
	require.NoError(t, conn.AutoMigrate(&homeserver.MatrixDMOutbound{}))
	require.NoError(t, conn.Create(&homeserver.MatrixDMOutbound{EventID: "$event", RoomID: "PRIVATE_ROOM", SourceStatusID: 1, SenderAccountID: 1, State: "failed", LastError: "PRIVATE_ERROR_BODY", NextAttemptAt: time.Now()}).Error)
	c, w := newGetContext(t, nil, "kind=matrix-direct")
	c.Set("account", account)
	FederationJobs(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "direct_bridge_unavailable")
	require.NotContains(t, w.Body.String(), "PRIVATE_ROOM")
	require.NotContains(t, w.Body.String(), "PRIVATE_ERROR_BODY")
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.User{}).Where("id = ?", 1).Update("is_admin", false).Error)
	c, w = newGetContext(t, nil, "kind=matrix-direct")
	c.Set("account", account)
	FederationJobs(c)
	require.Equal(t, 403, w.Code)
}
