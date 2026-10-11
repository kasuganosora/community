package activitypub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestQueuedAnnounceRechecksOriginalWithoutDroppingValidBoost(t *testing.T) {
	w := newRelayWorld(t)
	author := &models.Account{Username: "announce-author"}
	booster := &models.Account{Username: "announce-booster"}
	domain := "remote.test"
	recipient := &models.Account{Username: "announce-reader", Domain: &domain, OriginId: e2eOrigin + "/users/reader"}
	for _, account := range []*models.Account{author, booster, recipient} {
		require.NoError(t, w.conn.Create(account).Error)
	}
	require.NoError(t, w.conn.Create(&models.Follow{AccountId: recipient.Id, FollowId: booster.Id, Status: models.FollowStatusFollowed}).Error)
	original := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "original"}}
	require.NoError(t, w.conn.Create(original).Error)
	boost := &models.Status{AccountId: booster.Id, ReblogId: &original.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, w.conn.Create(boost).Error)
	auth := DeliveryAuthorization{AccountID: booster.Id, RecipientIDs: []uint64{recipient.Id}, StatusID: boost.Id, Visibility: models.StatusVisibilityPublic, Kind: "Announce"}
	encoded, err := json.Marshal(auth)
	require.NoError(t, err)
	delivery := &Delivery{Authorization: string(encoded)}
	activity := map[string]any{"type": "Announce", "actor": accountActorURI(booster, e2eBase), "object": statusObjectURI(original, author, e2eBase)}
	_, allowed, err := authorizeQueuedDelivery(delivery, activity)
	require.NoError(t, err)
	require.True(t, allowed, "a public boost must reach its accepted remote follower at send time")
	for _, change := range []map[string]any{{"visibility": models.StatusVisibilityPrivate}, {"local_only": true}, {"scheduled_at": time.Now().Add(time.Hour)}, {"deleted_at": time.Now()}} {
		require.NoError(t, w.conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Status{}).Where("id = ?", original.Id).UpdateColumns(change).Error)
		_, allowed, err = authorizeQueuedDelivery(delivery, activity)
		require.NoError(t, err)
		require.False(t, allowed, "restricted or deleted original must not be announced")
		require.NoError(t, w.conn.Session(&gorm.Session{SkipHooks: true}).Unscoped().Model(&models.Status{}).Where("id = ?", original.Id).UpdateColumns(map[string]any{"visibility": models.StatusVisibilityPublic, "local_only": false, "scheduled_at": nil, "deleted_at": nil}).Error)
	}
	block := &models.Block{AccountId: author.Id, TargetAccountId: recipient.Id, Type: models.BlockTypeAccount}
	require.NoError(t, w.conn.Session(&gorm.Session{SkipHooks: true}).Create(block).Error)
	_, allowed, err = authorizeQueuedDelivery(delivery, activity)
	require.NoError(t, err)
	require.False(t, allowed, "original author's block must apply through boost wrapper")
	require.NoError(t, w.conn.Session(&gorm.Session{SkipHooks: true}).Delete(block).Error)
	activity["object"] = "https://wrong.example/objects/other"
	_, allowed, err = authorizeQueuedDelivery(delivery, activity)
	require.NoError(t, err)
	require.False(t, allowed, "queued object must still match the current boost target")
	activity["object"] = statusObjectURI(original, author, e2eBase)
	require.NoError(t, w.conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Status{}).Where("id = ?", original.Id).UpdateColumn("local_only", true).Error)
	auth.Kind = "AnnounceUndo"
	encoded, err = json.Marshal(auth)
	require.NoError(t, err)
	delivery.Authorization = string(encoded)
	_, allowed, err = authorizeQueuedDelivery(delivery, map[string]any{"type": "Undo", "object": activity})
	require.NoError(t, err)
	require.True(t, allowed, "revocation must still be sent after original access disappears")
}

func TestAnnouncePublishedPreservesSubsecondOrderAndUndo(t *testing.T) {
	relayDB(t)
	zone := time.FixedZone("source", 9*60*60)
	originalTime := time.Date(2026, 10, 11, 12, 0, 0, 100000000, zone)
	boostTime := time.Date(2026, 10, 11, 12, 0, 0, 900000000, zone)
	author := &models.Account{Id: 1, Username: "precise"}
	original := &models.Status{Id: 10, AccountId: author.Id, Account: author, CreatedAt: &originalTime, Visibility: models.StatusVisibilityPublic}
	boost := &models.Status{Id: 11, AccountId: author.Id, Account: author, CreatedAt: &boostTime, Visibility: models.StatusVisibilityPublic, ReblogId: &original.Id, Reblog: original}
	actorURI := accountActorURI(author, e2eBase)
	announce, announceID := announcePayload(boost, author, actorURI, "unused", false)
	published, ok := announce["published"].(string)
	require.True(t, ok)
	require.Equal(t, "2026-10-11T03:00:00.9Z", published)
	parsed, err := time.Parse(time.RFC3339Nano, published)
	require.NoError(t, err)
	require.Equal(t, time.UTC, parsed.Location())
	require.True(t, parsed.Equal(boostTime), "published must preserve the actual boost instant")
	require.False(t, parsed.Before(originalTime), "Misskey rejects Announce created before the original Note")
	undo, _ := announcePayload(boost, author, actorURI, "https://example.test/undo/1", true)
	embedded := undo["object"].(map[string]any)
	require.Equal(t, announceID, embedded["id"])
	require.Equal(t, published, embedded["published"], "Undo must retain the original Announce timestamp precision")
}
