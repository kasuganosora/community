package models

import (
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// MatrixDMEventJob stores identifiers only. It commits with the action it mirrors.
// Source receipts and event references outlive status deletion.
type MatrixDMEventJob struct {
	ID            string `gorm:"primaryKey;size:160"`
	ReactionID    uint64
	EmojiID       uint64
	ReactionName  string `gorm:"size:320"`
	Kind          string `gorm:"size:32;not null"`
	StatusID      uint64 `gorm:"index"`
	AccountID     uint64
	RoomID        string    `gorm:"size:255"`
	EventID       string    `gorm:"size:255"`
	State         string    `gorm:"size:16;index;not null"`
	NextAttemptAt time.Time `gorm:"index"`
	Attempts      int
	LastError     string `gorm:"size:96"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func QueueMatrixDirectDeletion(tx *gorm.DB, status *Status) error {
	if status == nil || status.Id == 0 || !tx.Migrator().HasTable(&MatrixDMEventJob{}) {
		return nil
	}
	var source Status
	if err := tx.Select("id", "account_id", "visibility").Where("id = ?", status.Id).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if source.Visibility != StatusVisibilityDirect {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MatrixDMEventJob{ID: fmt.Sprintf("ap-delete:%d", source.Id), Kind: "ap_delete", StatusID: source.Id, AccountID: source.AccountId, State: "pending", NextAttemptAt: time.Now()}).Error
}

func QueueMatrixDirectReaction(tx *gorm.DB, kind string, reaction *StatusReaction, status *Status) error {
	if status.Visibility != StatusVisibilityDirect || !tx.Migrator().HasTable(&MatrixDMEventJob{}) {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MatrixDMEventJob{ID: fmt.Sprintf("ap-%s:%d", kind, reaction.ID), Kind: kind, StatusID: status.Id, AccountID: reaction.AccountID, ReactionID: reaction.ID, EmojiID: reaction.EmojiID, ReactionName: reaction.Name, State: "pending", NextAttemptAt: time.Now()}).Error
}

// Plain ActivityPub Like is a favourite, not an emoji reaction. Remote-only
// input avoids manufacturing a new local reaction or echoing a Matrix action.
func QueueMatrixDirectFavourite(tx *gorm.DB, kind string, favorite *FavoriteStatus) error {
	if favorite == nil || favorite.Id == 0 || !tx.Migrator().HasTable(&MatrixDMEventJob{}) {
		return nil
	}
	var actor Account
	if err := tx.Select("id", "domain").First(&actor, favorite.AccountId).Error; err != nil {
		return err
	}
	if actor.Domain == nil {
		return nil
	}
	var status Status
	if err := tx.Select("id", "visibility").First(&status, favorite.StatusId).Error; err != nil {
		return err
	}
	if status.Visibility != StatusVisibilityDirect {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MatrixDMEventJob{ID: fmt.Sprintf("ap-%s:%d", kind, favorite.Id), Kind: kind, StatusID: favorite.StatusId, AccountID: favorite.AccountId, ReactionID: favorite.Id, State: "pending", NextAttemptAt: time.Now()}).Error
}
