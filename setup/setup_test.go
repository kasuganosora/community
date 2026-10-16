package setup

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func withConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range databaseKeysForTest {
		t.Setenv(k, "")
	}
	t.Setenv("ENV_FILE", filepath.Join(dir, ".env"))
	config.Reload()
	t.Cleanup(config.Reload)
	return dir
}

var databaseKeysForTest = []string{"DB_CONNECTION", "DB_DSN", "DB_HOST", "DB_DATABASE", "DB_USERNAME", "DB_PASSWORD"}

func TestDecide(t *testing.T) {
	dir := withConfigDir(t)
	mode, _ := Decide()
	require.Equal(t, ModeSetup, mode, "no database and no marker")

	require.NoError(t, MarkInstalled())
	mode, reason := Decide()
	require.Equal(t, ModeMaintenance, mode, "installed but configuration missing must never offer setup")
	require.Equal(t, "configuration_missing", reason)

	t.Setenv("DB_CONNECTION", "postgres")
	config.Reload()
	mode, _ = Decide()
	require.Equal(t, ModeNormal, mode)

	require.NoError(t, os.Remove(filepath.Join(dir, markerName)))
	mode, _ = Decide()
	require.Equal(t, ModeNormal, mode, "an existing .env deployment is treated as installed")

	require.NoError(t, markPending())
	mode, _ = Decide()
	require.Equal(t, ModeSetup, mode, "an unfinished wizard run resumes")
	require.NoError(t, MarkInstalled())
	mode, _ = Decide()
	require.Equal(t, ModeNormal, mode)
}

func TestWriteEnvFileAtomicPrivateAndMerged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte("KEEP_ME=1\nDB_PASSWORD=old\n"), 0o644))
	password := `p"a$s w'o#r\d=`
	require.NoError(t, WriteEnvFile(path, map[string]string{"DB_PASSWORD": password, "APP_BASE_URL": "https://example.org"}))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	values, err := godotenv.Read(path)
	require.NoError(t, err)
	require.Equal(t, password, values["DB_PASSWORD"])
	require.Equal(t, "1", values["KEEP_ME"])
	entries, _ := os.ReadDir(dir)
	require.Len(t, entries, 1, "no temporary files are left behind")
}

func TestGeneratedSecretsKeepExisting(t *testing.T) {
	s := generatedSecrets(map[string]string{"ENCRYPTED_KEY": "existing-key-0123456789"})
	require.NotContains(t, s, "ENCRYPTED_KEY")
	require.GreaterOrEqual(t, len(s["MEDIA_PROXY_KEY"]), 32)
}

func TestNormalizeBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"example.org": "https://example.org", "https://Example.org/": "https://example.org",
		"http://localhost:8080": "http://localhost:8080",
	} {
		got, err := normalizeBaseURL(in)
		require.Nil(t, err, in)
		require.Equal(t, want, got)
	}
	for _, in := range []string{"", "http://example.org", "https://example.org/path", "https://user@example.org", "intranet", "ftp://example.org"} {
		_, err := normalizeBaseURL(in)
		require.NotNil(t, err, in)
	}
}

func TestGuessIDMethod(t *testing.T) {
	require.Equal(t, "aidx", guessIDMethod([]string{"9xk2a1b2c3d4e5f6"}))
	require.Equal(t, "aid", guessIDMethod([]string{"9xk2a1b2c3"}))
	require.Equal(t, "meidg", guessIDMethod([]string{"g18c1ef4e5a0123456789abc"}))
	require.Equal(t, "objectid", guessIDMethod([]string{"65a1b2c3d4e5f60718293a4b"}))
	require.Equal(t, "ulid", guessIDMethod([]string{"01HQ2X3Y4Z5A6B7C8D9E0F1G2H"}))
	require.Equal(t, "", guessIDMethod(nil))
}

func TestRefusesDatabaseWithAppTables(t *testing.T) {
	p := DatabaseParams{Type: "sqlite", Path: filepath.Join(t.TempDir(), "x.db")}
	require.Nil(t, p.normalize())
	conn, err := openDatabase(p)
	require.NoError(t, err)
	defer closeDB(conn)
	require.False(t, databaseHasCommunityData(conn))
	require.NoError(t, conn.Exec("CREATE TABLE system_settings (id integer)").Error)
	require.True(t, databaseHasCommunityData(conn))
}

func TestTokenRequiredAndLockout(t *testing.T) {
	withConfigDir(t)
	s := NewServer("")
	h := s.Handler()
	do := func(token string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/setup/verify", nil)
		if token != "" {
			r.Header.Set("X-Setup-Token", token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	require.Equal(t, 401, do(""))
	require.Equal(t, 200, do(s.Token()))
	for range 10 {
		require.Equal(t, 401, do("wrong"))
	}
	require.Equal(t, 429, do(s.Token()), "locked after repeated failures")
}

func TestSetupRedirectsAndMaintenance(t *testing.T) {
	withConfigDir(t)
	h := NewServer("").Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, 302, w.Code)
	require.Equal(t, "/setup", w.Header().Get("Location"))

	m := MaintenanceHandler("database_unavailable")
	for _, p := range []string{"/setup", "/api/setup/status", "/"} {
		w = httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		require.Equal(t, 503, w.Code, p)
		require.False(t, strings.Contains(w.Body.String(), "token"), p)
	}
}

func TestSanitizeDropsSecrets(t *testing.T) {
	out := sanitize("dial postgres://u:secretpw@db/x failed password=hunter2")
	require.NotContains(t, out, "secretpw")
	require.NotContains(t, out, "hunter2")
}
