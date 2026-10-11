package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Indexed aliases make prefix-free Matrix account resolution bounded.
type MatrixAliasScript struct{}

func (MatrixAliasScript) Up(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn(&models.Account{}, "MatrixAlias") {
		if err := tx.Migrator().AddColumn(&models.Account{}, "MatrixAlias"); err != nil {
			return err
		}
	}
	if !tx.Migrator().HasIndex(&models.Account{}, "idx_account_matrix_alias") {
		if err := tx.Migrator().CreateIndex(&models.Account{}, "idx_account_matrix_alias"); err != nil {
			return err
		}
	}
	return models.BackfillMatrixAliases(tx)
}
func (MatrixAliasScript) Down(*gorm.DB) error { return nil }
