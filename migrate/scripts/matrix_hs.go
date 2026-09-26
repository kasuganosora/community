package scripts

import (
	"github.com/kasuganosora/community/db/database"
	"gorm.io/gorm"
)

// legacyMatrixHSTables are the tables the old MigrateMatrixHSScript created
// with hand-written DDL. That DDL disagreed with the GORM models in
// matrix/homeserver (different primary keys, columns and unique indexes), so
// the homeserver's own AutoMigrate failed on top of it ("Multiple primary key
// defined" for matrix_presence / matrix_cross_signing_keys) and the
// homeserver could never be enabled on MySQL/MariaDB.
var legacyMatrixHSTables = []string{
	"matrix_cross_signing_keys", "matrix_one_time_keys", "matrix_device_keys",
	"matrix_room_aliases", "matrix_presence", "matrix_filters",
	"matrix_typing", "matrix_account_data", "matrix_receipts",
	"matrix_to_device_messages", "matrix_user_sessions", "matrix_room_members",
	"matrix_room_state", "matrix_rooms_hs", "matrix_events",
}

// MigrateMatrixHSScript used to create the Matrix homeserver tables from
// hand-written SQL. The homeserver package now owns its schema: its tables
// are created by homeserver.autoMigrate() from the GORM models when the
// homeserver starts. Up is kept as a no-op so the migration history stays
// stable; RepairMatrixHSTablesScript removes tables created by the old Up.
type MigrateMatrixHSScript struct{}

func (MigrateMatrixHSScript) Up(_ *gorm.DB) error {
	return nil
}

func (MigrateMatrixHSScript) Down(db *gorm.DB) (err error) {
	if database.IsXmlMode() {
		return nil
	}

	// Table names are constants, not user input.
	for _, table := range legacyMatrixHSTables {
		if err = db.Exec("DROP TABLE IF EXISTS `" + table + "`").Error; err != nil {
			return err
		}
	}

	return nil
}
