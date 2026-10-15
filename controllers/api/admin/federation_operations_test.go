package admin

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"testing"
	"time"
)

func operationsDB(t *testing.T) (*gorm.DB, *models.Account) {
	t.Helper()
	db, e := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard, DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, e)
	raw, e := db.DB()
	require.NoError(t, e)
	raw.SetMaxOpenConns(1)
	old := database.GetDB()
	database.SetTestDB(db)
	t.Cleanup(func() { database.SetTestDB(old); raw.Close() })
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.Account{}, &models.Role{}, &models.RoleUser{}, &models.Follow{}, &models.InstanceMetadata{}, &models.Status{}, &models.FederationOutbox{}, &activitypub.Delivery{}, &adminwebhook.Subscription{}, &adminwebhook.Delivery{}))
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.User{Id: 1, UUID: "ops-admin", UserName: "ops-admin", EmailHash: "ops-admin", IsAdmin: true, Status: models.UserStatusNormal}).Error)
	a := &models.Account{Id: 1, UserId: 1, Username: "ops-admin"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(a).Error)
	return db, a
}
func TestOperationsQueueCASAndRedactedPayloads(t *testing.T) {
	db, a := operationsDB(t)
	future := time.Now().Add(time.Hour)
	for i, state := range []activitypub.DeliveryStatus{activitypub.DeliveryStatusPending, activitypub.DeliveryStatusProcessing, activitypub.DeliveryStatusDelivered, activitypub.DeliveryStatusFailed} {
		require.NoError(t, db.Create(&activitypub.Delivery{ID: uint64(i + 1), ActivityID: "stable-activity", TargetInbox: "https://receiver.example/inbox?token=PRIVATE_TOKEN", Payload: `{"content":"PRIVATE_BODY"}`, Authorization: `{"secret":"PRIVATE_AUTH"}`, Status: state, NextRetryAt: &future, LastError: "delivery https://receiver.example/?token=PRIVATE_ERROR failed"}).Error)
	}
	c, w := newGetContext(t, nil, "kind=delivery")
	c.Set("account", a)
	FederationJobs(c)
	require.Equal(t, 200, w.Code)
	for _, secret := range []string{"PRIVATE_TOKEN", "PRIVATE_BODY", "PRIVATE_AUTH", "PRIVATE_ERROR"} {
		require.NotContains(t, w.Body.String(), secret)
	}
	for _, tc := range []struct {
		id      string
		action  string
		confirm bool
		code    int
	}{{"1", "retry", false, 200}, {"2", "retry", false, 409}, {"3", "retry", false, 409}, {"4", "cancel", false, 422}, {"4", "cancel", true, 200}} {
		c, w = newJSONContext(t, "POST", gin.Params{{Key: "kind", Value: "delivery"}, {Key: "id", Value: tc.id}}, gin.H{"action": tc.action, "confirm": tc.confirm})
		c.Set("account", a)
		FederationJobAction(c)
		require.Equal(t, tc.code, w.Code)
	}
	var delivery activitypub.Delivery
	require.NoError(t, db.First(&delivery, 1).Error)
	require.Equal(t, "stable-activity", delivery.ActivityID)
	require.JSONEq(t, `{"content":"PRIVATE_BODY"}`, delivery.Payload)
	delivery = activitypub.Delivery{}
	require.NoError(t, db.First(&delivery, "id = ?", 4).Error)
	require.Equal(t, activitypub.DeliveryStatus("cancelled"), delivery.Status)
	c, w = newGetContext(t, nil, "")
	FederationJobs(c)
	require.Equal(t, 401, w.Code)
}
func TestOperationsWebhookSecretWriteOnlyAndTestConfirmation(t *testing.T) {
	db, a := operationsDB(t)
	c, w := newJSONContext(t, "POST", nil, gin.H{"url": "https://receiver.example/hook", "description": "QA", "enabled": true, "events": []string{"status.created"}})
	c.Set("account", a)
	CreateWebhook(c)
	require.Equal(t, 201, w.Code)
	var response struct {
		Subscription adminwebhook.Subscription `json:"subscription"`
		Secret       string                    `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Secret, 64)
	c, w = newGetContext(t, nil, "")
	c.Set("account", a)
	Webhooks(c)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), response.Secret)
	c, w = newJSONContext(t, "POST", gin.Params{{Key: "id", Value: "1"}, {Key: "action", Value: "test"}}, gin.H{})
	c.Set("account", a)
	WebhookAction(c)
	require.Equal(t, 422, w.Code)
	c, w = newJSONContext(t, "POST", gin.Params{{Key: "id", Value: "1"}, {Key: "action", Value: "test"}}, gin.H{"confirm": true})
	c.Set("account", a)
	WebhookAction(c)
	require.Equal(t, 202, w.Code)
	var count int64
	require.NoError(t, db.Model(&adminwebhook.Delivery{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	c, w = newJSONContext(t, "POST", nil, gin.H{"url": "https://127.0.0.1/hook", "enabled": true, "events": []string{"status.created"}})
	c.Set("account", a)
	CreateWebhook(c)
	require.Equal(t, 422, w.Code)
}
func TestOperationsInstancesActualAcceptedRelationships(t *testing.T) {
	db, a := operationsDB(t)
	domain := "remote.example"
	peer := models.Account{Id: 2, Username: "remote", Domain: &domain, OriginId: "https://remote.example/users/u"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&peer).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.Follow{Id: 1, AccountId: 1, FollowId: 2, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.Follow{Id: 2, AccountId: 2, FollowId: 1, Status: models.FollowStatusRequested}).Error)
	c, w := newGetContext(t, nil, "")
	c.Set("account", a)
	FederationInstances(c)
	require.Equal(t, 200, w.Code)
	var result struct {
		Items []struct {
			Domain                         string
			Accounts, Following, Followers int64
		}
		Total int64
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result.Items, 1)
	require.EqualValues(t, 1, result.Items[0].Following)
	require.Zero(t, result.Items[0].Followers)
	require.EqualValues(t, 1, result.Total)
}
