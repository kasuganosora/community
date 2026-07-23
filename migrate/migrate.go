package migrate

import (
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/migrate/scripts"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

type Migration struct {
	ID    int64  `gorm:"primary_key;autoIncrement"         json:"id"`
	Name  string `gorm:"unique;not null;type:varchar(128)" json:"name"`
	Batch int    `gorm:"not null;default:0"                json:"batch"`
}

func (Migration) TableName() string {
	return "migrations"
}

// 检查迁移表是否存在，如果不存在则创建.
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

		return err
	}

	unExecutedMigrationsScriptsName, err := getUnExecutedMigrationsScript(db)
	if err != nil {
		log.Fatal("Failed to get unexecuted migrations script: %v", err)

		return err
	}

	if len(unExecutedMigrationsScriptsName) == 0 {
		log.Info("No migrations to execute")

		return err
	}

	// 获得当前最后一个批次数字
	lastBatch, err := getLastBatchNum(db)
	if err != nil {
		log.Fatal("Failed to get last batch number: %v", err)

		return err
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

			return err
		}

		// 成功执行则把当前脚本加入到迁移表中
		migRecord := &Migration{
			Name:  name,
			Batch: batch,
		}

		err = db.Create(migRecord).Error
		if err != nil {
			err = errors.Wrapf(err, "Failed to insert migration record %s", name)

			return err
		}

		log.Info("Executing migration script %s done!", name)
	}

	return err
}

// Down 回滚
// 1 次回滚操作，回滚 1 个批次的迁移.
func Down(db *gorm.DB) (err error) {
	lastBatch, err := getLastBatchNum(db)
	if err != nil {
		log.Fatal("Failed to get last batch number: %v", err)

		return err
	}

	if lastBatch == 0 {
		log.Info("No migrations to rollback")

		return err
	}

	// 获得最后一个批次的迁移记录
	var migRecords []*Migration

	err = db.Where("batch = ?", lastBatch).Find(&migRecords).Error
	if err != nil {
		err = errors.Wrapf(err, "Failed to get migration records of batch %d", lastBatch)

		return err
	}

	// 进行回滚
	for _, migRecord := range migRecords {
		scriptType, ok := scripts.GetScripts()[migRecord.Name]
		if !ok {
			// 找不到对应会滚结果,删除迁移记录, 并且跳过
			log.Warn("Failed to find rollback script for migration %s", migRecord.Name)

			err = db.Delete(migRecord).Error
			if err != nil {
				err = errors.Wrapf(err, "Failed to delete migration record %s", migRecord.Name)

				return err
			}

			continue
		}

		log.Info("Rollback migration script %s", migRecord.Name)

		err = scriptType.Down(db)
		if err != nil {
			err = errors.Wrapf(err, "Failed to rollback migration script %s", migRecord.Name)

			return err
		}

		// 成功回滚则删除迁移记录
		err = db.Delete(migRecord).Error
		if err != nil {
			err = errors.Wrapf(err, "Failed to delete migration record %s", migRecord.Name)

			return err
		}

		log.Info("Rollback migration script %s done!", migRecord.Name)
	}

	return err
}

func getLastBatchNum(db *gorm.DB) (lastBatch int, err error) {
	var m Migration

	err = db.Order("batch desc").First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
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
