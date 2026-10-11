package scripts

import (
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type FeedOutboxScript struct{}

func (FeedOutboxScript) Up(conn *gorm.DB) error {
	if err := conn.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}); err != nil {
		return err
	}
	if conn.Migrator().HasTable(&models.Status{}) && !conn.Migrator().HasColumn(&models.Status{}, "FeedRevision") {
		if err := conn.Migrator().AddColumn(&models.Status{}, "FeedRevision"); err != nil {
			return err
		}
	}
	if conn.Migrator().HasTable(&models.Account{}) && !conn.Migrator().HasColumn(&models.Account{}, "FeedLastSeenAt") {
		if err := conn.Migrator().AddColumn(&models.Account{}, "FeedLastSeenAt"); err != nil {
			return err
		}
	}
	if conn.Migrator().HasTable(&models.Account{}) && !conn.Migrator().HasIndex(&models.Account{}, "FeedLastSeenAt") {
		return conn.Migrator().CreateIndex(&models.Account{}, "FeedLastSeenAt")
	}
	return nil
}
func (FeedOutboxScript) Down(*gorm.DB) error {
	return fmt.Errorf("feed outbox rollback would discard durable delivery state")
}
