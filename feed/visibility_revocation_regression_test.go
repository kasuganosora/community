package feed_test

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNeverPublicPrivateUpdateDoesNotBroadcast(t *testing.T) {
	conn, client := setupTimelineOrder(t)
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}))
	mode, reads := feed.SnapshotRuntimeForTest()
	t.Cleanup(func() { feed.RestoreRuntimeForTest(mode, reads) })
	require.NoError(t, feed.ConfigureRuntime("shadow"))

	quiet := conn.Session(&gorm.Session{SkipHooks: true})
	author := &models.Account{Id: 1, Username: "author"}
	require.NoError(t, quiet.Create(author).Error)
	now := time.Now()
	lst := &models.List{Id: 7, Title: "people", AccountId: author.Id, LastViewedAt: &now}
	require.NoError(t, quiet.Create(lst).Error)
	require.NoError(t, quiet.Create(&models.ListAccount{ListId: lst.Id, AccountId: author.Id}).Error)
	status := &models.Status{
		Id: 40, AccountId: author.Id, Visibility: models.StatusVisibilityPrivate,
		Content: models.StatusContent{Content: "secret-body", SpoilerText: "secret-cw"},
	}
	require.NoError(t, quiet.Create(status).Error)
	mediaID := status.Id
	require.NoError(t, quiet.Create(&models.MediaAttachment{StatusId: &mediaID, AccountId: author.Id, FileName: "a", FilePath: "a", ContentType: "image/png"}).Error)

	channels := []string{
		feed.GetRedisChannelName(&public.PublicTimeline{}),
		feed.GetRedisChannelName(&public.PublicTimeline{OnlyMedia: true}),
		feed.GetRedisChannelName(&public.PublicTimeline{OnlyLocal: true}),
		feed.GetRedisChannelName(&list.ListTimeline{List: lst}),
		feed.GetRedisChannelName(&list.ListTimeline{List: lst, OnlyMedia: true}),
	}
	ctx := context.Background()
	sub := client.Subscribe(ctx, channels...)
	t.Cleanup(func() { _ = sub.Close() })
	waitSubscribed(t, client, channels)

	loaded := &models.Status{}
	require.NoError(t, conn.First(loaded, status.Id).Error)
	loaded.Content.Content = "secret-body"
	loaded.Content.SpoilerText = "secret-cw"
	loaded.Language = "ja"
	event.Trigger("status", event.Event{Name: "before_update", Data: loaded})

	assertNoStreamMessages(t, sub.Channel())
}

func TestDirectTagRefreshDoesNotPublishPublic(t *testing.T) {
	conn, client := setupTimelineOrder(t)
	mode, reads := feed.SnapshotRuntimeForTest()
	t.Cleanup(func() { feed.RestoreRuntimeForTest(mode, reads) })
	require.NoError(t, feed.ConfigureRuntime("shadow"))
	quiet := conn.Session(&gorm.Session{SkipHooks: true})
	author := &models.Account{Id: 1, Username: "author"}
	require.NoError(t, quiet.Create(author).Error)
	status := &models.Status{
		Id: 60, AccountId: author.Id, Visibility: models.StatusVisibilityDirect,
		Tags:    models.Tags{{Name: "synth"}},
		Content: models.StatusContent{Content: "direct-secret"},
	}
	require.NoError(t, quiet.Create(status).Error)
	publicCh := feed.GetRedisChannelName(&public.PublicTimeline{})
	tagCh := feed.GetRedisChannelName(&tag.TagTimeline{Tag: "synth"})
	ctx := context.Background()
	sub := client.Subscribe(ctx, publicCh, tagCh)
	t.Cleanup(func() { _ = sub.Close() })
	waitSubscribed(t, client, []string{publicCh, tagCh})

	loaded := &models.Status{}
	require.NoError(t, conn.First(loaded, status.Id).Error)
	loaded.Visibility = models.StatusVisibilityDirect
	loaded.Tags = models.Tags{{Name: "synth"}}
	loaded.Content.Content = "direct-secret"
	event.Trigger("status", event.Event{Name: "before_update", Data: loaded})
	msgs := collectStreamMessages(t, sub.Channel(), 500*time.Millisecond)
	require.NotEmpty(t, msgs)
	for _, msg := range msgs {
		require.Equal(t, tagCh, msg.Channel)
		require.NotContains(t, msg.Payload, "direct-secret")
		require.Contains(t, msg.Payload, `"id":60`)
	}
}

func waitSubscribed(t *testing.T, client *redis.Client, channels []string) {
	t.Helper()
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(context.Background(), channels...).Result()
		if err != nil {
			return false
		}
		for _, channel := range channels {
			if counts[channel] == 0 {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
}

func collectStreamMessages(t *testing.T, ch <-chan *redis.Message, wait time.Duration) []*redis.Message {
	t.Helper()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var msgs []*redis.Message
	for {
		select {
		case msg := <-ch:
			if msg != nil {
				msgs = append(msgs, msg)
				timer.Reset(50 * time.Millisecond)
			}
		case <-timer.C:
			return msgs
		}
	}
}

func assertNoStreamMessages(t *testing.T, ch <-chan *redis.Message) {
	t.Helper()
	select {
	case msg := <-ch:
		if msg != nil {
			t.Fatalf("unexpected stream message on %s: %s", msg.Channel, msg.Payload)
		}
	case <-time.After(400 * time.Millisecond):
	}
}
