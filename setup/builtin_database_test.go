package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func configureBuiltin(t *testing.T, profiles string) {
	t.Helper()
	t.Setenv("COMMUNITY_COMPOSE_PROFILES", profiles)
	t.Setenv("COMMUNITY_SETUP_DB_NAME", "bundled")
	t.Setenv("COMMUNITY_SETUP_DB_USER", "installer")
	t.Setenv("COMMUNITY_SETUP_DB_PASSWORD", "server-only-secret")
}

func TestBuiltinDatabaseAvailability(t *testing.T) {
	withConfigDir(t)
	configureBuiltin(t, "")
	t.Setenv("COMMUNITY_DEFAULT_DB_HOST", "db")
	t.Setenv("COMPOSE_PROFILES", "postgres")
	for _, profiles := range []string{"", "search", "postgres,mysql"} {
		t.Setenv("COMMUNITY_COMPOSE_PROFILES", profiles)
		_, available := builtinDatabase()
		require.False(t, available, profiles)
		p := DatabaseParams{Type: "builtin"}
		require.Equal(t, "builtin_database_unavailable", p.resolveTarget().Code)
	}
	for _, engine := range []string{"postgres", "mysql"} {
		configureBuiltin(t, "search, "+engine)
		p := DatabaseParams{Type: "builtin", Host: "attacker.example", Port: 42, Password: "client-secret"}
		require.Nil(t, p.resolveTarget())
		require.Equal(t, engine, p.Type)
		require.Equal(t, engine, p.Host)
		require.Equal(t, "server-only-secret", p.Password)
		require.Equal(t, "bundled", p.Name)
		require.Equal(t, "installer", p.User)
		if engine == "postgres" {
			require.Equal(t, 5432, p.Port)
		} else {
			require.Equal(t, 3306, p.Port)
		}
		values := envValues("https://example.org", p, RedisParams{Address: "redis:6379"})
		require.Equal(t, engine, values["DB_CONNECTION"])
		require.Equal(t, p.Password, values["DB_PASSWORD"])
		config.Reload()
		require.False(t, config.HasDatabaseConfig(), "setup-only credentials must not bypass the wizard")
	}
	t.Setenv("COMMUNITY_SETUP_DB_PASSWORD", "")
	_, available := builtinDatabase()
	require.False(t, available)
}

func TestBuiltinStatusDoesNotExposeCredentials(t *testing.T) {
	withConfigDir(t)
	configureBuiltin(t, "postgres")
	s := NewServer("")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/setup/status", nil))
	require.Equal(t, 200, w.Code)
	var status map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	require.Equal(t, map[string]any{"type": "postgres"}, status["builtin_database"])
	for _, private := range []string{"server-only-secret", "installer", "bundled"} {
		require.NotContains(t, w.Body.String(), private)
	}
	configureBuiltin(t, "")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/setup/status", nil))
	require.NotContains(t, w.Body.String(), "builtin_database")
}

func TestUnavailableBuiltinRejectedBeforeConnectingOrInstalling(t *testing.T) {
	withConfigDir(t)
	configureBuiltin(t, "")
	s := NewServer("")
	r := httptest.NewRequest(http.MethodPost, "/api/setup/test/database", strings.NewReader(`{"type":"builtin"}`))
	r.Header.Set("X-Setup-Token", s.Token())
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "builtin_database_unavailable")
	_, _, err := prepareInstall(context.Background(), &InstallRequest{Mode: "fresh", Database: DatabaseParams{Type: "builtin"}})
	require.Equal(t, "builtin_database_unavailable", err.(*Error).Code)
}
