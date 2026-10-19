package models

import "gorm.io/gorm"

// HiddenFromVisitors reports whether a's posts are hidden from logged-out
// viewers and unsigned federation fetches.
func (a *Account) HiddenFromVisitors() bool { return a != nil && a.RequireSigninToView }

// StatusRequiresSignin reports whether the status, or the status it boosts,
// is by an account that requires sign-in to view.
//
// Only statuses a visitor could otherwise see (published public or unlisted)
// count: a private, direct, scheduled or deleted status must get the
// handler's 404 "Record not found", since a "please sign in" answer would
// confirm that it exists.
func StatusRequiresSignin(conn *gorm.DB, statusID uint64) bool {
	var n int64
	conn.Table("statuses AS s").
		Joins("LEFT JOIN statuses r ON r.id = s.reblog_id").
		Joins("JOIN accounts a ON a.id = s.account_id OR a.id = r.account_id").
		Where("s.id = ? AND a.require_signin_to_view = ?", statusID, true).
		Where("s.visibility IN ? AND s.deleted_at IS NULL AND s.scheduled_at IS NULL", []int{StatusVisibilityPublic, StatusVisibilityUnlisted}).
		Count(&n)
	return n > 0
}

// PollStatusID returns the status a poll belongs to (0 when it is missing).
func PollStatusID(conn *gorm.DB, pollID uint64) uint64 {
	var ids []uint64
	conn.Model(&Poll{}).Where("id = ?", pollID).Limit(1).Pluck("status_id", &ids)
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

// StatusIsRemote reports whether the status was fetched from another server.
func StatusIsRemote(conn *gorm.DB, statusID uint64) bool {
	var n int64
	conn.Model(&Status{}).Where("id = ? AND origin_uri IS NOT NULL AND origin_uri <> ''", statusID).Count(&n)
	return n > 0
}
