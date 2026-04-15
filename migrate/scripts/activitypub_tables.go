package scripts

import (
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// CreateActivityPubTablesScript registers the ActivityPub table migration in the migration framework.
// Up is safe: uses GORM AutoMigrate which is idempotent.
type CreateActivityPubTablesScript struct{}

func (CreateActivityPubTablesScript) Up(db *gorm.DB) (err error) {
	return CreateActivityPubTables()
}

func (CreateActivityPubTablesScript) Down(db *gorm.DB) (err error) {
	return db.Migrator().DropTable(
		&activitypub.Delivery{},
		&activitypub.ProcessedActivity{},
		&models.ProxyFollow{},
		&models.AccountKey{},
	)
}

// CreateActivityPubTables creates tables needed for ActivityPub federation
func CreateActivityPubTables() error {
	log.Info("Creating ActivityPub tables...")

	// Auto-migrate delivery queue table and proxy follow tables
	if err := db.GetDB().AutoMigrate(
		&activitypub.Delivery{},
		&activitypub.ProcessedActivity{},
		&models.ProxyFollow{},
		&models.AccountKey{},
	); err != nil {
		log.Error("Failed to create ActivityPub tables: %v", err)
		return err
	}

	log.Info("ActivityPub tables created successfully")
	return nil
}

// RunActivityPubMigrations runs ActivityPub related migrations
func RunActivityPubMigrations() error {
	return CreateActivityPubTables()
}
