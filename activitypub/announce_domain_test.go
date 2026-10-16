package activitypub

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// An inbound Announce from a remote actor must be stored with that actor's
// domain; otherwise the reblog is classified as local and leaks into
// timelines/public?local=true and the public:local stream.
func TestInboundAnnounceStoresRemoteDomain(t *testing.T) {
	w := newRelayWorld(t)
	domain := "maid.example"
	booster := &models.Account{Username: "luna", Domain: &domain, OriginId: "https://maid.example/users/luna"}
	author := &models.Account{Username: "announced-author"}
	require.NoError(t, w.conn.Create(booster).Error)
	require.NoError(t, w.conn.Create(author).Error)
	uri := "https://misskey.example/notes/abc"
	original := &models.Status{AccountId: author.Id, OriginUri: &uri, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "original"}}
	require.NoError(t, w.conn.Create(original).Error)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/inbox", nil)
	ctx := context.WithValue(context.Background(), actorKey, booster.OriginId)
	activity := map[string]any{"type": "Announce", "id": "https://maid.example/notes/1/activity", "object": uri}
	handleAnnounceActivity(ctx, c, activity, nil)

	var reblog models.Status
	require.NoError(t, w.conn.Where("reblog_id = ? AND account_id = ?", original.Id, booster.Id).First(&reblog).Error)
	require.NotNil(t, reblog.Domain)
	require.Equal(t, domain, *reblog.Domain)
}
