package homeserver

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MatrixSyncToDeviceBatch retains a bounded, unacknowledged inbox snapshot.
// Payloads stay in matrix_to_device_messages; this is distinct from federation
// transaction de-duplication. The composite primary key is the device lock.
type MatrixSyncToDeviceBatch struct {
	UserID     string    `gorm:"primaryKey;type:varchar(255)"`
	DeviceID   string    `gorm:"primaryKey;type:varchar(255)"`
	BatchID    string    `gorm:"type:varchar(36);not null"`
	MessageIDs []uint64  `gorm:"type:json;serializer:json"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`
}

func (MatrixSyncToDeviceBatch) TableName() string { return "matrix_sync_to_device_batches" }

func toDeviceAck(token string) string {
	_, batch, ok := strings.Cut(token, "_d")
	if !ok || len(batch) != 36 {
		return ""
	}
	if _, err := uuid.Parse(batch); err != nil {
		return ""
	}
	return batch
}

func (s *SyncService) getToDeviceMessages(userID, deviceID, since string) (*SyncToDevice, string, error) {
	result := &SyncToDevice{Events: make([]*ToDeviceEvent, 0)}
	conn := s.syncDB()
	var batch MatrixSyncToDeviceBatch
	err := conn.Where("user_id = ? AND device_id = ?", userID, deviceID).Limit(1).Find(&batch).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", err
	}
	// Idle syncs do not write a lock row or open a transaction. A message arriving
	// after this check remains in the inbox for the next notification/sync.
	if batch.BatchID == "" {
		var message MatrixToDeviceMessage
		peek := conn.Select("id").Where("target_user = ? AND target_device = ?", userID, deviceID).Limit(1).Find(&message)
		err = peek.Error
		if err == nil && peek.RowsAffected == 0 {
			return result, "", nil
		}
		if err != nil {
			return nil, "", err
		}
	}
	var batchID string
	err = conn.Transaction(func(tx *gorm.DB) error {
		lock := MatrixSyncToDeviceBatch{UserID: userID, DeviceID: deviceID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&lock).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND device_id = ?", userID, deviceID).Take(&batch).Error; err != nil {
			return err
		}
		acknowledged := batch.BatchID != "" && toDeviceAck(since) == batch.BatchID
		if acknowledged {
			if len(batch.MessageIDs) > 0 {
				if err := tx.Where("target_user = ? AND target_device = ? AND id IN ?", userID, deviceID, batch.MessageIDs).Delete(&MatrixToDeviceMessage{}).Error; err != nil {
					return err
				}
			}
			batch.BatchID = ""
			batch.MessageIDs = nil
		}
		var messages []MatrixToDeviceMessage
		query := tx.Where("target_user = ? AND target_device = ?", userID, deviceID).Order("id ASC").Limit(100)
		if batch.BatchID != "" {
			query = query.Where("id IN ?", batch.MessageIDs)
		}
		if err := query.Find(&messages).Error; err != nil {
			return err
		}
		if batch.BatchID == "" && len(messages) > 0 {
			batch.BatchID = uuid.NewString()
			batch.MessageIDs = make([]uint64, len(messages))
			for i, message := range messages {
				batch.MessageIDs[i] = message.ID
			}
			if err := tx.Save(&batch).Error; err != nil {
				return err
			}
		} else if acknowledged {
			if err := tx.Save(&batch).Error; err != nil {
				return err
			}
		}
		batchID = batch.BatchID
		for _, message := range messages {
			result.Events = append(result.Events, &ToDeviceEvent{Sender: message.Sender, Type: message.EventType, Content: message.Content})
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return result, batchID, nil
}
