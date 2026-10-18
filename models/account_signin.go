package models

import "gorm.io/gorm"

// HiddenFromVisitors reports whether a's posts are hidden from logged-out
// viewers and unsigned federation fetches.
func (a *Account) HiddenFromVisitors() bool { return a != nil && a.RequireSigninToView }

// StatusRequiresSignin reports whether the status, or the status it boosts,
// is by an account that requires sign-in to view.
func StatusRequiresSignin(conn *gorm.DB, statusID uint64) bool {
	var n int64
	conn.Table("statuses AS s").
		Joins("LEFT JOIN statuses r ON r.id = s.reblog_id").
		Joins("JOIN accounts a ON a.id = s.account_id OR a.id = r.account_id").
		Where("s.id = ? AND a.require_signin_to_view = ?", statusID, true).
		Count(&n)
	return n > 0
}
