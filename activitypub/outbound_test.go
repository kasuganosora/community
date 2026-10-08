package activitypub

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func outboundDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.AccountKey{}, &models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.Tag{}, &models.StatusTag{}, &models.MediaAttachment{}, &models.Follow{}, &models.Block{}, &models.SystemSetting{}, &models.Poll{}, &models.PollOption{}, &models.Emoji{}, &models.StatusEmoji{}, &models.Application{}, &models.Conversation{}, &models.AccountConversation{}, &models.FederationOutbox{}, &Delivery{}))
	return conn
}
func outboundAccount(t *testing.T, conn *gorm.DB, name string, remote bool) *models.Account {
	t.Helper()
	var count int64
	require.NoError(t, conn.Model(&models.Account{}).Count(&count).Error)
	a := &models.Account{Id: uint64(count + 1), Username: name}
	if remote {
		domain := "remote.example"
		a.Domain = &domain
		a.OriginId = "https://remote.example/actors/" + name
	}
	require.NoError(t, conn.Create(a).Error)
	return a
}
func outboxEvents(t *testing.T, conn *gorm.DB) []models.FederationOutbox {
	t.Helper()
	var e []models.FederationOutbox
	require.NoError(t, conn.Order("id").Find(&e).Error)
	return e
}

func TestOutboundTransactionalSnapshots(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "writer", false)
	remote := outboundAccount(t, conn, "reader", true)
	require.NoError(t, conn.Create(&models.Follow{Id: 11, AccountId: remote.Id, FollowId: author.Id}).Error)
	status := &models.Status{AccountId: author.Id, Content: models.StatusContent{Content: "first committed text"}}
	require.NoError(t, conn.Create(status).Error)
	require.Len(t, outboxEvents(t, conn), 1)
	rollback := errors.New("rollback")
	require.ErrorIs(t, conn.Transaction(func(tx *gorm.DB) error {
		status.Content.Content = "rolled back"
		require.NoError(t, tx.Save(&status.Content).Error)
		require.NoError(t, tx.Omit("Content").Save(status).Error)
		return rollback
	}), rollback)
	require.Len(t, outboxEvents(t, conn), 1)
	require.ErrorIs(t, conn.Transaction(func(tx *gorm.DB) error { require.NoError(t, tx.Delete(status).Error); return rollback }), rollback)
	require.Len(t, outboxEvents(t, conn), 1)
	require.NoError(t, conn.Delete(status).Error)
	events := outboxEvents(t, conn)
	require.Len(t, events, 2)
	require.Equal(t, "Delete", events[1].Kind)
	var snapshot models.Status
	require.NoError(t, json.Unmarshal([]byte(events[1].Payload), &snapshot))
	require.Equal(t, "first committed text", snapshot.Content.Content)
	require.Equal(t, []uint64{remote.Id}, events[1].RecipientIDs)
	// Remote input is never reflected to followers as a locally signed Create.
	origin := "https://remote.example/notes/1"
	require.NoError(t, conn.Create(&models.Status{AccountId: remote.Id, OriginUri: &origin, Content: models.StatusContent{Content: "remote"}}).Error)
	require.Len(t, outboxEvents(t, conn), 2)
}

type outboundTransport func(*http.Request) (*http.Response, error)

func (f outboundTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func outboundResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestOutboundDirectIsSignedAndOnlyReachesMention(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "sender", false)
	recipient := outboundAccount(t, conn, "recipient", true)
	follower := outboundAccount(t, conn, "follower", true)
	require.NoError(t, conn.Create(&models.Follow{Id: 12, AccountId: follower.Id, FollowId: author.Id}).Error)
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityDirect, Content: models.StatusContent{Content: "@recipient@remote.example private body"}}
	require.NoError(t, conn.Create(status).Error)
	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, []uint64{recipient.Id}, events[0].RecipientIDs)
	key, err := localAccountKey(author)
	require.NoError(t, err)
	block, _ := pem.Decode([]byte(key.PublicKey))
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	var sent []map[string]any
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			require.Equal(t, recipient.OriginId, r.URL.String())
			return outboundResponse(200, `{"id":"`+recipient.OriginId+`","inbox":"https://remote.example/custom-mailbox"}`), nil
		}
		require.Equal(t, "/custom-mailbox", r.URL.Path)
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		r.Body = io.NopCloser(bytes.NewReader(data))
		require.NoError(t, VerifyRequest(r, parsed.(*rsa.PublicKey)))
		require.Contains(t, r.Header.Get("Signature"), systemsettings.GetBaseURL()+"/users/sender#main-key")
		var activity map[string]any
		require.NoError(t, json.Unmarshal(data, &activity))
		sent = append(sent, activity)
		return outboundResponse(202, ""), nil
	})}
	worker := &OutboundWorker{}
	worker.processPending()
	var deliveries []Delivery
	require.NoError(t, conn.Find(&deliveries).Error)
	require.Len(t, deliveries, 1)
	queue := NewDeliveryQueue(nil, "", 0)
	queue.processPending()
	queue.processPending()
	require.Len(t, sent, 1)
	require.Equal(t, "Create", sent[0]["type"])
	object := sent[0]["object"].(map[string]any)
	require.Equal(t, []any{recipient.OriginId}, object["to"])
	require.Empty(t, object["cc"])
	require.Equal(t, "processed", outboxEvents(t, conn)[0].State)
}

