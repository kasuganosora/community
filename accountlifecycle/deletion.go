// Package accountlifecycle owns resumable, user-requested account erasure.
package accountlifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/datatransfer"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrLastAdministrator = errors.New("cannot delete the last active administrator")

func RequestDeletion(ctx context.Context, conn *gorm.DB, accountID, userID uint64) error {
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var a models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, accountID).Error; err != nil {
			return err
		}
		if a.Domain != nil || a.UserId != userID {
			return errors.New("account ownership mismatch")
		}
		if a.DeletionRequestedAt != nil {
			return nil
		}
		var user models.User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		if user.IsAdmin {
			var admins []models.User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("is_admin = ?", true).Order("id").Find(&admins).Error; err != nil {
				return err
			}
			active := 0
			for _, admin := range admins {
				if !admin.IsBan && admin.Status == models.UserStatusNormal {
					active++
				}
			}
			if active <= 1 {
				return ErrLastAdministrator
			}
		}
		now := time.Now()
		if err := tx.Model(&a).Updates(map[string]any{"is_ban": true, "deletion_requested_at": now, "discoverable": false, "no_index": true}).Error; err != nil {
			return err
		}
		if err := tx.Model(&user).Updates(map[string]any{"is_ban": true, "status": models.UserStatusRejected}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&models.AuthToken{}) {
			if err := tx.Model(&models.AuthToken{}).Where("user_id = ?", userID).UpdateColumn("revoke", now).Error; err != nil {
				return err
			}
		}
		if err := homeserver.RevokeAccountSessions(tx, &a); err != nil {
			return err
		}
		if tx.Migrator().HasTable("user_webhooks") {
			if err := tx.Table("user_webhooks").Where("account_id = ?", a.Id).Update("enabled", false).Error; err != nil {
				return err
			}
		}
		if err := models.QueueFeedInvalidation(tx, models.FeedEventMetadata{ActorID: a.Id, TargetAccountID: a.Id, Reason: "account_deleted"}); err != nil {
			return err
		}
		var recipients []uint64
		if tx.Migrator().HasTable(&models.Follow{}) {
			if err := tx.Model(&models.Follow{}).Where("follow_id = ? AND account_id IN (SELECT id FROM accounts WHERE domain IS NOT NULL)", a.Id).Pluck("account_id", &recipients).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&models.FederationOutbox{}) {
			// Prior content must never be sent after erasure was requested.
			if err := tx.Model(&models.FederationOutbox{}).Where("account_id = ? AND kind NOT IN ? AND state IN ?", a.Id, []string{"Delete", "AccountDelete", "Undo", "AnnounceUndo"}, []string{"pending", "failed"}).Updates(map[string]any{"state": "cancelled", "payload": "{}"}).Error; err != nil {
				return err
			}
			if err := tx.Create(&models.FederationOutbox{AccountID: a.Id, ObjectID: a.Id, Kind: "AccountDelete", Payload: "{}", RecipientIDs: recipients}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&models.AccountDeletion{AccountID: accountID, UserID: userID, State: "pending"}).Error
	})
}

// Start resumes interrupted jobs after restart. Each claim outlives its bounded
// attempt, so another process cannot concurrently delete the same account.
func Start(ctx context.Context, conn *gorm.DB) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var jobs []models.AccountDeletion
			if err := conn.WithContext(ctx).Where("state <> ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", "completed", time.Now()).Order("created_at").Limit(4).Find(&jobs).Error; err != nil {
				log.Warn("Account erasure queue: %v", err)
				continue
			}
			for _, job := range jobs {
				if ctx.Err() != nil {
					return
				}
				process(ctx, conn, job)
			}
		}
	}()
}

func process(ctx context.Context, conn *gorm.DB, job models.AccountDeletion) {
	now := time.Now()
	lease := now.Add(3 * time.Minute)
	token := uuid.NewString()
	claim := conn.WithContext(ctx).Model(&models.AccountDeletion{}).Where("account_id = ? AND state <> ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", job.AccountID, "completed", now).Updates(map[string]any{"state": "processing", "next_attempt_at": lease, "lease_token": token})
	if claim.Error != nil || claim.RowsAffected != 1 {
		return
	}
	work, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	done, err := EraseBatch(work, conn, job)
	state, message, next := "pending", "", time.Now()
	attempts := job.Attempts
	if err != nil {
		attempts++
		message = err.Error()
		delay := attempts * attempts
		if delay > 60 {
			delay = 60
		}
		next = next.Add(time.Duration(delay) * time.Minute)
		log.Warn("Account erasure %d will retry: %v", job.AccountID, err)
	}
	if done && err == nil {
		state = "completed"
	}
	// Use a fresh bounded context to release the claim after cancellation.
	finish, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	conn.WithContext(finish).Model(&models.AccountDeletion{}).Where("account_id = ? AND lease_token = ?", job.AccountID, token).Updates(map[string]any{"state": state, "last_error": message, "attempts": attempts, "next_attempt_at": next, "lease_token": ""})
}

