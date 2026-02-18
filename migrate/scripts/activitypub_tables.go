package scripts

import (
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// CreateActivityPubTables creates tables needed for ActivityPub federation
func CreateActivityPubTables() error {
	log.Info("Creating ActivityPub tables...")

	// Auto-migrate delivery queue table
	if err := db.GetDB().AutoMigrate(&activitypub.Delivery{}); err != nil {
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
