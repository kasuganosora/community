package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type MastodonAPIFieldsScript struct{}

func (MastodonAPIFieldsScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	columns := []struct {
		model any
		name  string
	}{
		{&models.List{}, "Exclusive"},
		{&models.ApplicationUserAuth{}, "ScopesExact"},
		{&models.Application{}, "RegisteredScopes"},
		{&models.MuteAccount{}, "Notifications"},
		{&models.Account{}, "DiscoverableConfigured"},
		{&models.Account{}, "CollectionsConfigured"},
	}
	for _, column := range columns {
		if !m.HasColumn(column.model, column.name) {
			if err := m.AddColumn(column.model, column.name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (MastodonAPIFieldsScript) Down(*gorm.DB) error { return nil }
