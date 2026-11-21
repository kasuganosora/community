package feed_test

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRealSaveWithOneConnectionDoesNotDeadlock(t *testing.T) {
	conn, client := setupTimelineOrder(t)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}))
	mode, reads := feed.SnapshotRuntimeForTest()
	t.Cleanup(func() { feed.RestoreRuntimeForTest(mode, reads) })
	require.NoError(t, feed.ConfigureRuntime("shadow"))

	quiet := conn.Session(&gorm.Session{SkipHooks: true})
	author := &models.Account{Id: 1, Username: "author"}
	require.NoError(t, quiet.Create(author).Error)
	now := time.Now()
	lst := &models.List{Id: 9, Title: "people", AccountId: author.Id, LastViewedAt: &now}
	require.NoError(t, quiet.Create(lst).Error)
	require.NoError(t, quiet.Create(&models.ListAccount{ListId: lst.Id, AccountId: author.Id}).Error)

	never := &models.Status{
		Id: 70, AccountId: author.Id, Visibility: models.StatusVisibilityPrivate,
		Content: models.StatusContent{Content: "never-public-body", SpoilerText: "never-public-cw"},
	}
	plain := &models.Status{
		Id: 71, AccountId: author.Id, Visibility: models.StatusVisibilityPublic,
		Content: models.StatusContent{Content: "public-body", SpoilerText: "public-cw"},
	}
	unlisted := &models.Status{Id: 72, AccountId: author.Id, Visibility: models.StatusVisibilityUnlisted}
	withMedia := &models.Status{Id: 73, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, quiet.Create(never).Error)
	require.NoError(t, quiet.Create(plain).Error)
	require.NoError(t, quiet.Create(unlisted).Error)
	require.NoError(t, quiet.Create(withMedia).Error)
	mediaID := withMedia.Id
	require.NoError(t, quiet.Create(&models.MediaAttachment{
		StatusId: &mediaID, AccountId: author.Id, FileName: "a", FilePath: "a", ContentType: "image/png",
	}).Error)

	all := feed.GetRedisChannelName(&public.PublicTimeline{})
	media := feed.GetRedisChannelName(&public.PublicTimeline{OnlyMedia: true})
	local := feed.GetRedisChannelName(&public.PublicTimeline{OnlyLocal: true})
	remote := feed.GetRedisChannelName(&public.PublicTimeline{OnlyRemote: true})
	listAll := feed.GetRedisChannelName(&list.ListTimeline{List: lst})
	listMedia := feed.GetRedisChannelName(&list.ListTimeline{List: lst, OnlyMedia: true})
	channels := []string{all, media, local, remote, listAll, listMedia}
	sub := client.Subscribe(t.Context(), channels...)
	t.Cleanup(func() { _ = sub.Close() })
	waitSubscribed(t, client, channels)

	save := func(status *models.Status) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- conn.Save(status).Error }()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Save blocked on a one-connection pool")
		}
	}

	loaded := &models.Status{}
	require.NoError(t, conn.Where("id = ?", never.Id).First(loaded).Error)
	loaded.Language = "ja"
	loaded.Content.Content = "never-public-body"
	loaded.Content.SpoilerText = "never-public-cw"
	save(loaded)
	assertNoStreamMessages(t, sub.Channel())

	loaded = &models.Status{}
	require.NoError(t, conn.Where("id = ?", plain.Id).First(loaded).Error)
	loaded.Visibility = models.StatusVisibilityPrivate
	loaded.Content.Content = "public-body"
	loaded.Content.SpoilerText = "public-cw"
	save(loaded)
	plainMsgs := collectStreamMessages(t, sub.Channel(), 700*time.Millisecond)
	require.NotEmpty(t, plainMsgs)
	seen := map[string]int{}
	for _, msg := range plainMsgs {
		require.NotContains(t, msg.Payload, "public-body")
		require.NotContains(t, msg.Payload, "public-cw")
		require.NotContains(t, msg.Payload, `"visibility"`)
		require.Contains(t, msg.Payload, `"type":"delete"`)
		require.Contains(t, msg.Payload, `"id":71`)
		seen[msg.Channel]++
	}
	require.Equal(t, map[string]int{all: 1, local: 1, listAll: 1}, seen)

	loaded = &models.Status{}
	require.NoError(t, conn.Where("id = ?", unlisted.Id).First(loaded).Error)
	loaded.Visibility = models.StatusVisibilityPrivate
	save(loaded)
	unlistedMsgs := collectStreamMessages(t, sub.Channel(), 700*time.Millisecond)
	seen = map[string]int{}
	for _, msg := range unlistedMsgs {
		require.Contains(t, msg.Payload, `"id":72`)
		require.Contains(t, msg.Payload, `"type":"delete"`)
		seen[msg.Channel]++
	}
	require.Equal(t, map[string]int{listAll: 1}, seen)

	loaded = &models.Status{}
	require.NoError(t, conn.Where("id = ?", withMedia.Id).First(loaded).Error)
	loaded.Visibility = models.StatusVisibilityPrivate
	save(loaded)
	mediaMsgs := collectStreamMessages(t, sub.Channel(), 700*time.Millisecond)
	seen = map[string]int{}
	for _, msg := range mediaMsgs {
		require.Contains(t, msg.Payload, `"id":73`)
		require.Contains(t, msg.Payload, `"type":"delete"`)
		seen[msg.Channel]++
	}
	require.Equal(t, map[string]int{all: 1, local: 1, media: 1, listAll: 1, listMedia: 1}, seen)
}