func EraseBatch(ctx context.Context, conn *gorm.DB, job models.AccountDeletion) (bool, error) {
	conn = conn.WithContext(ctx)
	var a models.Account
	if err := conn.First(&a, job.AccountID).Error; err != nil {
		return false, err
	}
	if a.DeletionRequestedAt == nil || !a.IsBan || a.UserId != job.UserID {
		return false, errors.New("account is not scheduled for deletion")
	}
	if conn.Migrator().HasTable(&models.Status{}) {
		var statuses []models.Status
		if err := conn.Where("account_id = ?", a.Id).Order("id").Limit(50).Find(&statuses).Error; err != nil {
			return false, err
		}
		for _, status := range statuses {
			deferred, publish := models.BeginDeferredStatusEvents(ctx)
			if err := conn.WithContext(deferred).Transaction(func(tx *gorm.DB) error {
				if tx.Migrator().HasTable(&models.StatusHistory{}) {
					if err := tx.Where("status_id = ?", status.Id).Delete(&models.StatusHistory{}).Error; err != nil {
						return err
					}
				}
				return tx.Delete(&status).Error
			}); err != nil {
				return false, err
			}
			publish()
		}
		if len(statuses) > 0 {
			return false, nil
		}
	}
	if conn.Migrator().HasTable(&models.MediaAttachment{}) {
		var media []models.MediaAttachment
		if err := conn.Where("account_id = ?", a.Id).Order("id").Limit(50).Find(&media).Error; err != nil {
			return false, err
		}
		for _, m := range media {
			// Shared imported objects can belong to another account; detach only
			// this ownership row and retain their bytes in that case.
			var shared int64
			if err := conn.Model(&models.MediaAttachment{}).Where("account_id <> ? AND file_path = ?", a.Id, m.FilePath).Count(&shared).Error; err != nil {
				return false, err
			}
			if shared == 0 {
				if err := m.DeleteStoredFiles(ctx); err != nil {
					return false, err
				}
			}
			if err := conn.Delete(&m).Error; err != nil {
				return false, err
			}
		}
		if len(media) > 0 {
			return false, nil
		}
	}
	if done, err := homeserver.EraseAccountData(ctx, conn, &a); err != nil || !done {
		return false, err
	}
	if err := eraseTransfers(ctx, conn, a.Id); err != nil {
		return false, err
	}
	for _, raw := range []string{a.Avatar, a.AvatarStatic, a.Header, a.HeaderStatic} {
		key, _ := storage.NormalizeKey(raw)
		if !strings.HasPrefix(key, "avatars/") && !strings.HasPrefix(key, "headers/") && !strings.HasPrefix(key, "media/local/") {
			continue
		}
		var shared int64
		if err := conn.Model(&models.Account{}).Where("id <> ? AND (avatar = ? OR avatar_static = ? OR header = ? OR header_static = ?)", a.Id, raw, raw, raw, raw).Count(&shared).Error; err != nil {
			return false, err
		}
		if shared == 0 {
			if err := storage.Default().Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
		}
	}
	if conn.Migrator().HasTable(&models.Follow{}) {
		var follows []models.Follow
		if err := conn.Where("account_id = ? OR follow_id = ?", a.Id, a.Id).Limit(100).Find(&follows).Error; err != nil {
			return false, err
		}
		for _, follow := range follows {
			if err := conn.Delete(&follow).Error; err != nil {
				return false, err
			}
		}
		if len(follows) > 0 {
			return false, nil
		}
	}

	if done, err := eraseQueuedContent(conn, a.Id); err != nil || !done {
		return false, err
	}
	err := conn.Transaction(func(tx *gorm.DB) error {
		if err := erasePrivateRows(tx, a.Id, job.UserID); err != nil {
			return err
		}
		if err := tx.Model(&models.Account{}).Where("id = ?", a.Id).Updates(map[string]any{"display_name": "", "note": "", "imported_note_html": "", "website": "", "avatar": "", "avatar_static": "", "header": "", "header_static": "", "also_known_as": "[]", "avatar_decorations": "[]", "avatar_decoration_id": 0, "location": "", "birthday": "", "language": "", "followed_message": "", "reaction_acceptance": "all", "is_cat": false}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&models.AccountExtra{}) {
			if err := tx.Where("account_id = ?", a.Id).Delete(&models.AccountExtra{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.User{}).Where("id = ?", job.UserID).Updates(map[string]any{"email_hash": fmt.Sprintf("deleted:%d", job.UserID), "properties": nil, "is_admin": false, "is_mail_verified": false}).Error; err != nil {
			return err
		}
		return nil
	})
	if err == nil && len(cRedis.GetRedises()) > 0 {
		_ = cache.InvalidateAccount(ctx, a.Id)
	}
	return err == nil, err
}

func erasePrivateRows(tx *gorm.DB, accountID, userID uint64) error {
	// Keep the account/user identity and signing keys for queued Tombstones;
	// remove all reusable credentials and private profile/preferences.
	for _, model := range []any{&models.AuthToken{}, &models.UserSecretData{}, &models.UserMeta{}, &models.UserSetting{}, &models.PasskeyCredential{}, &models.WebAuthnSession{}, &models.ApplicationUserAuth{}, &models.MisskeyAuthSession{}, &models.RoleUser{}} {
		if tx.Migrator().HasTable(model) {
			if err := tx.Unscoped().Where("user_id = ?", userID).Delete(model).Error; err != nil {
				return err
			}
		}
	}
	for _, table := range []string{"personal_access_tokens"} {
		if tx.Migrator().HasTable(table) {
			if err := tx.Exec("DELETE FROM "+table+" WHERE user_id = ?", userID).Error; err != nil {
				return err
			}
		}
	}
	if tx.Migrator().HasTable("user_webhook_deliveries") {
		if err := tx.Exec("DELETE FROM user_webhook_deliveries WHERE webhook_id IN (SELECT id FROM user_webhooks WHERE account_id = ?)", accountID).Error; err != nil {
			return err
		}
	}
	for _, table := range []string{"profile_field_verifications", "user_webhooks", "status_histories", "notifications", "account_emojis", "account_conversations", "bookmark_statuses", "favorite_statuses", "pin_statuses", "mute_statuses", "mute_accounts", "list_accounts", "list_follow_accounts", "status_to_accounts", "status_reactions", "poll_option_votes", "followed_tags", "markers", "group_members", "contacts", "account_languages"} {
		if tx.Migrator().HasTable(table) && tx.Migrator().HasColumn(table, "account_id") {
			if err := tx.Exec("DELETE FROM "+table+" WHERE account_id = ?", accountID).Error; err != nil {
				return err
			}
		}
	}
	if tx.Migrator().HasTable("recommendation_feedback") {
		if err := tx.Exec("DELETE FROM recommendation_feedback WHERE scope = ? AND owner_id = ?", "user", accountID).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable("lists") {
		for _, table := range []string{"list_accounts", "list_follow_accounts", "list_hashtags"} {
			if tx.Migrator().HasTable(table) {
				if err := tx.Exec("DELETE FROM "+table+" WHERE list_id IN (SELECT id FROM lists WHERE account_id = ?)", accountID).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Unscoped().Where("account_id = ?", accountID).Delete(&models.List{}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&models.GroupMessage{}) {
		if err := tx.Unscoped().Where("sender_id = ?", accountID).Delete(&models.GroupMessage{}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&models.ContactRequest{}) {
		if err := tx.Unscoped().Where("from_account_id = ? OR to_account_id = ?", accountID, accountID).Delete(&models.ContactRequest{}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&models.Filter{}) {
		if err := tx.Where("account_id = ?", accountID).Delete(&models.Filter{}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&models.Block{}) {
		if err := tx.Where("account_id = ? OR target_account_id = ?", accountID, accountID).Delete(&models.Block{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func eraseTransfers(ctx context.Context, conn *gorm.DB, accountID uint64) error {
	if !conn.Migrator().HasTable(&datatransfer.Job{}) {
		return nil
	}
	var jobs []datatransfer.Job
	if err := conn.Where("owner_account_id = ? AND scope = ?", accountID, "personal").Find(&jobs).Error; err != nil {
		return err
	}
	if len(jobs) == 0 {
		return nil
	}
	root, err := datatransfer.TransferRoot()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.LeaseUntil != nil && job.LeaseUntil.After(time.Now()) {
			return errors.New("waiting for data transfer to stop")
		}
		path, err := filepath.Abs(job.Directory)
		if err != nil {
			return err
		}
		if filepath.Dir(path) != root || filepath.Base(path) != job.ID {
			return errors.New("unexpected transfer directory")
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if err := conn.Delete(&job).Error; err != nil {
			return err
		}
	}
	return ctx.Err()
}
