package accountlifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Each batch commits its cursor with the scrub. Restarts and large account
// histories therefore make forward progress rather than rolling back all work.
func eraseQueuedContent(conn *gorm.DB, accountID uint64) (bool, error) {
	var job models.AccountDeletion
	if err := conn.First(&job, "account_id = ?", accountID).Error; err != nil {
		return false, err
	}
	if conn.Migrator().HasTable(&models.FederationOutbox{}) {
		var active int64
		if err := conn.Model(&models.FederationOutbox{}).Where("account_id = ? AND state = ? AND next_attempt_at > ?", accountID, "processing", time.Now()).Count(&active).Error; err != nil {
			return false, err
		}
		if active > 0 {
			return false, errors.New("waiting for active federation work to finish")
		}
		var rows []models.FederationOutbox
		if err := conn.Where("account_id = ? AND id > ?", accountID, job.OutboxCursor).Order("id").Limit(100).Find(&rows).Error; err != nil {
			return false, err
		}
		if len(rows) > 0 {
			err := conn.Transaction(func(tx *gorm.DB) error {
				for _, row := range rows {
					updates := map[string]any{"payload": "{}"}
					switch row.Kind {
					case "AccountDelete", "Undo":
						continue
					case "Delete":
						var status models.Status
						if err := json.Unmarshal([]byte(row.Payload), &status); err != nil {
							return err
						}
						minimal := models.Status{Id: status.Id, AccountId: status.AccountId, Account: &models.Account{Id: accountID}, LocalObjectURI: status.LocalObjectURI, OriginUri: status.OriginUri, Visibility: status.Visibility, ToAccounts: status.ToAccounts}
						payload, err := json.Marshal(minimal)
						if err != nil {
							return err
						}
						updates["payload"] = string(payload)
					case "AnnounceUndo":
						var status models.Status
						if err := json.Unmarshal([]byte(row.Payload), &status); err != nil {
							return err
						}
						status.Content = models.StatusContent{}
						status.Attachments = nil
						status.Properties = nil
						if status.Account != nil {
							status.Account = &models.Account{Id: status.Account.Id, Username: status.Account.Username, LocalActorURI: status.Account.LocalActorURI}
						}
						payload, err := json.Marshal(status)
						if err != nil {
							return err
						}
						updates["payload"] = string(payload)
					default:
						updates["state"] = "cancelled"
					}
					if err := tx.Model(&row).Updates(updates).Error; err != nil {
						return err
					}
				}
				return tx.Model(&models.AccountDeletion{}).Where("account_id = ?", accountID).UpdateColumn("outbox_cursor", rows[len(rows)-1].ID).Error
			})
			return false, err
		}
	}
	if conn.Migrator().HasTable(&activitypub.Delivery{}) {
		var rows []activitypub.Delivery
		cast := "CAST(authorization AS TEXT)"
		if dialect.IsMySQL(conn) {
			cast = "CAST(authorization AS CHAR)"
		}
		pattern := fmt.Sprintf("%%\"account_id\":%d,%%", accountID)
		spaced := fmt.Sprintf("%%\"account_id\": %d,%%", accountID)
		query := conn.Where("("+cast+" LIKE ? OR "+cast+" LIKE ?)", pattern, spaced)
		var active int64
		if err := query.Session(&gorm.Session{}).Model(&activitypub.Delivery{}).Where("status = ? AND next_retry_at > ?", "processing", time.Now()).Count(&active).Error; err != nil {
			return false, err
		}
		if active > 0 {
			return false, errors.New("waiting for active federation delivery to finish")
		}
		if err := query.Where("id > ?", job.DeliveryCursor).Order("id").Limit(100).Find(&rows).Error; err != nil {
			return false, err
		}
		if len(rows) > 0 {
			err := conn.Transaction(func(tx *gorm.DB) error {
				for _, row := range rows {
					var auth activitypub.DeliveryAuthorization
					if err := json.Unmarshal([]byte(row.Authorization), &auth); err != nil {
						return err
					}
					if auth.AccountID != accountID {
						continue
					}
					switch auth.Kind {
					case "Delete", "AccountDelete", "Undo", "ReactionUndo", "AnnounceUndo":
						continue
					}
					if err := tx.Model(&row).Updates(map[string]any{"status": "cancelled", "payload": "{}", "last_error": "account erased"}).Error; err != nil {
						return err
					}
				}
				return tx.Model(&models.AccountDeletion{}).Where("account_id = ?", accountID).UpdateColumn("delivery_cursor", rows[len(rows)-1].ID).Error
			})
			return false, err
		}
	}
	return true, nil
}
