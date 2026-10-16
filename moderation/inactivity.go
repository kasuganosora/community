package moderation

import (
	"context"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// ModeratorInactivityLimit is Misskey's MODERATOR_INACTIVITY_LIMIT_DAYS.
const ModeratorInactivityLimit = 7 * 24 * time.Hour

// moderatorPermissions are the role permissions that count as a moderator
// (Misskey: isModerator || isAdministrator).
const moderatorPermissions = models.PermissionAdministrator | models.PermissionManageReports | models.PermissionManageUsers

// ModeratorActivity returns the most recent activity time of each local
// moderator that has any recorded activity (Misskey ignores moderators
// whose lastActiveDate is null). Activity is the latest of
// last_activity_at, last_status_at and feed_last_seen_at.
func ModeratorActivity(conn *gorm.DB) ([]time.Time, error) {
	var userIDs []uint64
	if err := conn.Model(&models.User{}).Where("is_admin = ?", true).Pluck("id", &userIDs).Error; err != nil {
		return nil, err
	}
	var roleUsers []uint64
	sub := conn.Model(&models.Role{}).Select("id").Where("(permissions & ?) <> 0", moderatorPermissions)
	if err := conn.Model(&models.RoleUser{}).Where("role_id IN (?)", sub).Pluck("user_id", &roleUsers).Error; err == nil {
		userIDs = append(userIDs, roleUsers...)
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	var rows []models.Account
	if err := conn.Model(&models.Account{}).Select("id", "last_activity_at", "last_status_at", "feed_last_seen_at").
		Where("user_id IN ? AND domain IS NULL", userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, len(rows))
	for _, a := range rows {
		var last time.Time
		for _, t := range []*time.Time{a.LastActivityAt, a.LastStatusAt, a.FeedLastSeenAt} {
			if t != nil && t.After(last) {
				last = *t
			}
		}
		if !last.IsZero() {
			out = append(out, last)
		}
	}
	return out, nil
}

// ModeratorsInactive reports whether every moderator with recorded activity
// has been inactive longer than ModeratorInactivityLimit at now. With no
// such moderators it reports false (nothing to judge by).
func ModeratorsInactive(activity []time.Time, now time.Time) bool {
	if len(activity) == 0 {
		return false
	}
	cutoff := now.Add(-ModeratorInactivityLimit)
	for _, t := range activity {
		if !t.Before(cutoff) {
			return false
		}
	}
	return true
}

// CheckModeratorInactivity closes registration when it is open, the
// automatic switch is enabled and all moderators are inactive. It returns
// true when it closed registration.
func CheckModeratorInactivity(ctx context.Context) (bool, error) {
	if !systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, false) || !Current().AutoCloseRegistration {
		return false, nil
	}
	conn := db.GetDB()
	if conn == nil {
		return false, nil
	}
	activity, err := ModeratorActivity(conn.WithContext(ctx))
	if err != nil {
		return false, err
	}
	now := time.Now()
	if !ModeratorsInactive(activity, now) {
		return false, nil
	}
	log.Warn("moderation: no moderator activity for %s; closing registration", ModeratorInactivityLimit)
	err = systemsettings.SaveChanges(map[string]any{
		systemsettings.KeyIsEnableRegistration: false,
		KeyRegistrationAutoClosedAt:            now.UTC().Format(time.RFC3339),
	})
	return err == nil, err
}

// StartInactivityChecker runs CheckModeratorInactivity hourly (Misskey's
// checkModeratorsActivity queue job) until ctx is done.
func StartInactivityChecker(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if _, err := CheckModeratorInactivity(ctx); err != nil {
				log.Warn("moderation: moderator activity check failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// TouchModerator records moderator activity for an account, e.g. when an
// admin opens the moderation settings.
func TouchModerator(conn *gorm.DB, accountID uint64) {
	if conn == nil || accountID == 0 {
		return
	}
	_ = conn.Model(&models.Account{}).Where("id = ?", accountID).Update("last_activity_at", time.Now()).Error
}
