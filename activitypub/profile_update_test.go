package activitypub

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProfileUpdateSnapshotRollsBackAndRestrictsFollowers(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "profile_author", false)
	remote := outboundAccount(t, conn, "recipient", true)
	pending := outboundAccount(t, conn, "pending", true)
	blocked := outboundAccount(t, conn, "blocked", true)
	for i, follow := range []*models.Follow{{AccountId: remote.Id, FollowId: author.Id}, {AccountId: pending.Id, FollowId: author.Id, Status: models.FollowStatusRequested}, {AccountId: blocked.Id, FollowId: author.Id}} {
		follow.Id = uint64(100 + i)
		require.NoError(t, conn.Create(follow).Error)
	}
	require.NoError(t, conn.Create(&models.Block{Id: 100, AccountId: author.Id, TargetAccountId: blocked.Id}).Error)
	emoji := models.Emoji{Id: 100, Shortcode: "wave", Url: "https://local.example/wave.png", StaticUrl: "https://local.example/wave.png"}
	require.NoError(t, conn.Create(&emoji).Error)
	rollback := errors.New("rollback")
	require.ErrorIs(t, conn.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Model(author).Update("display_name", "uncommitted :wave:").Error)
		require.NoError(t, models.QueueFederationProfile(tx, author.Id))
		return rollback
	}), rollback)
	require.Empty(t, outboxEvents(t, conn))
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(author).Update("display_name", "committed :wave:").Error; err != nil {
			return err
		}
		return models.QueueFederationProfile(tx, author.Id)
	}))
	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, "ProfileUpdate", events[0].Kind)
	require.Equal(t, []uint64{remote.Id}, events[0].RecipientIDs)
	require.NoError(t, conn.Model(author).Update("display_name", "later unsent edit").Error)
	payload, err := profileUpdatePayload(&events[0], "https://local.example/users/profile_author", "https://local.example/activity/1")
	require.NoError(t, err)
	actor := payload["object"].(map[string]any)
	require.Equal(t, "committed :wave:", actor["name"])
	require.Len(t, actor["tag"], 1)
	require.NotEmpty(t, actor["publicKey"].(map[string]any)["publicKeyPem"])
}

func TestProfileUpdateIsSignedRetriedAndRevokedAtSendTime(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "profile_sender", false)
	remote := outboundAccount(t, conn, "profile_receiver", true)
	follow := models.Follow{Id: 110, AccountId: remote.Id, FollowId: author.Id}
	require.NoError(t, conn.Create(&follow).Error)
	require.NoError(t, models.QueueFederationProfile(conn, author.Id))
	key, err := localAccountKey(author)
	require.NoError(t, err)
	block, _ := pem.Decode([]byte(key.PublicKey))
	public, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	posts := 0
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return outboundResponse(200, `{"id":"`+remote.OriginId+`","inbox":"https://remote.example/inbox"}`), nil
		}
		require.NoError(t, VerifyRequest(r, public.(*rsa.PublicKey)))
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var activity map[string]any
		require.NoError(t, json.Unmarshal(raw, &activity))
		require.Equal(t, "Update", activity["type"])
		require.Equal(t, "Person", activity["object"].(map[string]any)["type"])
		require.Contains(t, activity["object"].(map[string]any), "name")
		require.Nil(t, activity["object"].(map[string]any)["name"])
		require.Contains(t, activity["object"].(map[string]any), "summary")
		require.Nil(t, activity["object"].(map[string]any)["summary"])
		require.Contains(t, activity["object"].(map[string]any), "tag")
		posts++
		return outboundResponse(503, "retry later"), nil
	})}
	worker := &OutboundWorker{}
	worker.processPending()
	var deliveries []Delivery
	require.NoError(t, conn.Find(&deliveries).Error)
	require.Len(t, deliveries, 1)
	queue := NewDeliveryQueue(nil, "", 0)
	queue.processPending()
	require.Equal(t, 1, posts)
	var saved Delivery
	require.NoError(t, conn.First(&saved, deliveries[0].ID).Error)
	require.NotEqual(t, DeliveryStatusDelivered, saved.Status)
	// A queued retry loses authorization after the recipient unfollows.
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Delete(&follow).Error)
	var activity map[string]any
	require.NoError(t, json.Unmarshal([]byte(saved.Payload), &activity))
	_, allowed, err := authorizeQueuedDelivery(&saved, activity)
	require.NoError(t, err)
	require.False(t, allowed)
	expanded, err := worker.expand(context.Background(), &outboxEvents(t, conn)[0])
	require.NoError(t, err)
	require.Empty(t, expanded)
}

func TestInboundProfileUpdateInvalidatesOnlyAfterCommitAndAllowsClearing(t *testing.T) {
	conn := emojiDB(t)
	actor, err := createRemoteAccount(conn, &Actor{ID: "https://remote.example/users/clear-profile", PreferredUsername: "clear-profile", Name: "Old :party:", Summary: "Old biography", Tag: sampleEmojiTags("https://cdn.example/p.png")})
	require.NoError(t, err)
	resolver := getDefaultResolver()
	resolver.storeAccountCache(actor.OriginId, &actor)
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	update := map[string]any{"object": map[string]any{"id": actor.OriginId, "type": "Person", "name": nil, "summary": nil, "tag": []any{}}}
	invoke := func(fail bool) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		processInboxTransaction(ctx, c, "", func(ctx context.Context) {
			handleUpdateActivity(ctx, c, update, nil)
			_, exists := resolver.loadAccountCache(actor.OriginId)
			require.True(t, exists, "cache must not be invalidated before commit")
			if fail {
				c.Status(500)
			}
		})
		return w.Code
	}
	require.Equal(t, 500, invoke(true))
	_, exists := resolver.loadAccountCache(actor.OriginId)
	require.True(t, exists)
	var saved models.Account
	require.NoError(t, conn.Preload("Emojis").First(&saved, actor.Id).Error)
	require.Equal(t, "Old :party:", saved.DisplayName)
	require.Len(t, saved.Emojis, 1)
	require.Equal(t, 200, invoke(false))
	_, exists = resolver.loadAccountCache(actor.OriginId)
	require.False(t, exists)
	require.NoError(t, conn.Preload("Emojis").First(&saved, actor.Id).Error)
	require.Empty(t, saved.DisplayName)
	require.Empty(t, saved.Note)
	require.Empty(t, saved.Emojis)
}

func TestInboundSparseProfileUpdatePreservesOmittedFields(t *testing.T) {
	conn := emojiDB(t)
	actor, err := createRemoteAccount(conn, &Actor{ID: "https://remote.example/users/sparse-profile", PreferredUsername: "sparse-profile", Name: "Old name", Summary: "Biography :party:", Tag: sampleEmojiTags("https://cdn.example/p.png")})
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	for _, patch := range []map[string]any{{"name": "New name"}, {"tag": sampleEmojiTags("https://cdn.example/changed.png")}} {
		object := map[string]any{"id": actor.OriginId, "type": "Person"}
		for k, v := range patch {
			object[k] = v
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		handleUpdateActivity(ctx, c, map[string]any{"object": object}, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var saved models.Account
		require.NoError(t, conn.Preload("Emojis").First(&saved, actor.Id).Error)
		require.Equal(t, "New name", saved.DisplayName)
		require.Equal(t, "Biography :party:", saved.Note)
		require.Len(t, saved.Emojis, 1)
	}
}
