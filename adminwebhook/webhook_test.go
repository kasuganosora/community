package adminwebhook

import (
	"context"
	"encoding/json"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func hookDB(t *testing.T) *gorm.DB {
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, e)
	raw, e := db.DB()
	require.NoError(t, e)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	require.NoError(t, db.AutoMigrate(&Subscription{}, &Delivery{}, &models.FederationOutbox{}))
	return db
}
func TestCapturePublicOnlyPersistentIdempotentAndNoBodies(t *testing.T) {
	db := hookDB(t)
	s := &Subscription{URL: "https://receiver.example/hook", Enabled: true, Events: []string{"status.created", "status.updated", "status.deleted"}}
	secret, e := Create(db, s)
	require.NoError(t, e)
	require.Len(t, secret, 64)
	for i := 0; i < 8; i++ {
		status := models.Status{Id: uint64(i + 1), AccountId: 7, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "NEVER_EXPOSE_BODY"}}
		kind := "Create"
		switch i {
		case 1:
			status.Visibility = models.StatusVisibilityDirect
		case 2:
			status.Visibility = models.StatusVisibilityPrivate
		case 3:
			status.Visibility = models.StatusVisibilityUnlisted
		case 4:
			status.LocalOnly = true
		case 5:
			domain := "remote.example"
			status.Domain = &domain
		case 6:
			kind = "Update"
		case 7:
			kind = "Delete"
		}
		payload, _ := json.Marshal(status)
		require.NoError(t, db.Create(&models.FederationOutbox{ObjectID: status.Id, AccountID: 7, Kind: kind, Payload: string(payload)}).Error)
	}
	require.NoError(t, Capture(db, s.ID))
	require.NoError(t, Capture(db, s.ID))
	var rows []Delivery
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 3)
	for _, d := range rows {
		require.NotContains(t, d.Payload, "NEVER_EXPOSE_BODY")
		require.Equal(t, "pending", d.State)
	}
	out, _ := json.Marshal(s)
	require.NotContains(t, string(out), secret)
	late := &Subscription{URL: s.URL, Events: s.Events, Enabled: true}
	_, e = Create(db, late)
	require.NoError(t, e)
	require.NoError(t, Capture(db, late.ID))
	var count int64
	require.NoError(t, db.Model(&Delivery{}).Where("subscription_id = ?", late.ID).Count(&count).Error)
	require.Zero(t, count)
}

type hookRoundTrip func(*http.Request) (*http.Response, error)

func (f hookRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDeliverySignatureRetryStableIDAndLease(t *testing.T) {
	db := hookDB(t)
	s := &Subscription{URL: "https://receiver.example/hook", Enabled: true, Events: []string{"status.created"}}
	secret, e := Create(db, s)
	require.NoError(t, e)
	d, e := QueueTest(db, s.ID)
	require.NoError(t, e)
	calls := 0
	client := &http.Client{Transport: hookRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		body, e := io.ReadAll(r.Body)
		require.NoError(t, e)
		require.Equal(t, d.ID, r.Header.Get("Idempotency-Key"))
		require.Equal(t, Sign(secret, r.Header.Get("X-Community-Timestamp"), d.ID, string(body)), r.Header.Get("X-Community-Signature"))
		status := 500
		if calls == 2 {
			status = 204
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("SECRET_RESPONSE_MUST_NOT_LEAK"))}, nil
	})}
	require.NoError(t, process(context.Background(), db, d.ID, client))
	require.NoError(t, db.First(d, "id = ?", d.ID).Error)
	require.Equal(t, "pending", d.State)
	require.Equal(t, 1, d.Attempts)
	require.Equal(t, "receiver returned HTTP 500", d.LastError)
	require.NoError(t, process(context.Background(), db, d.ID, client))
	require.Equal(t, 1, calls)
	require.NoError(t, db.Model(d).Update("next_attempt_at", time.Now().Add(-time.Second)).Error)
	require.NoError(t, process(context.Background(), db, d.ID, client))
	require.NoError(t, db.First(d, "id = ?", d.ID).Error)
	require.Equal(t, "delivered", d.State)
	require.NoError(t, process(context.Background(), db, d.ID, client))
	require.Equal(t, 2, calls)
}
func TestWebhookDestinationAndEventsValidation(t *testing.T) {
	for _, url := range []string{"http://public.example", "https://127.0.0.1", "https://[::1]", "https://10.0.0.1", "https://169.254.169.254", "https://localhost", "https://user:pass@public.example/hook", "https://public.example/#fragment"} {
		require.Error(t, ValidateURL(url))
	}
	require.NoError(t, ValidateURL("https://receiver.example/hook"))
	require.Error(t, ValidateEvents([]string{"unknown"}))
	require.Error(t, ValidateEvents([]string{"status.created", "status.created"}))
	require.Error(t, ValidateEvents(nil))
}
