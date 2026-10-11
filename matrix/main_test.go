package matrix

import (
	"os"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
)

// TestMain sets up the test environment so the matrix package's db-dependent
// code does not panic on startup. We set test mode (which prevents the db
// package from trying to open a real MySQL/SQLite connection in init()) and
// then run all tests.
func TestMain(m *testing.M) {
	database.SetTestMode()
	// Local httptest servers must not inherit the developer deployment proxy.
	config.C.HttpProxy = ""
	os.Exit(m.Run())
}

// setupTestGlobalConfig overrides the package-level GlobalConfig directly.
// Because LoadConfig uses sync.Once and consults systemsettings (which needs
// a DB), tests that need GlobalConfig to be a known value must set it here.
func setupTestGlobalConfig(homeserverURL string) {
	GlobalConfig = Config{
		Enabled:             true,
		HomeserverURL:       homeserverURL,
		ServerName:          "example.com",
		ASToken:             "test-as-token",
		HSToken:             "test-hs-token",
		BridgeUsername:      "_bridge",
		UserNamespacePrefix: "_c_",
		RoomNamespacePrefix: "_c_",
	}
}
