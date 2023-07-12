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
		&models.UserSetting{},
		&models.Emoji{},
		&models.Account{},
		&models.AccountExtra{},
		&models.Role{},
		&models.Rule{},
		&models.InviteCode{},
		&models.Follow{},
		&models.RoleUser{},
		&models.Emoji{},
		&models.AccountEmoji{},
		&models.AccountField{},
		&models.AuthToken{},
		&models.Application{},
		&models.ApplicationUserAuth{},
		&models.UserMeta{},
		&models.List{},
		&models.ListAccount{},
		&models.Post{},
		&models.PostContent{},
	)
}

func (CreateInitTables) Down(db *gorm.DB) (err error) {
	return db.Migrator().DropTable(
		&models.User{},
		&models.SystemSetting{},
		&models.UserSecretData{},
		&models.UserSetting{},
		&models.Emoji{},
		&models.Account{},
		&models.AccountExtra{},
		&models.Rule{},
		&models.InviteCode{},
		&models.Follow{},
		&models.RoleUser{},
		&models.Emoji{},
		&models.AccountEmoji{},
		&models.AccountField{},
		&models.AuthToken{},
		&models.Application{},
		&models.ApplicationUserAuth{},
		&models.UserMeta{},
		&models.List{},
		&models.ListAccount{},
		&models.Post{},
		&models.PostContent{},
	)
}
