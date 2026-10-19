package streaming

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/secretredact"
	"github.com/stretchr/testify/require"
)

// A status created by a local account must reach live subscribers of the
// public, public:local and user streams, and the event must be serializable
// the moment it arrives (the subscriber reloads the row from SQL).
func TestLocalStatusCreateReachesLiveStreams(t *testing.T) {
	setupTestDB(t)
	client := setupRedisForTest(t)
	require.NoError(t, feed.ConfigureRuntime("shadow"))

	author := &models.Account{Username: "localauthor"}
	require.NoError(t, db.GetDB().Create(author).Error)

	channels := []string{
		feed.GetRedisChannelName(&public.PublicTimeline{}),
		feed.GetRedisChannelName(&public.PublicTimeline{OnlyLocal: true}),
		feed.GetRedisChannelName(&home.HomeTimeline{Account: author}),
	}
	sub := client.Subscribe(context.Background(), channels...)
	defer sub.Close()
	for range channels {
		_, err := sub.Receive(context.Background())
		require.NoError(t, err)
	}
	feed.ResetSubscriptionCache()

	type result struct {
		channel string
		err     error
	}
	results := make(chan result, 16)
	go func() {
		for msg := range sub.Channel() {
			// Serialize immediately, exactly like the websocket handler does.
			_, _, err := serializerStatusEvent(author, msg.Payload, msg.Channel)
			results <- result{msg.Channel, err}
		}
	}()

	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "hello"}}
	require.NoError(t, models.CreateStatusCommitted(db.GetDB(), status))

	got := map[string]error{}
	timeout := time.After(3 * time.Second)
	for len(got) < len(channels) {
		select {
		case r := <-results:
			got[r.channel] = r.err
		case <-timeout:
			t.Fatalf("local status reached only %v of %v", got, channels)
		}
	}
	for channel, err := range got {
		require.NoError(t, err, channel)
	}
}

func TestSubprotocolResponseHeaderEchoesOfferedProtocol(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/api/v1/streaming", nil)
	require.Nil(t, subprotocolResponseHeader(r))
	r.Header.Set("Sec-WebSocket-Protocol", "token123, other")
	require.Equal(t, "token123", subprotocolResponseHeader(r).Get("Sec-WebSocket-Protocol"))
}

func TestSubprotocolSelectionNeverEchoesBearer(t *testing.T) {
	bearer := secretredact.BearerSubprotocolPrefix + "dG9rZW4"
	for _, tc := range []struct {
		offer, mastodon, feed string
	}{
		{"community.v1, " + bearer, "community.v1", ""},
		{bearer + ", community.v1", "community.v1", ""},
		{bearer, "", ""},
		{"rawtoken", "rawtoken", ""},
	} {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Sec-WebSocket-Protocol", tc.offer)
		require.Equal(t, tc.mastodon, subprotocolResponseHeader(r).Get("Sec-WebSocket-Protocol"), tc.offer)
		want := tc.feed
		if strings.Contains(tc.offer, "community.v1") {
			want = "community.v1"
		}
		require.Equal(t, want, feedSubprotocolResponseHeader(r).Get("Sec-WebSocket-Protocol"), tc.offer)
	}
}
