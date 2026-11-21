package list

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShadowListMediaStreamPublishesMediaOnly(t *testing.T) {
	gdb, mr := setupFull(t)
	gdb = gdb.Session(&gorm.Session{SkipHooks: true})
	author := createAccount(t, gdb, "author")
	owner := createAccount(t, gdb, "owner")
	list := createList(t, gdb, owner, "media-list")
	addListMember(t, gdb, list, author)

	plain := createStatus(t, gdb, author)
	media := createStatus(t, gdb, author)
	mediaID := media.Id
	media.Attachments = models.MediaAttachments{&models.MediaAttachment{StatusId: &mediaID}}
	privateMedia := createStatus(t, gdb, author, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})
	privateID := privateMedia.Id
	privateMedia.Attachments = models.MediaAttachments{&models.MediaAttachment{StatusId: &privateID}}

	channel := feed.GetRedisChannelName(&ListTimeline{List: list, OnlyMedia: true})
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	sub := client.Subscribe(ctx, channel)
	t.Cleanup(func() { _ = sub.Close() })
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, channel).Result()
		return err == nil && counts[channel] == 1
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, Push(media, false, true))
	require.NoError(t, Push(plain, false, true))
	require.NoError(t, Push(privateMedia, false, true))

	msgs := sub.Channel()
	deadline := time.After(400 * time.Millisecond)
	gotMedia := false
	for {
		select {
		case msg := <-msgs:
			var wire struct {
				Payload struct {
					ID uint64 `json:"id"`
				} `json:"payload"`
			}
			require.NoError(t, json.Unmarshal([]byte(msg.Payload), &wire))
			require.Equal(t, media.Id, wire.Payload.ID, "list media channel leaked status %d", wire.Payload.ID)
			gotMedia = true
		case <-deadline:
			require.True(t, gotMedia, "public media post was not published on the list media channel")
			return
		}
	}
}
