package homeserver

import (
	"context"
	"fmt"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

func accountMatrixIDs(conn *gorm.DB, a *models.Account) ([]string, error) {
	server := systemsettings.GetDomain()
	if GlobalHS != nil && GlobalHS.Config != nil {
		server = GlobalHS.Config.ServerName
	}
	ids := []string{fmt.Sprintf("@%s:%s", a.Username, server)}
	if conn.Migrator().HasTable(&models.MatrixUser{}) {
		var mapped []string
		if err := conn.Model(&models.MatrixUser{}).Where("local_account_id = ? AND direction = ?", a.Id, "local").Pluck("matrix_user_id", &mapped).Error; err != nil {
			return nil, err
		}
		ids = append(ids, mapped...)
	}
	if conn.Migrator().HasTable(&MatrixUserSession{}) {
		var sessionIDs []string
		if err := conn.Model(&MatrixUserSession{}).Where("local_account_id = ?", a.Id).Distinct().Pluck("user_id", &sessionIDs).Error; err != nil {
			return nil, err
		}
		ids = append(ids, sessionIDs...)
	}
	return ids, nil
}

// RevokeAccountSessions participates in the erasure request transaction so
// native Matrix clients lose access at the same time as web/OAuth clients.
func RevokeAccountSessions(conn *gorm.DB, a *models.Account) error {
	if !conn.Migrator().HasTable(&MatrixUserSession{}) {
		return nil
	}
	ids, err := accountMatrixIDs(conn, a)
	if err != nil {
		return err
	}
	return conn.Where("local_account_id = ? OR user_id IN ?", a.Id, ids).Delete(&MatrixUserSession{}).Error
}

// EraseAccountData redacts the local copy, purges uploads (including private
// encrypted attachments), and removes device/session data. Remote copies are
// beyond the local server's control, as with ActivityPub deletion.
func EraseAccountData(ctx context.Context, conn *gorm.DB, a *models.Account) (bool, error) {
	conn = conn.WithContext(ctx)
	ids, err := accountMatrixIDs(conn, a)
	if err != nil {
		return false, err
	}
	if conn.Migrator().HasTable(&MatrixEvent{}) {
		var events []MatrixEvent
		if err := conn.Where("sender IN ? AND state_key IS NULL AND event_type <> ? AND content <> ?", ids, EventTypeRoomRedaction, "{}").Limit(100).Find(&events).Error; err != nil {
			return false, err
		}
		for _, event := range events {
			if err := applyStoredRedactionTx(conn, event.RoomID, event.EventID); err != nil {
				return false, err
			}
		}
		if len(events) > 0 {
			return false, nil
		}
	}
	if conn.Migrator().HasTable(&MatrixMedia{}) {
		var media []MatrixMedia
		if err := conn.Where("uploader_id IN ? AND gc_state <> ?", ids, "deleted").Limit(50).Find(&media).Error; err != nil {
			return false, err
		}
		for _, m := range media {
			// Account erasure explicitly deletes the owner's uploads even when
			// their old MXCs remain in immutable room history.
			if err := conn.Model(&m).Updates(map[string]any{"gc_state": "deleting", "gc_next_check_at": time.Now()}).Error; err != nil {
				return false, err
			}
			if err := finishMatrixMediaDeletion(ctx, conn, m.ServerName, m.MediaID); err != nil {
				return false, err
			}
		}
		if len(media) > 0 {
			return false, nil
		}
	}
	// Older bridge rows may identify the sender only by MXID, without the
	// local account ID. Remove those copies as well as native room events.
	for table, column := range map[string]string{"group_messages": "matrix_sender_id", "matrix_messages": "matrix_sender_id"} {
		if conn.Migrator().HasTable(table) && conn.Migrator().HasColumn(table, column) {
			if err := conn.Exec("DELETE FROM "+table+" WHERE "+column+" IN ?", ids).Error; err != nil {
				return false, err
			}
		}
	}
	if conn.Migrator().HasTable(&MatrixRoomMember{}) {
		if err := conn.Model(&MatrixRoomMember{}).Where("user_id IN ?", ids).Updates(map[string]any{"membership": MembershipLeave, "display_name": "", "avatar_url": ""}).Error; err != nil {
			return false, err
		}
	}
	for _, table := range []string{"matrix_user_sessions", "matrix_account_data", "matrix_filters", "matrix_devices", "matrix_device_keys", "matrix_one_time_keys", "matrix_fallback_keys", "matrix_cross_signing_keys", "matrix_key_backup_sessions", "matrix_key_backup_recoveries", "matrix_key_backups", "matrix_key_backup_versions", "matrix_ssss_secrets", "matrix_ssss_default_keys", "matrix_password_reset_tokens", "matrix_push_rules", "matrix_pushers", "matrix_presence", "matrix_receipts", "matrix_room_tags"} {
		if conn.Migrator().HasTable(table) && conn.Migrator().HasColumn(table, "user_id") {
			if err := conn.Exec("DELETE FROM "+table+" WHERE user_id IN ?", ids).Error; err != nil {
				return false, err
			}
		}
	}
	if conn.Migrator().HasTable(&MatrixToDeviceMessage{}) {
		if err := conn.Where("sender IN ? OR target_user IN ?", ids, ids).Delete(&MatrixToDeviceMessage{}).Error; err != nil {
			return false, err
		}
	}
	if conn.Migrator().HasTable(&models.MatrixUser{}) {
		if err := conn.Unscoped().Where("local_account_id = ?", a.Id).Delete(&models.MatrixUser{}).Error; err != nil {
			return false, err
		}
	}
	return true, nil
}
