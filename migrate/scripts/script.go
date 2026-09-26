package scripts

import (
	"gorm.io/gorm"
	"reflect"
)

var ScripsType = []MigrationScript{
	CreateInitTables{},
	InitSystemConfigOptions{},
	AddRoles{},
	AddOAuthTables{},

	// Previously unregistered scripts — all Up() methods are safe (use IF NOT EXISTS / idempotent):
	CreateActivityPubTablesScript{},
	MigrateGroupTablesScript{},
	MigrateMatrixTablesScript{},
	MigrateMatrixHSScript{},
	CreateMissingModelTablesScript{},
	MigrateMediaTablesScript{},
	CreatePerformanceIndexesScript{},
	MigrateRelayTableScript{},
}

type MigrationScript interface {
	Up(db *gorm.DB) error
	Down(db *gorm.DB) error
}

func GetScripts() map[string]MigrationScript {
	ret := make(map[string]MigrationScript)

	for _, script := range ScripsType {
		rType := reflect.TypeOf(script)
		ret[rType.Name()] = script
	}

	return ret
}

func GetScriptsName() []string {
	ret := make([]string, 0, len(ScripsType))

	for _, script := range ScripsType {
		rType := reflect.TypeOf(script)
		ret = append(ret, rType.Name())
	}

	return ret
}
