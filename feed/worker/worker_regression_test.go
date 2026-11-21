package worker

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/direct"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestWorkerSkipsUnmatchedVariantDeletesAndProjectsUnlistedTagsSafely(t *testing.T) {
	w, client := workerFixture(t)
	w.Options.Shadow = false
	ctx := context.Background()

	subRemote := client.Subscribe(ctx, feed.GetRedisChannelName(&public.PublicTimeline{OnlyRemote: true}))
	defer subRemote.Close()
	_, err := subRemote.Receive(ctx)
	require.NoError(t, err)

	subLocal := client.Subscribe(ctx, feed.GetRedisChannelName(&public.PublicTimeline{OnlyLocal: true}))
	defer subLocal.Close()
	_, err = subLocal.Receive(ctx)
	require.NoError(t, err)

	subTag := client.Subscribe(ctx, feed.GetRedisChannelName(&tag.TagTimeline{Tag: "golang"}))
	defer subTag.Close()
	_, err = subTag.Receive(ctx)
	require.NoError(t, err)

	// 1. Local public text post: must publish update to public:local, and nothing to public:remote.
	require.NoError(t, w.DB.Exec("INSERT INTO statuses (id,account_id,visibility,feed_revision) VALUES (101,1,?,1)", models.StatusVisibilityPublic).Error)
	require.NoError(t, w.DB.Create(&models.FeedEvent{
		EventKey:      "s:101:1",
		Kind:          models.FeedStatusCreate,
		ObjectType:    "status",
		ObjectID:      101,
		ActorID:       1,
		ObjectVersion: 1,
		Metadata:      models.FeedEventMetadata{ActorID: 1, Visibility: models.StatusVisibilityPublic, Local: true},
	}).Error)

	drain := func() {
		for {
			s, err := w.ScanOnce(ctx)
			require.NoError(t, err)
			d, err := w.DeliverOnce(ctx)
			require.NoError(t, err)
			if !s && !d {
				break
			}
		}
	}

	drain()

	select {
	case msg := <-subLocal.Channel():
		require.Contains(t, msg.Payload, `"type":"update"`)
	case <-time.After(time.Second):
		t.Fatal("expected update on public:local")
	}

	select {
	case msg := <-subRemote.Channel():
		t.Fatalf("unexpected message on unmatched public:remote: %s", msg.Payload)
	case <-time.After(100 * time.Millisecond):
	}

	// 2. Unlisted tagged post: must enter hashtag candidate ZSet without publishing ID to public hashtag channel.
	require.NoError(t, w.DB.Exec("INSERT INTO tags (id,name) VALUES (5,'golang')").Error)
	require.NoError(t, w.DB.Exec("INSERT INTO statuses (id,account_id,visibility,feed_revision) VALUES (102,1,?,1)", models.StatusVisibilityUnlisted).Error)
	require.NoError(t, w.DB.Exec("INSERT INTO status_tags (status_id,tag_id) VALUES (102,5)").Error)
	tagTimeline := &tag.TagTimeline{Tag: "golang"}
	lease, err := feed.BeginRebuild(ctx, tagTimeline)
	require.NoError(t, err)
	require.NoError(t, feed.PublishRebuild(ctx, tagTimeline, lease, nil, true))

	require.NoError(t, w.DB.Create(&models.FeedEvent{
		EventKey:      "s:102:1",
		Kind:          models.FeedStatusCreate,
		ObjectType:    "status",
		ObjectID:      102,
		ActorID:       1,
		ObjectVersion: 1,
		Metadata:      models.FeedEventMetadata{ActorID: 1, Visibility: models.StatusVisibilityUnlisted, TagIDs: []uint64{5}, TagNames: []string{"golang"}, Local: true},
	}).Error)

	drain()

	members := client.ZRevRange(ctx, feed.TimelineKey(tagTimeline), 0, -1).Val()
	require.Contains(t, members, "102", "unlisted tagged post must be projected into tag candidate cache")

	select {
	case msg := <-subTag.Channel():
		t.Fatalf("non-public tagged status leaked onto public hashtag stream: %s", msg.Payload)
	case <-time.After(100 * time.Millisecond):
	}

	// 3. Direct status delete: must publish delete to direct:<recipient>.
	subDirect := client.Subscribe(ctx, feed.GetRedisChannelName(&direct.DirectTimeline{Account: &models.Account{Id: 2}}))
	defer subDirect.Close()
	_, err = subDirect.Receive(ctx)
	require.NoError(t, err)
	directCh := subDirect.Channel(redis.WithChannelSize(4))

	require.NoError(t, w.DB.Create(&models.FeedEvent{
		EventKey:      "s:103:2",
		Kind:          models.FeedStatusDelete,
		ObjectType:    "status",
		ObjectID:      103,
		ActorID:       1,
		ObjectVersion: 2,
		Metadata:      models.FeedEventMetadata{ActorID: 1, Visibility: models.StatusVisibilityDirect, RecipientIDs: []uint64{2}},
	}).Error)

	drain()

	select {
	case msg := <-directCh:
		require.Contains(t, msg.Payload, `"type":"delete"`)
		require.Contains(t, msg.Payload, `"id":103`)
	case <-time.After(time.Second):
		t.Fatal("expected delete event on direct channel")
	}
}
