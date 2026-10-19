package streaming

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/direct"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func streamPrivacyDB(t *testing.T) (*gorm.DB, *models.Account, *models.Account) {
	t.Helper()
	setupTestDB(t)
	conn := db.GetDB().Session(&gorm.Session{SkipHooks: true})
	author := &models.Account{Id: 1, Username: "author"}
	viewer := &models.Account{Id: 2, Username: "viewer"}
	require.NoError(t, conn.Create([]*models.Account{author, viewer, {Id: 3, Username: "outsider"}}).Error)
	return conn, author, viewer
}

func streamPost(t *testing.T, conn *gorm.DB, id uint64, visibility uint8, text string) *models.Status {
	t.Helper()
	status := &models.Status{Id: id, AccountId: 1, Visibility: visibility, Content: models.StatusContent{Content: text}}
	require.NoError(t, conn.Create(status).Error)
	return status
}

func TestStreamReloadsSQLRatherThanQueuedBody(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	status := streamPost(t, conn, 10, models.StatusVisibilityPublic, "current SQL body")
	queued := *status
	queued.Content.Content = "stale queued secret"
	payload := mustEventJSON(t, "update", &queued)
	_, out, err := serializerStatusEvent(viewer, payload)
	require.NoError(t, err)
	require.Contains(t, out, "current SQL body")
	require.NotContains(t, out, "stale queued secret")
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 10).Update("visibility", models.StatusVisibilityDirect).Error)
	_, out, err = serializerStatusEvent(viewer, payload)
	require.ErrorIs(t, err, errEventNotVisible)
	require.Empty(t, out)
	require.NoError(t, conn.Where("id = ?", 10).Delete(&models.Status{}).Error)
	_, _, err = serializerStatusEvent(viewer, payload)
	require.ErrorIs(t, err, errEventNotVisible)
}

func TestStreamRevokesPrivateFollowerAndReverseBlock(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	status := streamPost(t, conn, 10, models.StatusVisibilityPrivate, "private")
	require.NoError(t, conn.Create(&models.Follow{AccountId: viewer.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	payload := mustEventJSON(t, "update", status)
	_, _, err := serializerStatusEvent(viewer, payload)
	require.NoError(t, err)
	require.NoError(t, conn.Where("account_id = ?", viewer.Id).Delete(&models.Follow{}).Error)
	_, _, err = serializerStatusEvent(viewer, payload)
	require.ErrorIs(t, err, errEventNotVisible)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 10).Update("visibility", models.StatusVisibilityPublic).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: author.Id, TargetAccountId: viewer.Id, Type: models.BlockTypeAccount}).Error)
	_, _, err = serializerStatusEvent(viewer, payload)
	require.ErrorIs(t, err, errEventNotVisible)
}

func TestStreamEmbeddedOriginalAndQuoteUseCurrentPermissions(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	original := streamPost(t, conn, 10, models.StatusVisibilityDirect, "do not disclose")
	boost := &models.Status{Id: 11, AccountId: 3, Visibility: models.StatusVisibilityPublic, ReblogId: &original.Id}
	require.NoError(t, conn.Create(boost).Error)
	_, _, err := serializerStatusEvent(viewer, mustEventJSON(t, "update", boost))
	require.ErrorIs(t, err, errEventNotVisible)
	quote := streamPost(t, conn, 12, models.StatusVisibilityPublic, "public commentary")
	require.NoError(t, conn.Model(quote).Update("quote_id", original.Id).Error)
	_, out, err := serializerStatusEvent(viewer, mustEventJSON(t, "update", quote))
	require.NoError(t, err)
	require.Contains(t, out, "public commentary")
	require.NotContains(t, out, "do not disclose")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &decoded))
	require.Nil(t, decoded["quote"])
}

func TestNotificationStreamOwnerBlockAndCurrentSQL(t *testing.T) {
	viewer := seedStreamNotification(t)
	conn := db.GetDB().Session(&gorm.Session{SkipHooks: true})
	payload := `{"payload":{"id":"42","type":"mention","account":{"id":"999","username":"stale sender"}}}`
	_, out, err := serializerNotificationEvent(viewer, payload)
	require.NoError(t, err)
	require.Contains(t, out, "current-sender")
	require.NotContains(t, out, "stale sender")
	_, _, err = serializerNotificationEvent(&models.Account{Id: 1}, payload)
	require.ErrorIs(t, err, errEventNotVisible)
	require.NoError(t, conn.Create(&models.Block{AccountId: 1, TargetAccountId: viewer.Id, Type: models.BlockTypeAccount}).Error)
	_, _, err = serializerNotificationEvent(viewer, payload)
	require.ErrorIs(t, err, errEventNotVisible)
}

