package database_test

import (
	"os"
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// DefaultDBConfig
// ---------------------------------------------------------------------------

func TestDefaultDBConfig(t *testing.T) {
	cfg := database.DefaultDBConfig()

	assert.Equal(t, 100, cfg.MaxOpenConns, "MaxOpenConns should be 100")
	assert.Equal(t, 10, cfg.MaxIdleConns, "MaxIdleConns should be 10")
	assert.Equal(t, 30*time.Minute, cfg.ConnMaxLifetime, "ConnMaxLifetime should be 30m")
	assert.Equal(t, 10*time.Minute, cfg.ConnMaxIdleTime, "ConnMaxIdleTime should be 10m")
}

// ---------------------------------------------------------------------------
// LoadDBConfig
// ---------------------------------------------------------------------------

func TestLoadDBConfig_NoEnvVars(t *testing.T) {
	// Ensure all related env vars are cleared
	unsetEnv(t,
		"DB_MAX_OPEN_CONNS",
		"DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME_MINUTES",
		"DB_CONN_MAX_IDLE_TIME_MINUTES",
	)

	cfg := database.LoadDBConfig()
	assert.Equal(t, database.DefaultDBConfig(), cfg, "Should return defaults when no env vars set")
}

func TestLoadDBConfig_MaxOpenConns(t *testing.T) {
	unsetEnv(t,
		"DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME_MINUTES",
		"DB_CONN_MAX_IDLE_TIME_MINUTES",
	)
	t.Setenv("DB_MAX_OPEN_CONNS", "50")

	cfg := database.LoadDBConfig()
	assert.Equal(t, 50, cfg.MaxOpenConns)
	assert.Equal(t, 10, cfg.MaxIdleConns, "other fields should keep defaults")
}

func TestLoadDBConfig_MaxIdleConns(t *testing.T) {
	unsetEnv(t,
		"DB_MAX_OPEN_CONNS",
		"DB_CONN_MAX_LIFETIME_MINUTES",
		"DB_CONN_MAX_IDLE_TIME_MINUTES",
	)
	t.Setenv("DB_MAX_IDLE_CONNS", "5")

	cfg := database.LoadDBConfig()
	assert.Equal(t, 5, cfg.MaxIdleConns)
	assert.Equal(t, 100, cfg.MaxOpenConns, "other fields should keep defaults")
}

func TestLoadDBConfig_ConnMaxLifetime(t *testing.T) {
	unsetEnv(t,
		"DB_MAX_OPEN_CONNS",
		"DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_IDLE_TIME_MINUTES",
	)
	t.Setenv("DB_CONN_MAX_LIFETIME_MINUTES", "60")

	cfg := database.LoadDBConfig()
	assert.Equal(t, 60*time.Minute, cfg.ConnMaxLifetime)
	assert.Equal(t, 10*time.Minute, cfg.ConnMaxIdleTime, "other fields should keep defaults")
}

func TestLoadDBConfig_ConnMaxIdleTime(t *testing.T) {
	unsetEnv(t,
		"DB_MAX_OPEN_CONNS",
		"DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME_MINUTES",
	)
	t.Setenv("DB_CONN_MAX_IDLE_TIME_MINUTES", "5")

	cfg := database.LoadDBConfig()
	assert.Equal(t, 5*time.Minute, cfg.ConnMaxIdleTime)
	assert.Equal(t, 30*time.Minute, cfg.ConnMaxLifetime, "other fields should keep defaults")
}

func TestLoadDBConfig_AllEnvVarsSet(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "200")
	t.Setenv("DB_MAX_IDLE_CONNS", "20")
	t.Setenv("DB_CONN_MAX_LIFETIME_MINUTES", "120")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_MINUTES", "15")

	cfg := database.LoadDBConfig()
	assert.Equal(t, 200, cfg.MaxOpenConns)
	assert.Equal(t, 20, cfg.MaxIdleConns)
	assert.Equal(t, 120*time.Minute, cfg.ConnMaxLifetime)
	assert.Equal(t, 15*time.Minute, cfg.ConnMaxIdleTime)
}

