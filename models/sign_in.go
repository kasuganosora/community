package models

import (
	"net"
	"strings"
	"time"

	"gorm.io/gorm"
)

// NormalizeSignInIP returns a canonical IP, or "" when the value is not an IP.
// Callers must not store hostnames or placeholders in its place.
func NormalizeSignInIP(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ""
	}

	return parsed.String()
}

// RecordSignIn rotates Mastodon-style sign-in columns after a successful web
// session is issued. The previous current values become last_*; a first
// sign-in leaves last_* untouched.
func RecordSignIn(tx *gorm.DB, userID uint64, ip string, at time.Time) error {
	if userID == 0 || at.IsZero() {
		return nil
	}

	var current User
	err := tx.Model(&User{}).
		Select("current_sign_in_at", "current_sign_in_ip", "sign_in_count").
		Where("id = ?", userID).
		Take(&current).Error
	if err != nil {
		return err
	}

	updates := map[string]any{
		"current_sign_in_at": at,
		"current_sign_in_ip": NormalizeSignInIP(ip),
		"sign_in_count":      current.SignInCount + 1,
	}
	if current.CurrentSignInAt != nil {
		updates["last_sign_in_at"] = *current.CurrentSignInAt
		updates["last_sign_in_ip"] = current.CurrentSignInIP
	}

	return tx.Model(&User{}).Where("id = ?", userID).UpdateColumns(updates).Error
}
