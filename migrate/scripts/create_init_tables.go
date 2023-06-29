package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type CreateInitTables struct {
}

func (CreateInitTables) Up(db *gorm.DB) (err error) {
	return db.Migrator().CreateTable(
		&models.User{},
		&models.SystemSetting{},
		&models.UserSecretData{},
		&models.UserSetting{})
}

func (CreateInitTables) Down(db *gorm.DB) (err error) {
	return db.Migrator().DropTable(
		&models.User{},
		&models.SystemSetting{},
		&models.UserSecretData{},
		&models.UserSetting{})
}