func TestLoadDBConfig_InvalidValuesIgnored(t *testing.T) {
	defaults := database.DefaultDBConfig()

	tests := []struct {
		name string
		env  string
		val  string
	}{
		{name: "non-numeric string", env: "DB_MAX_OPEN_CONNS", val: "abc"},
		{name: "negative number", env: "DB_MAX_IDLE_CONNS", val: "-1"},
		{name: "zero", env: "DB_CONN_MAX_LIFETIME_MINUTES", val: "0"}, //nolint:goconst
		{name: "non-numeric lifetime", env: "DB_CONN_MAX_LIFETIME_MINUTES", val: "abc"},
		{name: "negative idle time", env: "DB_CONN_MAX_IDLE_TIME_MINUTES", val: "-5"}, //nolint:goconst
		{name: "zero idle time", env: "DB_CONN_MAX_IDLE_TIME_MINUTES", val: "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(t,
				"DB_MAX_OPEN_CONNS",
				"DB_MAX_IDLE_CONNS",
				"DB_CONN_MAX_LIFETIME_MINUTES",
				"DB_CONN_MAX_IDLE_TIME_MINUTES",
			)
			t.Setenv(tc.env, tc.val)

			cfg := database.LoadDBConfig()
			// The specific field that was given an invalid value should keep its default
			switch tc.env {
			case "DB_MAX_OPEN_CONNS":
				assert.Equal(t, defaults.MaxOpenConns, cfg.MaxOpenConns)
			case "DB_MAX_IDLE_CONNS":
				assert.Equal(t, defaults.MaxIdleConns, cfg.MaxIdleConns)
			case "DB_CONN_MAX_LIFETIME_MINUTES":
				assert.Equal(t, defaults.ConnMaxLifetime, cfg.ConnMaxLifetime)
			case "DB_CONN_MAX_IDLE_TIME_MINUTES":
				assert.Equal(t, defaults.ConnMaxIdleTime, cfg.ConnMaxIdleTime)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectTestMode / IsTestMode
// ---------------------------------------------------------------------------

func TestIsTestMode_InTestContext(t *testing.T) {
	// When running under `go test`, os.Args contains `-test.*` flags (e.g. -test.v, -test.run).
	// The init() function calls detectTestMode() which should detect this automatically.
	// We can't easily re-detect because init() already ran, but we verify the result.
	assert.True(t, database.IsTestMode(),
		"IsTestMode should return true when running under go test (os.Args has -test.* flags)")
}

func TestIsTestMode_GoTestModeEnvVar(t *testing.T) {
	// The init() in the database package already ran with the real os.Args / env.
	// We can only verify the logic indirectly: since we are in a test context,
	// IsTestMode should be true regardless of env var.
	assert.True(t, database.IsTestMode(),
		"IsTestMode should return true in test context")
}

// ---------------------------------------------------------------------------
// SetTestMode / IsTestMode
// ---------------------------------------------------------------------------

func TestSetTestMode(t *testing.T) {
	// Reset to known state first
	database.ResetTestDB()
	assert.False(t, database.IsTestMode())

	database.SetTestMode()
	assert.True(t, database.IsTestMode())

	// Clean up: restore false for other tests
	database.ResetTestDB()
}

// ---------------------------------------------------------------------------
// SetTestDB / ResetTestDB / IsTestMode
// ---------------------------------------------------------------------------

func TestSetTestDB_SetsDBAndTestMode(t *testing.T) {
	database.ResetTestDB()

	// SetTestDB with nil DB is still valid – it sets both DB and isTestMode
	database.SetTestDB(nil)
	assert.True(t, database.IsTestMode(), "SetTestDB should enable test mode")

	// Reset
	database.ResetTestDB()
	assert.False(t, database.IsTestMode(), "ResetTestDB should disable test mode")
}

func TestResetTestDB_ClearsState(t *testing.T) {
	database.SetTestMode()
	database.ResetTestDB()

	assert.False(t, database.IsTestMode(), "isTestMode should be false after reset")
	// DB should be nil – we cannot directly access the unexported DB var,
	// but we can verify via GetDBStats which returns os.ErrInvalid when DB is nil
	_, err := database.GetDBStats()
	assert.Equal(t, os.ErrInvalid, err, "DB should be nil after reset")
}

// ---------------------------------------------------------------------------
// GetDBStats
// ---------------------------------------------------------------------------

func TestGetDBStats_NilDB(t *testing.T) {
	database.ResetTestDB()

	stats, err := database.GetDBStats()
	assert.Nil(t, stats)
	assert.Equal(t, os.ErrInvalid, err)
}

// ---------------------------------------------------------------------------
// CloseDB
// ---------------------------------------------------------------------------

func TestCloseDB_NilDB(t *testing.T) {
	database.ResetTestDB()

	err := database.CloseDB()
	assert.NoError(t, err, "CloseDB with nil DB should return nil")
}

// ---------------------------------------------------------------------------
// PingDB
// ---------------------------------------------------------------------------

func TestPingDB_NilDB(t *testing.T) {
	database.ResetTestDB()

	err := database.PingDB()
	assert.Equal(t, os.ErrInvalid, err)
}

// ---------------------------------------------------------------------------
// IsXmlMode
// ---------------------------------------------------------------------------

func TestIsXmlMode_True(t *testing.T) {
	original := config.C.DbConnection
	defer func() { config.C.DbConnection = original }()

	config.C.DbConnection = "xml"

	assert.True(t, database.IsXmlMode())
}

func TestIsXmlMode_False_MySQL(t *testing.T) {
	original := config.C.DbConnection
	defer func() { config.C.DbConnection = original }()

	config.C.DbConnection = "mysql"

	assert.False(t, database.IsXmlMode())
}

func TestIsXmlMode_False_SQLite(t *testing.T) {
	original := config.C.DbConnection
	defer func() { config.C.DbConnection = original }()

	config.C.DbConnection = "sqlite"

	assert.False(t, database.IsXmlMode())
}

func TestIsXmlMode_False_Empty(t *testing.T) {
	original := config.C.DbConnection
	defer func() { config.C.DbConnection = original }()

	config.C.DbConnection = ""

	assert.False(t, database.IsXmlMode())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// unsetEnv removes several environment variables and registers t.Cleanup to
// restore their original values.
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, k := range keys {
		t.Setenv(k, "")
	}
}
