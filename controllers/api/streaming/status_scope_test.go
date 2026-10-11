package streaming

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestStreamHomeMatchesCurrentMembershipAndMutes(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	setupRedisForTest(t)
	status := streamPost(t, conn, 10, models.StatusVisibilityPublic, "home membership")
	payload := mustEventJSON(t, "update", status)
	channel := "subscribed:feed:home:2"
	_, _, err := serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible, "readable public objects do not automatically belong in home")
	require.NoError(t, conn.Create(&models.Follow{AccountId: viewer.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.NoError(t, err)
	mute := &models.MuteAccount{AccountId: viewer.Id, TargetId: author.Id}
	require.NoError(t, conn.Create(mute).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible)
	expired := time.Now().Add(-time.Minute)
	require.NoError(t, conn.Model(mute).Update("expired_at", expired).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.NoError(t, err)
	require.NoError(t, conn.Where("account_id = ?", viewer.Id).Delete(&models.Follow{}).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible)
}

func TestStreamListRechecksMembershipAndReplyPolicy(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	setupRedisForTest(t)
	list := &models.List{Id: 7, AccountId: viewer.Id, Title: "read list", RepliesPolicy: models.ListRepliesPolicyNone}
	require.NoError(t, conn.Create(list).Error)
	require.NoError(t, conn.Table("list_accounts").Create(map[string]any{"list_id": list.Id, "account_id": author.Id}).Error)
	parent := streamPost(t, conn, 9, models.StatusVisibilityPublic, "parent")
	reply := streamPost(t, conn, 10, models.StatusVisibilityPublic, "reply")
	require.NoError(t, conn.Model(reply).Updates(map[string]any{"in_reply_to_id": parent.Id, "in_reply_to_account_id": author.Id}).Error)
	payload := mustEventJSON(t, "status.update", reply)
	channel := "subscribed:feed:list:v2:7"
	_, _, err := serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible)
	require.NoError(t, conn.Model(list).Update("replies_policy", models.ListRepliesPolicyList).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.NoError(t, err)
	require.NoError(t, conn.Model(list).Update("replies_policy", models.ListRepliesPolicyFollowed).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible)
	require.NoError(t, conn.Create(&models.Follow{AccountId: viewer.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.NoError(t, err)
	require.NoError(t, conn.Where("list_id = ?", list.Id).Delete(&models.ListAccount{}).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible)
}

func TestStreamPublicAndTagUseActualMembershipAndFilterContext(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	setupRedisForTest(t)
	status := streamPost(t, conn, 10, models.StatusVisibilityPublic, "context keyword")
	payload := mustEventJSON(t, "update", status)
	require.NoError(t, conn.Create(&models.Tag{Id: 5, Name: "scope", NameKey: "scope"}).Error)
	channel := "subscribed:feed:hashtag:scope"
	_, _, err := serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible)
	require.NoError(t, conn.Table("status_tags").Create(map[string]any{"status_id": status.Id, "tag_id": 5}).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.NoError(t, err)
	_, _, err = serializerStatusEvent(viewer, payload, "subscribed:feed:public:remote")
	require.ErrorIs(t, err, errEventNotVisible, "local post cannot enter remote-only stream")
	_, _, err = serializerStatusEvent(viewer, payload, "subscribed:feed:public:media")
	require.ErrorIs(t, err, errEventNotVisible, "post without attachment cannot enter media-only stream")
	require.NoError(t, conn.Create(&models.Filter{AccountId: viewer.Id, Title: "public rule", Context: []string{"public"}, FilterAction: models.FilterActionHide, Keywords: []*models.FilterKeyword{{Keyword: "keyword"}}}).Error)
	_, out, err := serializerStatusEvent(viewer, payload, "subscribed:feed:public:local")
	require.NoError(t, err)
	var decoded struct {
		Filtered []struct {
			Filter struct {
				FilterAction string `json:"filter_action"`
			} `json:"filter"`
		} `json:"filtered"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &decoded))
	require.Len(t, decoded.Filtered, 1)
	require.Equal(t, "hide", decoded.Filtered[0].Filter.FilterAction)
	require.NoError(t, conn.Create(&models.MuteAccount{AccountId: viewer.Id, TargetId: author.Id}).Error)
	_, _, err = serializerStatusEvent(viewer, payload, channel)
	require.ErrorIs(t, err, errEventNotVisible)
}
