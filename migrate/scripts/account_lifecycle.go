package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"time"
)

type AccountLifecycleScript struct{}
type lifecycleAccountColumns struct {
	AlsoKnownAs         *string `gorm:"type:text"`
	MovedTo             *string `gorm:"type:varchar(2048)"`
	DeletionRequestedAt *time.Time
}

func (lifecycleAccountColumns) TableName() string { return "accounts" }
func (AccountLifecycleScript) Up(db *gorm.DB) error {
	if db.Migrator().HasTable("accounts") {
		for _, col := range []string{"also_known_as", "moved_to", "deletion_requested_at"} {
			if !db.Migrator().HasColumn(&lifecycleAccountColumns{}, col) {
				if err := db.Migrator().AddColumn(&lifecycleAccountColumns{}, col); err != nil {
					return err
				}
			}
		}
	}
	return db.AutoMigrate(&models.AccountDeletion{})
}
func (AccountLifecycleScript) Down(*gorm.DB) error { return nil }
