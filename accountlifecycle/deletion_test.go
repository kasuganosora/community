package accountlifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func deletionDB(t *testing.T) (*gorm.DB, *models.Account) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	sql, err := conn.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sql.Close() })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.UserSecretData{}, &models.SystemSetting{}, &models.AuthToken{}, &models.AccountDeletion{}, &models.MediaAttachment{}, &models.FederationOutbox{}, &models.UserWebhook{}, &models.UserWebhookDelivery{}, &models.PersonalAccessToken{}))
	// MediaAttachment's associations migrate Status. These tests intentionally
	// focus on credential/media erasure without the independent status hooks.
	require.NoError(t, conn.Migrator().DropTable(&models.Status{}))
	user := models.User{Id: 1, UserName: "alice", EmailHash: "alice-email"}
	require.NoError(t, conn.Create(&user).Error)
	a := &models.Account{Id: 1, UserId: 1, Username: "alice", DisplayName: "Alice", Note: "private bio"}
	require.NoError(t, conn.Create(a).Error)
	return conn, a
}

func TestDeletionImmediatelyRevokesCredentialsAndRetriesFailedMedia(t *testing.T) {
	conn, a := deletionDB(t)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, conn.Create(&models.AuthToken{Token: "test-token", UserId: 1, CreatedAt: &now}).Error)
	store := storagetest.NewMemory()
	previous := storage.Default()
	storage.SetDefault(store)
	t.Cleanup(func() { storage.SetDefault(previous) })
	require.NoError(t, store.Put(ctx, "media/local/private.png", strings.NewReader("private"), 7, "image/png"))
	m := models.MediaAttachment{Id: 1, AccountId: a.Id, FileName: "private.png", FilePath: "local/private.png", ContentType: "image/png"}
	require.NoError(t, conn.Create(&m).Error)
	require.NoError(t, RequestDeletion(ctx, conn, a.Id, a.UserId))
	var token models.AuthToken
	require.NoError(t, conn.First(&token, "token = ?", "test-token").Error)
	require.NotNil(t, token.Revoke)
	var saved models.Account
	require.NoError(t, conn.First(&saved, a.Id).Error)
	require.True(t, saved.IsBan)
	require.NotNil(t, saved.DeletionRequestedAt)
	var job models.AccountDeletion
	require.NoError(t, conn.First(&job).Error)
	store.Fail = errors.New("storage unavailable")
	done, err := EraseBatch(ctx, conn, job)
	require.Error(t, err)
	require.False(t, done)
	var count int64
	require.NoError(t, conn.Model(&models.MediaAttachment{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "failed storage deletion keeps retry metadata")
	store.Fail = nil
	done, err = EraseBatch(ctx, conn, job)
	require.NoError(t, err)
	require.False(t, done)
	_, err = store.Stat(ctx, "media/local/private.png")
	require.ErrorIs(t, err, storage.ErrNotFound)
	for i := 0; i < 5 && !done; i++ {
		done, err = EraseBatch(ctx, conn, job)
		require.NoError(t, err)
	}
	require.True(t, done)
	require.NoError(t, conn.First(&saved, a.Id).Error)
	require.Empty(t, saved.Note)
	require.Empty(t, saved.DisplayName)
	require.NoError(t, conn.Model(&models.AuthToken{}).Count(&count).Error)
	require.Zero(t, count)
	var events []models.FederationOutbox
	require.NoError(t, conn.Find(&events).Error)
	require.Len(t, events, 1)
	require.Equal(t, "AccountDelete", events[0].Kind)
}

func TestDeletionProtectsLastAdminAndRejectsWrongOwner(t *testing.T) {
	conn, a := deletionDB(t)
	require.NoError(t, conn.Model(&models.User{}).Where("id = ?", a.UserId).UpdateColumn("is_admin", true).Error)
	require.ErrorIs(t, RequestDeletion(context.Background(), conn, a.Id, a.UserId), ErrLastAdministrator)
	require.Error(t, RequestDeletion(context.Background(), conn, a.Id, 2))
	var count int64
	require.NoError(t, conn.Model(&models.AccountDeletion{}).Count(&count).Error)
	require.Zero(t, count)
	var saved models.Account
	require.NoError(t, conn.First(&saved, a.Id).Error)
	require.False(t, saved.IsBan)
}

func TestDeletionBlocksLateUploadsAndClearsConnectedServices(t *testing.T) {
	conn, a := deletionDB(t)
	hook := models.UserWebhook{ID: "own-hook", AccountID: a.Id, Name: "own", URL: "https://example.test/hook", Secret: "secret", Enabled: true}
	require.NoError(t, conn.Create(&hook).Error)
	require.NoError(t, conn.Create(&models.UserWebhookDelivery{ID: "own-delivery", WebhookID: hook.ID, Payload: "private notification", Source: "one"}).Error)
	require.NoError(t, conn.Create(&models.PersonalAccessToken{ID: "own-token", UserID: a.UserId, TokenHash: "hash", Name: "own"}).Error)
	require.NoError(t, RequestDeletion(context.Background(), conn, a.Id, a.UserId))
	require.NoError(t, conn.First(&hook, "id = ?", hook.ID).Error)
	require.False(t, hook.Enabled)
	require.ErrorContains(t, conn.Create(&models.MediaAttachment{Id: 9, AccountId: a.Id, FilePath: "local/late.png"}).Error, "scheduled for deletion")
	var job models.AccountDeletion
	require.NoError(t, conn.First(&job).Error)
	for i := 0; i < 5; i++ {
		process(context.Background(), conn, job)
		require.NoError(t, conn.First(&job).Error)
		if job.State == "completed" {
			break
		}
	}
	require.Equal(t, "completed", job.State)
	require.Empty(t, job.LeaseToken)
	for _, model := range []any{&models.UserWebhook{}, &models.UserWebhookDelivery{}, &models.PersonalAccessToken{}} {
		var count int64
		require.NoError(t, conn.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
}
