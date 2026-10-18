package models

import "time"

const MaxProfileFields = 16

var ReactionAcceptances = []string{"all", "likeOnlyForRemote", "nonSensitiveOnly", "nonSensitiveOnlyForLocalLikeOnlyForRemote", "likeOnly"}

// AllowsEmojiReaction applies the author's current account preference. Removing
// an earlier reaction remains possible after the author changes this setting.
func (a *Account) AllowsEmojiReaction(actor *Account, name string, emoji *Emoji) bool {
	if a == nil {
		return true
	}
	remote := actor != nil && !actor.IsLocal()
	likeOnly := a.ReactionAcceptance == "likeOnly" || (remote && (a.ReactionAcceptance == "likeOnlyForRemote" || a.ReactionAcceptance == "nonSensitiveOnlyForLocalLikeOnlyForRemote"))
	if likeOnly {
		return name == "❤" || name == "❤️"
	}
	nonSensitive := a.ReactionAcceptance == "nonSensitiveOnly" || a.ReactionAcceptance == "nonSensitiveOnlyForLocalLikeOnlyForRemote"
	return !nonSensitive || emoji == nil || !emoji.IsSensitive
}

// A durable queue contains identifiers only; profile URLs live in AccountExtra.
type ProfileFieldVerification struct {
	AccountID     uint64    `gorm:"primaryKey;autoIncrement:false"`
	Version       uint64    `gorm:"not null;default:1"`
	State         string    `gorm:"size:16;not null;default:'pending';index"`
	LeaseToken    string    `gorm:"size:36"`
	Attempts      int       `gorm:"not null;default:0"`
	NextAttemptAt time.Time `gorm:"not null;index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (ProfileFieldVerification) TableName() string { return "profile_field_verifications" }
