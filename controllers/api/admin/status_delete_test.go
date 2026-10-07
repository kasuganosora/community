package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminDeletionCommitsOutboxAndThenPublishesCompleteSnapshot(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	require.NoError(t, env.gdb.AutoMigrate(&models.FederationOutbox{}))
	owner := createLocalAccount(t, env, "deleteowner")
	status := models.Status{Id: 811, AccountId: owner.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, env.gdb.Session(&gorm.Session{SkipHooks: true}).Create(&status).Error)
	require.NoError(t, env.gdb.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusContent{StatusId: status.Id, Content: "before delete", SpoilerText: "CW"}).Error)
	calls := 0
	handler := event.Register("status", "after_delete", &event.Handler{Exec: func(e event.Event) bool {
		got, ok := e.Data.(*models.Status)
		if !ok || got.Id != status.Id {
			return true
		}
		calls++
		// With a single DB connection this query also proves commit precedes the
		// event; an event fired inside the open transaction would block forever.
		var count int64
		require.NoError(t, env.gdb.Model(&models.Status{}).Where("id = ?", status.Id).Count(&count).Error)
		require.Zero(t, count)
		require.Equal(t, "before delete", got.Content.Content)
		require.NotNil(t, got.Account)
		require.NoError(t, env.gdb.Model(&models.FederationOutbox{}).Where("object_id = ? AND kind = ?", status.Id, "Delete").Count(&count).Error)
		require.EqualValues(t, 1, count)
		return true
	}})
	defer event.Remove("status", "after_delete", handler)
	_, err := deleteAdminStatus(context.Background(), env.gdb, status.Id)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestAdminDeletionRollbackDoesNotPublishOrQueueDelete(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	require.NoError(t, env.gdb.AutoMigrate(&models.FederationOutbox{}))
	owner := createLocalAccount(t, env, "rollbackowner")
	status := models.Status{Id: 812, AccountId: owner.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, env.gdb.Session(&gorm.Session{SkipHooks: true}).Create(&status).Error)
	require.NoError(t, env.gdb.Callback().Delete().After("gorm:delete").Register("qa:fail_status_delete", func(tx *gorm.DB) {
		if tx.Statement.Table == "statuses" {
			tx.AddError(errors.New("injected failure"))
		}
	}))
	defer env.gdb.Callback().Delete().Remove("qa:fail_status_delete")
	calls := 0
	handler := event.Register("status", "*", &event.Handler{Exec: func(e event.Event) bool {
		if got, ok := e.Data.(*models.Status); ok && got.Id == status.Id {
			calls++
		}
		return true
	}})
	defer event.Remove("status", "*", handler)
	_, err := deleteAdminStatus(context.Background(), env.gdb, status.Id)
	require.Error(t, err)
	require.Zero(t, calls)
	var count int64
	require.NoError(t, env.gdb.Model(&models.Status{}).Where("id = ?", status.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, env.gdb.Model(&models.FederationOutbox{}).Where("object_id = ? AND kind = ?", status.Id, "Delete").Count(&count).Error)
	require.Zero(t, count)
}
