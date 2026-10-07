package admin

import (
	"context"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Reuse model deletion hooks (durable federation Delete, dependent rows and
// counters), but expose the cache/feed/search events only after commit.
func deleteAdminStatus(ctx context.Context, conn *gorm.DB, id uint64) (*models.Status, error) {
	ctx, publish := models.BeginDeferredStatusEvents(ctx)
	status := &models.Status{}
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := models.StatusLoadAllQuery(tx.Clauses(clause.Locking{Strength: "UPDATE"})).First(status, id).Error; err != nil {
			return err
		}
		return tx.Delete(status).Error
	})
	if err != nil {
		return nil, err
	}
	publish()
	return status, nil
}
