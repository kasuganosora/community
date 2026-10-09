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
	return tx.Where("COALESCE(" + dialect.JSONText(tx, "statuses.properties", models.StatusPropBackfilled) + ", '') <> '1'")
}

// SkipsTimelines reports whether a status must not be pushed to, or removed
// from, the shared timelines.
func SkipsTimelines(status *models.Status) bool {
	return status.IsBackfilled()
}
