package scripts

import (
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/log"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// RepairMatrixHSTablesScript drops the Matrix homeserver tables that the old
// MigrateMatrixHSScript created with DDL that conflicts with the homeserver
// models, so that homeserver.autoMigrate() can recreate them correctly on the
// next start.
//
// Only empty tables are dropped. With the old DDL in place the homeserver
// failed to initialise on MySQL/MariaDB, so these tables cannot hold data in
// practice; a non-empty table is left untouched and reported instead of
// risking data loss.
type RepairMatrixHSTablesScript struct{}

func (RepairMatrixHSTablesScript) Up(db *gorm.DB) error {
	if database.IsXmlMode() {
		return nil
	}

	m := db.Migrator()

	for _, table := range legacyMatrixHSTables {
		if !m.HasTable(table) {
			continue
		}

		var rows int64
		// Raw queries on the migration's own connection (see
		// UnifyMediaAttachmentsScript for why Migrator().DropTable is avoided).
		if err := db.Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&rows).Error; err != nil {
			return errors.Wrapf(err, "count rows in %s", table)
		}

		if rows > 0 {
			log.Warn("Matrix table %s has %d rows; not dropping it. If the homeserver fails to migrate, "+
				"compare it with the models in matrix/homeserver and fix it manually.", table, rows)

			continue
		}

		if err := db.Exec("DROP TABLE IF EXISTS `" + table + "`").Error; err != nil {
			return errors.Wrapf(err, "drop legacy matrix table %s", table)
		}
	}

	return nil
}

func (RepairMatrixHSTablesScript) Down(_ *gorm.DB) error {
	// Nothing to restore: the homeserver recreates its tables on start.
	return nil
}
