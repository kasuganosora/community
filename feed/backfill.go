package feed

import (
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// ExcludeBackfilled drops statuses imported long after publication (see
// models.StatusPropBackfilled) from a timeline query. Keeping them out before
// LIMIT keeps id pagination gap-free. Rows without properties are included.
func ExcludeBackfilled(tx *gorm.DB) *gorm.DB {
	marker := dialect.JSONText(tx, "statuses.properties", models.StatusPropBackfilled)
	if dialect.IsMySQL(tx) {
		// MySQL JSON_EXTRACT returns a JSON string (including its quotes),
		// while PostgreSQL ->> and SQLite json_extract return SQL text.
		marker = "JSON_UNQUOTE(" + marker + ")"
	}
	return tx.Where("COALESCE(" + marker + ", '') <> '1'")
}

// SkipsTimelines reports whether a status must not be pushed to, or removed
// from, the shared timelines.
func SkipsTimelines(status *models.Status) bool {
	return status.IsBackfilled()
}
