package activitypub

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestLocalOnlyStaysOnLocalWebAndNeverFederates(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "local_only_author", false)
	remote := outboundAccount(t, conn, "remote_reader", true)
	require.NoError(t, conn.Create(&models.Follow{AccountId: remote.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	status := &models.Status{AccountId: author.Id, LocalOnly: true, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "local only payload"}}
	require.NoError(t, conn.Create(status).Error)
	require.Empty(t, outboxEvents(t, conn))
	visible, err := status.CanViewEx(conn, nil)
	require.NoError(t, err)
	require.True(t, visible, "local public webpage remains readable")
	visible, err = status.CanViewEx(conn, author)
	require.NoError(t, err)
	require.True(t, visible)
	visible, err = status.CanViewEx(conn, remote)
	require.NoError(t, err)
	require.False(t, visible)
	_, err = StatusToNote(status)
	require.Error(t, err)
	router := gin.New()
	router.GET("/users/:username/statuses/:id", StatusObject)
	router.GET("/users/:username/outbox", Outbox)
	base := systemsettings.GetBaseURL() + "/users/" + author.Username
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", base+"/statuses/"+strconv.FormatUint(status.Id, 10), nil))
	require.Equal(t, 404, w.Code)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", base+"/outbox?page=true", nil))
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "local only payload")
	require.NoError(t, conn.Model(status).Update("visibility", models.StatusVisibilityDirect).Error)
	status.Visibility = models.StatusVisibilityDirect
	visible, err = status.CanViewEx(conn, nil)
	require.NoError(t, err)
	require.False(t, visible, "local only must not broaden direct ACL")
}