func TestOutboundFollowUndoAndRetry(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "follower", false)
	remote := outboundAccount(t, conn, "opaque", true)
	follow := &models.Follow{Id: 13, AccountId: author.Id, FollowId: remote.Id, Status: models.FollowStatusRequested}
	require.NoError(t, conn.Create(follow).Error)
	require.NoError(t, conn.Delete(follow).Error)
	events := outboxEvents(t, conn)
	require.Len(t, events, 2)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	var sent []map[string]any
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return outboundResponse(200, `{"id":"`+remote.OriginId+`","inbox":"https://remote.example/inbox"}`), nil
		}
		var activity map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&activity))
		sent = append(sent, activity)
		if len(sent) == 1 {
			return outboundResponse(503, "retry"), nil
		}
		return outboundResponse(202, ""), nil
	})}
	worker := &OutboundWorker{}
	worker.processPending()
	queue := NewDeliveryQueue(nil, "", 0)
	queue.processPending()
	require.Len(t, sent, 1, "Undo must not overtake the retried Follow")
	var pending Delivery
	require.NoError(t, conn.Order("id").First(&pending).Error)
	require.Equal(t, DeliveryStatusPending, pending.Status)
	require.Equal(t, 1, pending.Retries)
	queue.processPending()
	require.Len(t, sent, 1, "respect retry time")
	past := time.Now().Add(-time.Second)
	require.NoError(t, conn.Model(&pending).Update("next_retry_at", past).Error)
	queue.processPending()
	require.Len(t, sent, 3)
	wantID := systemsettings.GetBaseURL() + "/users/follower/follows/" + strconv.FormatUint(follow.Id, 10)
	require.Equal(t, wantID, sent[0]["id"])
	require.Equal(t, wantID, sent[1]["id"])
	require.Equal(t, "Undo", sent[2]["type"])
	require.Equal(t, wantID, sent[2]["object"].(map[string]any)["id"])
}

func TestOutboundPrivateRetryRechecksRevokedAccessAndDeleteStillDelivers(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "private_writer", false)
	remote := outboundAccount(t, conn, "accepted_reader", true)
	pending := outboundAccount(t, conn, "pending_reader", true)
	follow := &models.Follow{Id: 31, AccountId: remote.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}
	require.NoError(t, conn.Create(follow).Error)
	require.NoError(t, conn.Create(&models.Follow{Id: 32, AccountId: pending.Id, FollowId: author.Id, Status: models.FollowStatusRequested}).Error)
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPrivate, Content: models.StatusContent{Content: "followers-only secret"}}
	require.NoError(t, conn.Create(status).Error)
	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, []uint64{remote.Id}, events[0].RecipientIDs)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	var sent []map[string]any
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			require.Equal(t, remote.OriginId, r.URL.String())
			return outboundResponse(200, `{"id":"`+remote.OriginId+`","inbox":"https://remote.example/private-inbox"}`), nil
		}
		var activity map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&activity))
		sent = append(sent, activity)
		if len(sent) == 1 {
			return outboundResponse(503, "retry later"), nil
		}
		return outboundResponse(202, ""), nil
	})}
	worker := &OutboundWorker{}
	worker.processPending()
	queue := NewDeliveryQueue(nil, "", 0)
	queue.processPending()
	require.Len(t, sent, 1)
	require.NoError(t, conn.Delete(follow).Error)
	var row Delivery
	require.NoError(t, conn.First(&row).Error)
	require.Equal(t, DeliveryStatusPending, row.Status)
	require.NoError(t, conn.Model(&row).Update("next_retry_at", time.Now().Add(-time.Second)).Error)
	queue.processPending()
	require.Len(t, sent, 1, "revoked follower must receive no retried body")
	require.NoError(t, conn.Delete(status).Error)
	events = outboxEvents(t, conn)
	require.Equal(t, []uint64{remote.Id}, events[len(events)-1].RecipientIDs, "retain previous recipients for cleanup")
	worker.processPending()
	queue.processPending()
	require.Len(t, sent, 2)
	require.Equal(t, "Delete", sent[1]["type"])
	tombstone := sent[1]["object"].(map[string]any)
	require.Equal(t, "Tombstone", tombstone["type"])
	require.NotContains(t, tombstone, "content")
}

