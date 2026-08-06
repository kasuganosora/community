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

// Up 执行所有未执行的迁移脚本.
//
// 每个迁移脚本的执行与其迁移记录的插入被包裹在一个数据库事务中：
// 如果脚本执行成功但记录插入失败（或反之），事务会回滚，避免出现
// "脚本已执行但未记录"或"记录已写入但脚本未执行"的不一致状态。
func Up(db *gorm.DB) (err error) {
	err = checkMigrationsTable(db)
	if err != nil {
		log.Error("Failed to check migrations table: %v", err)

		return errors.Wrap(err, "failed to check/create migrations table")
	}

	unExecutedMigrationsScriptsName, err := getUnExecutedMigrationsScript(db)
	if err != nil {
		log.Error("Failed to get unexecuted migrations script: %v", err)

		return errors.Wrap(err, "failed to get unexecuted migrations")
	}

	if len(unExecutedMigrationsScriptsName) == 0 {
		log.Info("No migrations to execute")

		return nil
	}

	// 获得当前最后一个批次数字
	lastBatch, err := getLastBatchNum(db)
	if err != nil {
		log.Error("Failed to get last batch number: %v", err)

		return errors.Wrap(err, "failed to get last batch number")
	}

	// 在当前最后一个批次数字上加一，执行之前没有执行过的迁移
	batch := lastBatch + 1

	allScripts := scripts.GetScripts()

	for _, name := range unExecutedMigrationsScriptsName {
		scriptType, ok := allScripts[name]
		if !ok {
			// 脚本已注册名但实现不存在，跳过并告警
			log.Warn("Migration script %s not found in registry, skipping", name)

			continue
		}

		log.Info("Executing migration script %s", name)

		// 将脚本执行与迁移记录写入放在同一事务中，保证原子性。
		// 注意：MySQL 的 DDL 语句会隐式提交，因此事务对纯 DDL 脚本可能
		// 无法完全回滚，但迁移记录的插入仍然受事务保护。
		err = db.Transaction(func(tx *gorm.DB) error {
			if txErr := scriptType.Up(tx); txErr != nil {
				return errors.Wrapf(txErr, "failed to execute script %s", name)
			}

			migRecord := &Migration{
				Name:  name,
				Batch: batch,
			}

			if createErr := tx.Create(migRecord).Error; createErr != nil {
				return errors.Wrapf(createErr, "failed to insert migration record %s", name)
			}

			return nil
		})

		if err != nil {
			log.Error("Migration script %s failed: %v", name, err)

			return err
		}

		log.Info("Executing migration script %s done!", name)
	}

	return nil
}

// Down 回滚
// 1 次回滚操作，回滚 1 个批次的迁移.
func Down(db *gorm.DB) (err error) {
	lastBatch, err := getLastBatchNum(db)
	if err != nil {
		log.Error("Failed to get last batch number: %v", err)

		return errors.Wrap(err, "failed to get last batch number")
	}

	if lastBatch == 0 {
		log.Info("No migrations to rollback")

		return nil
	}

	// 获得最后一个批次的迁移记录
	var migRecords []*Migration

	err = db.Where("batch = ?", lastBatch).Order("id DESC").Find(&migRecords).Error
	if err != nil {
		return errors.Wrapf(err, "failed to get migration records of batch %d", lastBatch)
	}

	allScripts := scripts.GetScripts()

	// 进行回滚（按 id 倒序回滚，保证依赖顺序正确）
	for _, migRecord := range migRecords {
		scriptType, ok := allScripts[migRecord.Name]
		if !ok {
			// 找不到对应回滚脚本，删除迁移记录，并且跳过
			log.Warn("Failed to find rollback script for migration %s", migRecord.Name)

			if delErr := db.Delete(migRecord).Error; delErr != nil {
				return errors.Wrapf(delErr, "failed to delete migration record %s", migRecord.Name)
			}

			continue
		}

		log.Info("Rollback migration script %s", migRecord.Name)

		// 将脚本回滚与迁移记录删除放在同一事务中，保证原子性。
		// 注意：MySQL 的 DDL 语句会隐式提交，因此事务对纯 DDL 脚本可能
		// 无法完全回滚，但迁移记录的删除仍然受事务保护。
		err = db.Transaction(func(tx *gorm.DB) error {
			if downErr := scriptType.Down(tx); downErr != nil {
				return errors.Wrapf(downErr, "failed to rollback migration script %s", migRecord.Name)
			}

			if delErr := tx.Delete(migRecord).Error; delErr != nil {
				return errors.Wrapf(delErr, "failed to delete migration record %s", migRecord.Name)
			}

			return nil
		})

		if err != nil {
			log.Error("Rollback migration script %s failed: %v", migRecord.Name, err)

			return err
		}

		log.Info("Rollback migration script %s done!", migRecord.Name)
	}

	return nil
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
