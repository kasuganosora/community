package scripts

import (
	"github.com/kasuganosora/community/newacctmod"
	"gorm.io/gorm"
)

// NewAccountModScript creates review job and appeal tables for the
// new-account LLM federation gate.
type NewAccountModScript struct{}

func (NewAccountModScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(newacctmod.Models()...)
}

func (NewAccountModScript) Down(db *gorm.DB) error {
	for _, m := range newacctmod.Models() {
		if err := db.Migrator().DropTable(m); err != nil {
			return err
		}
	}
	return nil
}