func TestConversationRedisJSONFiltersHiddenBranchesAndRevocation(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	conversation := &models.Conversation{Id: 7, AccountsIds: []uint64{1, 2, 3}}
	require.NoError(t, conn.Create(conversation).Error)
	first := streamPost(t, conn, 10, models.StatusVisibilityDirect, "allowed direct message")
	hidden := streamPost(t, conn, 11, models.StatusVisibilityDirect, "hidden branch message")
	for _, s := range []*models.Status{first, hidden} {
		require.NoError(t, conn.Model(s).Update("conversation_id", conversation.Id).Error)
	}
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: first.Id, AccountId: viewer.Id}).Error)
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: hidden.Id, AccountId: 3}).Error)
	payload := `{"type":"conversation","payload":{"id":7,"accounts_ids":[1,2,3],"statuses_ids":[10,11]}}`
	typ, out, err := serializerConversationEvent(viewer, payload)
	require.NoError(t, err)
	require.Equal(t, "conversation", typ)
	require.Contains(t, out, "allowed direct message")
	require.NotContains(t, out, "hidden branch message")
	require.NotContains(t, out, "outsider")
	require.NoError(t, conn.Create(&models.Block{AccountId: author.Id, TargetAccountId: viewer.Id, Type: models.BlockTypeAccount}).Error)
	_, out, err = serializerConversationEvent(viewer, payload)
	require.Error(t, err)
	require.Empty(t, out)
	_, _, err = serializerConversationEvent(viewer, `{"payload":null}`)
	require.Error(t, err)
}

func TestLegacyStreamScopesPrefixAndListRevocation(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	channel, err := getChannelName(viewer, &StreamingRequest{Type: "subscribe", Stream: "public:local:media"})
	require.NoError(t, err)
	require.Equal(t, "subscribed:feed:public:media:local", channel)
	tuple, _, err := streamForChannel(viewer, channel)
	require.NoError(t, err)
	require.Equal(t, []string{"public:local:media"}, tuple)
	tuple, _, err = streamForChannel(viewer, "subscribed:feed:home:2")
	require.NoError(t, err)
	require.Equal(t, []string{"user"}, tuple)
	_, _, err = streamForChannel(viewer, "subscribed:feed:home:1")
	require.Error(t, err)
	list := &models.List{Id: 7, AccountId: author.Id, Title: "private list", Visibility: models.ListVisibilityPrivate}
	require.NoError(t, conn.Create(list).Error)
	require.NoError(t, conn.Create(&models.ListFollowAccount{ListId: list.Id, AccountId: viewer.Id}).Error)
	channel, err = getChannelName(viewer, &StreamingRequest{Type: "subscribe", Stream: "list", List: "7"})
	require.NoError(t, err)
	tuple, _, err = streamForChannel(viewer, channel)
	require.NoError(t, err)
	require.Equal(t, []string{"list", "7"}, tuple)
	require.NoError(t, conn.Where("list_id = ?", list.Id).Delete(&models.ListFollowAccount{}).Error)
	_, _, err = streamForChannel(viewer, channel)
	require.ErrorIs(t, err, errEventNotVisible)
}

func TestNotificationStreamDropsRevokedDirectStatus(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	status := streamPost(t, conn, 10, models.StatusVisibilityDirect, "private notification body")
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: viewer.Id}).Error)
	require.NoError(t, conn.Create(&models.Notification{Id: 42, AccountId: viewer.Id, FromAccountId: author.Id, StatusId: status.Id, Type: models.NotificationTypeMention}).Error)
	payload := `{"payload":{"id":"42"}}`
	_, out, err := serializerNotificationEvent(viewer, payload)
	require.NoError(t, err)
	require.Contains(t, out, "private notification body")
	require.NoError(t, conn.Where("status_id = ? AND account_id = ?", status.Id, viewer.Id).Delete(&models.StatusToAccount{}).Error)
	_, out, err = serializerNotificationEvent(viewer, payload)
	require.ErrorIs(t, err, errEventNotVisible)
	require.Empty(t, out)
}

func TestDirectSSEConsumesRealRedisJSONString(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	client := setupRedisForTest(t)
	conversation := &models.Conversation{Id: 7, AccountsIds: []uint64{1, 2}}
	require.NoError(t, conn.Create(conversation).Error)
	status := streamPost(t, conn, 10, models.StatusVisibilityDirect, "SSE private reply")
	require.NoError(t, conn.Model(status).Update("conversation_id", conversation.Id).Error)
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: viewer.Id}).Error)
	router := gin.New()
	router.GET("/direct", func(c *gin.Context) { c.Set("account", viewer); DirectStreaming(c) })
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/direct", nil)
	require.NoError(t, err)
	response, err := server.Client().Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	channel := feed.GetRedisChannelName(&direct.DirectTimeline{Account: viewer})
	require.Eventually(t, func() bool {
		counts, e := client.PubSubNumSub(ctx, channel).Result()
		return e == nil && counts[channel] > 0
	}, time.Second, time.Millisecond)
	require.NoError(t, client.Publish(ctx, channel, `{"type":"conversation","payload":{"id":7}}`).Err())
	scanner := bufio.NewScanner(response.Body)
	skipSSEPadding(t, scanner)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		lines = append(lines, line)
	}
	require.NoError(t, scanner.Err())
	body := strings.Join(lines, "\n")
	require.Contains(t, body, "event: conversation")
	require.Contains(t, body, "SSE private reply")
}
