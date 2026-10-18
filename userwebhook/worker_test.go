package userwebhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func hookDB(t *testing.T) *gorm.DB {
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := db.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	require.NoError(t, db.AutoMigrate(&models.UserWebhook{}, &models.UserWebhookDelivery{}, &models.FeedEvent{}))
	return db
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOwnEventsAreTransactionalScopedAndFiltered(t *testing.T) {
	db := hookDB(t)
	for _, hook := range []models.UserWebhook{
		{ID: "mine", AccountID: 1, Enabled: true, Events: []string{"note", "noteUpdated", "noteDeleted"}},
		{ID: "another", AccountID: 2, Enabled: true, Events: []string{"note"}},
		{ID: "paused", AccountID: 1, Enabled: false, Events: []string{"note"}},
		{ID: "follow-only", AccountID: 1, Enabled: true, Events: []string{"follow"}},
	} {
		require.NoError(t, db.Create(&hook).Error)
	}
	for index, kind := range []string{models.FeedStatusCreate, models.FeedStatusRefresh, models.FeedStatusDelete} {
		require.NoError(t, db.Create(&models.FeedEvent{EventKey: kind, Kind: kind, ActorID: 1, ObjectID: 99, ObjectVersion: uint64(index + 1), Metadata: models.FeedEventMetadata{Visibility: models.StatusVisibilityDirect}}).Error)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Create(&models.FeedEvent{EventKey: "rollback", Kind: models.FeedStatusCreate, ActorID: 1, ObjectID: 100}).Error)
		return errors.New("rollback")
	})
	require.Error(t, err)
	var rows []models.UserWebhookDelivery
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 3)
	for _, row := range rows {
		require.Equal(t, "mine", row.WebhookID)
		require.Contains(t, row.Payload, `"noteId":"99"`)
	}
}

func TestDeliverySignatureRetriesAndDisabledSubscription(t *testing.T) {
	db := hookDB(t)
	hook := models.UserWebhook{ID: "mine", AccountID: 1, URL: "https://receiver.example/hook", Secret: "secret", Enabled: true, Events: []string{"note"}}
	require.NoError(t, db.Create(&hook).Error)
	require.NoError(t, models.QueueUserWebhook(db, 1, "source", "note", map[string]any{"noteId": "7"}))
	var row models.UserWebhookDelivery
	require.NoError(t, db.First(&row).Error)
	calls := 0
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		payload, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, adminwebhook.Sign(hook.Secret, req.Header.Get("X-Community-Timestamp"), row.ID, string(payload)), req.Header.Get("X-Community-Signature"))
		require.Equal(t, row.ID, req.Header.Get("Idempotency-Key"))
		code := 503
		if calls > 1 {
			code = 204
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	require.NoError(t, process(context.Background(), db, row.ID, client))
	require.NoError(t, db.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "pending", row.State)
	require.Equal(t, 1, row.Attempts)
	require.NotEmpty(t, row.Payload)
	require.NoError(t, process(context.Background(), db, row.ID, client))
	require.Equal(t, 1, calls)
	require.NoError(t, db.Model(&row).Update("next_attempt_at", time.Now().Add(-time.Second)).Error)
	require.NoError(t, process(context.Background(), db, row.ID, client))
	require.NoError(t, db.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "delivered", row.State)
	require.Empty(t, row.Payload)
	require.NoError(t, process(context.Background(), db, row.ID, client))
	require.Equal(t, 2, calls)
	require.NoError(t, models.QueueUserWebhook(db, 1, "second", "note", map[string]any{}))
	var pending models.UserWebhookDelivery
	require.NoError(t, db.Where("state = ?", "pending").First(&pending).Error)
	require.NoError(t, db.Model(&hook).Update("enabled", false).Error)
	require.NoError(t, process(context.Background(), db, pending.ID, client))
	require.Equal(t, 2, calls)
	require.NoError(t, db.First(&pending, "id = ?", pending.ID).Error)
	require.Equal(t, "cancelled", pending.State)
}

func TestPersonalNotificationTriggersDoNotLeakToOtherOwners(t *testing.T) {
	db := hookDB(t)
	// Minimal source tables keep this test focused on real event routing.
	require.NoError(t, db.Exec("CREATE TABLE notifications (id BIGINT PRIMARY KEY, type INTEGER, account_id BIGINT, from_account_id BIGINT, status_id BIGINT)").Error)
	require.NoError(t, db.Exec("CREATE TABLE statuses (id BIGINT PRIMARY KEY, in_reply_to_account_id BIGINT)").Error)
	require.NoError(t, db.Exec("INSERT INTO statuses (id, in_reply_to_account_id) VALUES (99, 1)").Error)
	for _, id := range []string{"owner", "outsider"} {
		account := uint64(1)
		if id == "outsider" {
			account = 2
		}
		require.NoError(t, db.Create(&models.UserWebhook{ID: id, AccountID: account, Enabled: true, Events: models.UserWebhookEvents}).Error)
	}
	for i, kind := range []int{models.NotificationTypeFollow, models.NotificationTypeReblog, models.NotificationTypeFavourite, models.NotificationTypeEmojiReaction, models.NotificationTypeMention, models.NotificationTypePoll} {
		id := uint64(i + 1)
		require.NoError(t, db.Exec("INSERT INTO notifications (id, type, account_id, from_account_id, status_id) VALUES (?, ?, 1, 3, 99)", id, kind).Error)
		require.NoError(t, db.Create(&models.FeedEvent{EventKey: fmt.Sprint("notification:", id), Kind: models.FeedNotification, ObjectID: id, ActorID: 3}).Error)
	}
	var rows []models.UserWebhookDelivery
	require.NoError(t, db.Find(&rows).Error)
	names := map[string]int{}
	for _, row := range rows {
		require.Equal(t, "owner", row.WebhookID)
		names[row.Event]++
	}
	require.Equal(t, map[string]int{"followed": 1, "renote": 1, "reaction": 2, "mention": 1, "reply": 1}, names)
}

func TestCancelledClaimCannotSendOldEventToChangedDestination(t *testing.T) {
	db := hookDB(t)
	hook := models.UserWebhook{ID: "mine", AccountID: 1, URL: "https://old.example/hook", Secret: "secret", Enabled: true, Events: []string{"note"}}
	require.NoError(t, db.Create(&hook).Error)
	require.NoError(t, models.QueueUserWebhook(db, 1, "source", "note", map[string]any{"noteId": "7"}))
	var row models.UserWebhookDelivery
	require.NoError(t, db.First(&row).Error)
	interrupted := false
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("test:change_destination_after_claim", func(tx *gorm.DB) {
		if interrupted || tx.Statement.Table != "user_webhook_deliveries" {
			return
		}
		interrupted = true
		query := tx.Session(&gorm.Session{NewDB: true})
		require.NoError(t, query.Exec("UPDATE user_webhook_deliveries SET state = ?, lease_token = ?, payload = ? WHERE id = ?", "cancelled", "", "", row.ID).Error)
		require.NoError(t, query.Exec("UPDATE user_webhooks SET url = ? WHERE id = ?", "https://new.example/hook", hook.ID).Error)
	}))
	client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("cancelled delivery must not send a request")
		return nil, errors.New("unexpected request")
	})}
	require.NoError(t, process(context.Background(), db, row.ID, client))
	require.True(t, interrupted)
	require.NoError(t, db.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "cancelled", row.State)
	require.Empty(t, row.Payload)
}
