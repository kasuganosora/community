package models

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Prevent an in-flight upload/post which authenticated before deletion from
// recreating private content after the erasure worker has already visited it.
// The account lock serializes creation with RequestDeletion's account lock.
func ensureAccountWritable(tx *gorm.DB, id uint64) error {
	if id == 0 || !tx.Migrator().HasTable(&AccountDeletion{}) {
		return nil
	}
	var account Account
	err := tx.Session(&gorm.Session{NewDB: true}).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "deletion_requested_at").Where("id = ? AND domain IS NULL", id).Take(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if account.DeletionRequestedAt != nil {
		return errors.New("account is scheduled for deletion")
	}
	return nil
}

func (m *MediaAttachment) BeforeCreate(tx *gorm.DB) error {
	return ensureAccountWritable(tx, m.AccountId)
}
