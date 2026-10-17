package setup

import (
	"os"
	"strings"
)

// builtinDatabase is available only when our Compose file explicitly reports
// one selected database profile. These setup-only variables must not trigger
// HasDatabaseConfig or override the configuration written by the wizard.
func builtinDatabase() (DatabaseParams, bool) {
	engine := ""
	for _, profile := range strings.Split(os.Getenv("COMMUNITY_COMPOSE_PROFILES"), ",") {
		profile = strings.TrimSpace(profile)
		if profile != "postgres" && profile != "mysql" {
			continue
		}
		if engine != "" && engine != profile {
			return DatabaseParams{}, false
		}
		engine = profile
	}
	p := DatabaseParams{
		Type: engine, Host: engine,
		Name:     os.Getenv("COMMUNITY_SETUP_DB_NAME"),
		User:     os.Getenv("COMMUNITY_SETUP_DB_USER"),
		Password: os.Getenv("COMMUNITY_SETUP_DB_PASSWORD"), SSLMode: "disable",
	}
	if engine == "" || p.Password == "" || p.normalize() != nil {
		return DatabaseParams{}, false
	}
	return p, true
}

func (p *DatabaseParams) resolveTarget() *Error {
	if strings.EqualFold(strings.TrimSpace(p.Type), "builtin") {
		resolved, available := builtinDatabase()
		if !available {
			return fail("builtin_database_unavailable")
		}
		// Never accept client-supplied overrides for the bundled connection.
		*p = resolved
	}
	return p.normalize()
}
