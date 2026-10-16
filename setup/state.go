// Package setup implements the first-run installation wizard (/setup) and the
// boot decision between normal operation, setup mode and maintenance.
//
// Setup mode is entered only when no database is configured (.env and the
// environment set none of the DB_* variables) AND the installed marker does
// not exist. An installed instance whose configuration or database is
// missing shows a maintenance page instead, so nobody can re-initialize (and
// take over) a deployed site by making its database unreachable.
package setup

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// Mode is what the server process does after loading its configuration.
type Mode int

const (
	// ModeNormal runs the community server.
	ModeNormal Mode = iota
	// ModeSetup serves only the installation wizard.
	ModeSetup
	// ModeMaintenance serves only a maintenance page.
	ModeMaintenance
)

func (m Mode) String() string {
	switch m {
	case ModeSetup:
		return "setup"
	case ModeMaintenance:
		return "maintenance"
	default:
		return "normal"
	}
}

const (
	markerName  = ".installed"
	pendingName = ".setup-pending"
	stateName   = "setup-state.json"
	secretsName = "setup-secrets.json"
)

// ConfigDir is the directory holding .env, the installed marker and the
// setup job state. It is the persistent volume in Docker (/app/config).
func ConfigDir() string { return filepath.Dir(config.EnvFile()) }

func configPath(name string) string { return filepath.Join(ConfigDir(), name) }

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsInstalled reports whether the installed marker exists.
func IsInstalled() bool { return exists(configPath(markerName)) }

// setupPending reports whether a setup run wrote .env but has not finished
// (for example a Misskey migration that is still running or failed).
func setupPending() bool { return exists(configPath(pendingName)) }

// Decide returns the boot mode and, for maintenance, a short reason code.
func Decide() (Mode, string) {
	if config.LoadError() != nil {
		return ModeMaintenance, "configuration_unreadable"
	}
	installed := IsInstalled()
	if !config.HasDatabaseConfig() {
		if installed {
			return ModeMaintenance, "configuration_missing"
		}
		return ModeSetup, ""
	}
	if setupPending() && !installed {
		// A pending setup resumes only while the database has not been
		// marked installed (the marker row is written by the final stage).
		if conn := db.GetDB(); conn != nil && databaseInstalled(conn) {
			return ModeNormal, ""
		}
		return ModeSetup, ""
	}
	return ModeNormal, ""
}

// MarkInstalled writes the installed marker. After it exists the server
// never enters setup mode again and every /setup endpoint returns 404.
func MarkInstalled() error {
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(configPath(markerName), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// EnsureInstalledMarker is called by a normally booting server after its
// migrations: an existing deployment configured through .env (installed
// before the wizard existed) gets the marker on its first boot, so it can
// never fall into setup mode later. Failing to write it is only a warning;
// such an instance still has its database configuration and boots normally.
func EnsureInstalledMarker() (bool, error) {
	if conn := db.GetDB(); conn != nil && !databaseInstalled(conn) {
		if err := markDatabaseInstalled(); err != nil {
			return false, err
		}
	}
	if IsInstalled() {
		_ = os.Remove(configPath(pendingName))
		return false, nil
	}
	if err := MarkInstalled(); err != nil {
		return false, err
	}
	_ = os.Remove(configPath(pendingName))
	return true, nil
}

func markPending() error {
	return writeFileAtomic(configPath(pendingName), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

func clearSetupFiles() {
	for _, name := range []string{pendingName, secretsName} {
		if err := os.Remove(configPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
	}
}

// databaseInstalled reports whether the database carries the installed
// marker row in system_settings.
func databaseInstalled(conn *gorm.DB) bool {
	if !conn.Migrator().HasTable("system_settings") {
		return false
	}
	var n int64
	if conn.Table("system_settings").Where(dialect.Quote(conn, "key")+" = ?", systemsettings.KeyInstalledAt).Count(&n).Error != nil {
		return false
	}
	return n > 0
}

func markDatabaseInstalled() error {
	return systemsettings.SaveChanges(map[string]any{systemsettings.KeyInstalledAt: time.Now().UTC().Format(time.RFC3339)})
}
