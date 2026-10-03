package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
)

// RemoteReactionActivity remembers accepted Likes and their revocations. An
// Undo can arrive before its Like; the retained tombstone prevents resurrection.
// Keys include the authenticated actor, so a peer cannot suppress another
// actor's activity by embedding that actor's ID in its own signed Undo.
// Hashing bounds MySQL index size without truncating opaque ActivityPub IDs.
type RemoteReactionActivity struct {
	AccountID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	ActivityHash string `gorm:"type:char(64);primaryKey"`
	ActivityURI  string `gorm:"type:text;not null"`
	StatusID     uint64 `gorm:"not null;index"`
	Name         string `gorm:"type:varchar(320);not null"`
	Undone       bool   `gorm:"not null;default:false"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Called under the target status lock and in the same transaction as reaction
// and notification mutations. No marker survives a failed reaction mutation.
func recordRemoteReactionActivity(tx *gorm.DB, actor *Account, statusID uint64, name, uri string, undo bool) (bool, error) {
	if actor.Domain == nil {
		return true, nil
	}
	if uri == "" || len(uri) > 2048 {
		return false, ErrInvalidReaction
	}
	digest := sha256.Sum256([]byte(uri))
	hash := hex.EncodeToString(digest[:])
	var previous RemoteReactionActivity
	err := tx.Where("account_id = ? AND activity_hash = ?", actor.Id, hash).First(&previous).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if err == nil {
		if previous.ActivityURI != uri || previous.StatusID != statusID || previous.Name != name {
			return false, ErrInvalidReaction
		}
		if !undo || previous.Undone {
			return false, nil
		}
		return true, tx.Model(&RemoteReactionActivity{}).Where("account_id = ? AND activity_hash = ?", actor.Id, hash).UpdateColumn("undone", true).Error
	}
	row := RemoteReactionActivity{AccountID: actor.Id, ActivityHash: hash, ActivityURI: uri, StatusID: statusID, Name: name, Undone: undo}
	return true, tx.Create(&row).Error
}
