package models

import "gorm.io/gorm"

// ReactionNameSQL compares the actual encoded reaction, never a linguistic
// collation. MySQL's utf8mb4_unicode_ci considers distinct supplementary-plane
// emoji equal (and also folds variation selectors/case). Schema defaults may
// legitimately use that collation for human text; reaction identity must not.
// The fixed, qualified column name is safe to compose into equality/group SQL.
func ReactionNameSQL(conn *gorm.DB) string {
	switch conn.Dialector.Name() {
	case "mysql":
		return "CAST(status_reactions.name AS BINARY)"
	case "sqlite":
		return "status_reactions.name COLLATE BINARY"
	default:
		return "status_reactions.name"
	}
}
