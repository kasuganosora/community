package migrate

import (
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/migrate/scripts"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

type Migration struct {
	ID    int64  `json:"id" gorm:"primary_key;autoIncrement"`
	Name  string `json:"name" gorm:"unique;not null;type:varchar(128)"`
	Batch int    `json:"batch" gorm:"not null;default:0"`
}

func (Migration) TableName() string {
	return "migrations"
}

// 检查迁移表是否存在，如果不存在则创建
func checkMigrationsTable(db *gorm.DB) (err error) {
	if !db.Migrator().HasTable(&Migration{}) {
		err = db.Migrator().CreateTable(&Migration{})
	}

	return
}

func Up(db *gorm.DB) (err error) {
	err = checkMigrationsTable(db)
	if err != nil {
		log.Fatal("Failed to check migrations table: %v", err)
		return
	}

	unExecutedMigrationsScriptsName, err := getUnExecutedMigrationsScript(db)
	if err != nil {
		log.Fatal("Failed to get unexecuted migrations script: %v", err)
		return
	}

	if len(unExecutedMigrationsScriptsName) == 0 {
		log.Info("No migrations to execute")
		return
	}

	// 获得当前最后一个批次数字
	lastBatch, err := getLastBatchNum(db)
	if err != nil {
		log.Fatal("Failed to get last batch number: %v", err)
		return
	}

	// 在当前最后一个批次数字上加一，执行之前没有执行过的迁移
	batch := lastBatch + 1

	for _, name := range unExecutedMigrationsScriptsName {
		// 执行脚本
		scriptType := scripts.GetScripts()[name]
		log.Info("Executing migration script %s", name)

		err = scriptType.Up(db)
		if err != nil {
			err = errors.Wrapf(err, "Failed to execute script %s", name)
			return
		}

		// 成功执行则把当前脚本加入到迁移表中
		migRecord := &Migration{
			Name:  name,
			Batch: batch,
		}

		err = db.Create(migRecord).Error
		if err != nil {
			err = errors.Wrapf(err, "Failed to insert migration record %s", name)
			return
		}

		log.Info("Executing migration script %s done!", name)
	}

	return
}

func getLastBatchNum(db *gorm.DB) (lastBatch int, err error) {
	var m Migration
	err = db.Order("batch desc").First(&m).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			lastBatch = 0
			err = nil
		}
		return
	}
	lastBatch = m.Batch
	return
}

func getUnExecutedMigrationsScript(db *gorm.DB) (unExecutedMigrationsScriptsName []string, err error) {
	allScriptsName := scripts.GetScriptsName()
	execMigrations := make([]*Migration, 0)

	err = db.Find(&execMigrations).Error
	if err != nil {
		return
	}

	execMigrationsNameCheckMap := make(map[string]bool)
	for _, m := range execMigrations {
		execMigrationsNameCheckMap[m.Name] = true
	}

	unExecutedMigrationsScriptsName = make([]string, 0)
	for _, name := range allScriptsName {
		if _, ok := execMigrationsNameCheckMap[name]; !ok {
			unExecutedMigrationsScriptsName = append(unExecutedMigrationsScriptsName, name)
		}
	}

	return

}
