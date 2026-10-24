package setup

import (
	"context"
	"errors"
	"os"

	"github.com/joho/godotenv"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/migrate"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SupportedLocales are the UI languages the wizard offers.
var SupportedLocales = []string{"zh-CN", "zh-TW", "ja", "en", "ru", "fr", "de"}

func validLocale(l string) bool {
	for _, s := range SupportedLocales {
		if s == l {
			return true
		}
	}
	return false
}

func commonStages() map[string]stageFunc {
	return map[string]stageFunc{
		"config":   stageConfig,
		"schema":   stageSchema,
		"admin":    stageAdmin,
		"finalize": stageFinalize,
	}
}

// stageConfig writes the core settings and generated secrets to .env
// (atomically, 0600), marks setup as pending and switches this process to
// the new configuration.
func stageConfig(_ context.Context, r *runner) error {
	job := r.t.snapshot()
	target := job.Target
	target.Password = r.secrets.DatabasePassword
	rp := job.Redis
	rp.Password = r.secrets.RedisPassword
	values := envValues(job.BaseURL, target, rp)
	existing, err := godotenv.Read(config.EnvFile())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail("config_write_failed", "the existing .env cannot be read")
	}
	for k, v := range generatedSecrets(existing) {
		values[k] = v
	}
	if err = markPending(); err != nil {
		return fail("config_write_failed", "the configuration directory is not writable")
	}
	if err = WriteEnvFile(config.EnvFile(), values); err != nil {
		return fail("config_write_failed", "the configuration directory is not writable")
	}
	r.t.logf("info", "configuration written to %s (mode 0600)", config.EnvFile())
	config.Reload()
	cRedis.Init()
	_ = database.CloseDB()
	database.InitGorm()
	if database.InitError() != nil || db.GetDB() == nil {
		return fail("database_connect_failed")
	}
	return nil
}

func requireDB() (*gorm.DB, error) {
	if db.GetDB() == nil {
		database.InitGorm()
	}
	if db.GetDB() == nil {
		return nil, fail("database_connect_failed")
	}
	return db.GetDB(), nil
}

func stageSchema(_ context.Context, r *runner) error {
	conn, err := requireDB()
	if err != nil {
		return err
	}
	if err = migrate.Up(conn); err != nil {
		r.t.logf("error", "schema migration failed: %s", sanitize(err.Error()))
		return fail("schema_failed")
	}
	r.t.logf("info", "database schema is up to date")
	return nil
}

// stageAdmin creates the first administrator with the owner role. In a
// Misskey migration the username may already exist (imported with its
// Misskey password); that account is promoted instead and keeps its
// password.
func stageAdmin(_ context.Context, r *runner) error {
	conn, err := requireDB()
	if err != nil {
		return err
	}
	job := r.t.snapshot()
	var owner models.Role
	if err = conn.Where("name = ?", "owner").First(&owner).Error; err != nil {
		return fail("admin_failed", "owner role missing")
	}
	var user models.User
	err = conn.Where("username = ?", job.Admin).First(&user).Error
	switch {
	case err == nil:
		if job.Mode == "fresh" {
			return fail("admin_exists")
		}
		r.t.logf("warn", "user @%s already exists (imported); it is promoted to owner and keeps its existing password", job.Admin)
		err = conn.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.User{}).Where("id = ?", user.Id).Updates(map[string]any{"is_admin": true}).Error; err != nil {
				return err
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.RoleUser{RoleID: uint64(owner.ID), UserID: user.Id}).Error //nolint:gosec // positive id
		})
	case errors.Is(err, gorm.ErrRecordNotFound):
		var taken int64
		conn.Model(&models.User{}).Where("email_hash = ?", models.GetEmailHash(job.Email)).Count(&taken)
		if taken > 0 {
			return fail("admin_email_taken")
		}
		user = models.User{UserName: job.Admin, EmailHash: models.GetEmailHash(job.Email), IsAdmin: true, IsMailVerified: true, Status: models.UserStatusNormal}
		err = conn.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
			if err := tx.Create(&models.UserSecretData{UserId: user.Id, Salt: r.secrets.AdminSalt, Password: r.secrets.AdminHash, Email: job.Email}).Error; err != nil {
				return err
			}
			if err := tx.Create(&models.Account{UserId: user.Id, Username: job.Admin}).Error; err != nil {
				return err
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.RoleUser{RoleID: uint64(owner.ID), UserID: user.Id}).Error //nolint:gosec // positive id
		})
		if err == nil {
			r.t.logf("info", "administrator @%s created", job.Admin)
		}
	}
	if err != nil {
		r.t.logf("error", "administrator setup failed: %s", sanitize(err.Error()))
		return fail("admin_failed")
	}
	if job.Locale != "" {
		if _, err := models.UpdateUserSettingData(conn, user.Id, map[string]any{"interface_locale": job.Locale}); err != nil {
			r.t.logf("warn", "could not save the administrator's interface language")
		}
	}
	r.t.setCount("admin", "accounts", 1)
	return nil
}

func stageFinalize(_ context.Context, r *runner) error {
	if err := importRuntimeServiceSettings(r); err != nil {
		return err
	}
	job := r.t.snapshot()
	if job.Locale != "" {
		if err := systemsettings.SaveChanges(map[string]any{systemsettings.KeyDefaultLocale: job.Locale}); err != nil {
			r.t.logf("warn", "could not save the default language")
		}
	}
	if err := markDatabaseInstalled(); err != nil {
		return fail("config_write_failed", "cannot write the installed marker to the database")
	}
	if err := MarkInstalled(); err != nil {
		return fail("config_write_failed", "cannot write the installed marker")
	}
	clearSetupFiles()
	r.t.logf("info", "installation complete; restarting into normal mode")
	return nil
}
