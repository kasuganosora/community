package timelines

import (
	"encoding/json"
	"github.com/kasuganosora/community/serializer"
	"net/http"
	"strconv"
	"testing"
	"time"

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

func TestReactionSingleNotificationUsesListContract(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.MuteStatus{}, &models.MuteAccount{}, &models.FilterKeyword{}, &models.StatusContent{}, &models.SystemSetting{}))
	writer := conn.Session(&gorm.Session{SkipHooks: true})
	owner, actor := makeAccount(1), makeAccount(2)
	require.NoError(t, writer.Create(owner).Error)
	require.NoError(t, writer.Create(actor).Error)
	now := time.Now()
	status := &models.Status{Id: 1, AccountId: owner.Id, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{StatusId: 1, Content: "reaction target"}}
	require.NoError(t, writer.Create(status).Error)
	require.NoError(t, writer.Create(&models.StatusReaction{AccountID: actor.Id, StatusID: status.Id, Name: "😀"}).Error)
	n := &models.Notification{AccountId: owner.Id, FromAccountId: actor.Id, StatusId: status.Id, Type: models.NotificationTypeEmojiReaction}
	require.NoError(t, writer.Create(n).Error)
	_, serializeErr := serializer.ToNotification(n, owner)
	require.NoError(t, serializeErr)
	c, w := newTestContext(t, "GET", "/api/v1/notifications/1", "")
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(n.Id, 10)}}
	setAccount(c, owner)
	GetNotification(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var got serializer.Notification
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "emoji_reaction", got.Type)
	require.Equal(t, strconv.FormatUint(actor.Id, 10), got.Account.Id)
	require.NotNil(t, got.Status)
	require.Equal(t, "1", got.Status.Id)
	require.NotNil(t, got.EmojiReaction)
	require.Equal(t, "😀", got.EmojiReaction.Name)
}