func TestOutboundInternalUpdatesDoNotPublishAndBlocksStillApply(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "editor", false)
	reader := outboundAccount(t, conn, "read", true)
	require.NoError(t, conn.Create(&models.Follow{Id: 41, AccountId: reader.Id, FollowId: author.Id}).Error)
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPrivate, Content: models.StatusContent{Content: "before edit"}}
	require.NoError(t, conn.Create(status).Error)
	status.Content.Content = "after edit"
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&status.Content).Error; err != nil {
			return err
		}
		return tx.Omit("Content").Save(status).Error
	}))
	events := outboxEvents(t, conn)
	require.Len(t, events, 1, "internal status saves must not publish unsupported Note Update")
	var before models.Status
	require.NoError(t, json.Unmarshal([]byte(events[0].Payload), &before))
	require.Equal(t, "before edit", before.Content.Content)
	require.NoError(t, conn.Create(&models.Block{AccountId: author.Id, Type: models.BlockTypeDomain, TargetDomain: *reader.Domain}).Error)
	worker := &OutboundWorker{}
	worker.processPending()
	var count int64
	require.NoError(t, conn.Model(&Delivery{}).Count(&count).Error)
	require.Zero(t, count, "blocked domain must not receive queued content")
	require.Equal(t, "processed", outboxEvents(t, conn)[0].State)
}

func TestOutboundDeleteRepostChangesIdentityAndAudience(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "dm_author", false)
	first := outboundAccount(t, conn, "first", true)
	second := outboundAccount(t, conn, "second", true)
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityDirect, Content: models.StatusContent{Content: "@first@remote.example first secret"}}
	require.NoError(t, conn.Create(status).Error)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	sent := map[string][]map[string]any{}
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return outboundResponse(200, `{"id":"`+r.URL.String()+`","inbox":"`+r.URL.String()+`/mail"}`), nil
		}
		var a map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&a))
		sent[r.URL.String()] = append(sent[r.URL.String()], a)
		return outboundResponse(202, ""), nil
	})}
	worker := &OutboundWorker{}
	queue := NewDeliveryQueue(nil, "", 0)
	worker.processPending()
	queue.processPending()
	require.Len(t, sent[first.OriginId+"/mail"], 1)
	replacement := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityDirect, Content: models.StatusContent{Content: "@second@remote.example new secret"}}
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(status).Error; err != nil {
			return err
		}
		return tx.Create(replacement).Error
	}))
	require.NotEqual(t, status.Id, replacement.Id)
	events := outboxEvents(t, conn)
	require.Len(t, events, 3)
	require.Equal(t, "Delete", events[1].Kind)
	require.Equal(t, status.Id, events[1].ObjectID)
	require.Equal(t, "Create", events[2].Kind)
	require.Equal(t, replacement.Id, events[2].ObjectID)
	worker.processPending()
	queue.processPending()
	require.Len(t, sent[first.OriginId+"/mail"], 2)
	revoke := sent[first.OriginId+"/mail"][1]
	require.Equal(t, "Delete", revoke["type"])
	require.NotContains(t, revoke["object"].(map[string]any), "content")
	require.Len(t, sent[second.OriginId+"/mail"], 1)
	created := sent[second.OriginId+"/mail"][0]
	require.Equal(t, "Create", created["type"])
	require.Contains(t, created["object"].(map[string]any)["content"], "new secret")
	require.NotEqual(t, sent[first.OriginId+"/mail"][0]["object"].(map[string]any)["id"], created["object"].(map[string]any)["id"])
}
