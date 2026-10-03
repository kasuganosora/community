package scripts

import (
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type StatusReactionsScript struct{}

func (StatusReactionsScript) Up(db *gorm.DB) error { return db.AutoMigrate(&models.StatusReaction{}) }
func (StatusReactionsScript) Down(*gorm.DB) error {
	return fmt.Errorf("reaction migration rollback would discard user interactions")
}
