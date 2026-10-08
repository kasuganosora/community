package database

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/assert"
)

func TestPostgresDSN(t *testing.T) {
	saved := config.C
	savedLocal := time.Local
	t.Cleanup(func() { config.C = saved; time.Local = savedLocal })
	time.Local = time.UTC

	config.C.DbHost = "db.internal"
	config.C.DbPort = "3306" // MySQL default from config; ignored unless DB_PORT is set
	config.C.DbUsername = "community"
	config.C.DbPassword = "p@ss word'"
	config.C.DbDatabase = "community"
	config.C.DbSSLMode = ""
	config.C.DbDSN = ""
	t.Setenv("DB_PORT", "")

	assert.Equal(t,
		`host=db.internal port=5432 user=community password='p@ss word\'' dbname=community sslmode=disable TimeZone=UTC`,
		postgresDSN())

	t.Setenv("DB_PORT", "6543")
	config.C.DbPort = "6543"
	config.C.DbSSLMode = "require"
	assert.Equal(t,
		`host=db.internal port=6543 user=community password='p@ss word\'' dbname=community sslmode=require TimeZone=UTC`,
		postgresDSN())

	config.C.DbDSN = " postgres://u:p@h:5/db?sslmode=verify-full "
	assert.Equal(t, "postgres://u:p@h:5/db?sslmode=verify-full", postgresDSN())
}

func TestQuoteDSNValue(t *testing.T) {
	assert.Equal(t, "plain", quoteDSNValue("plain"))
	assert.Equal(t, "''", quoteDSNValue(""))
	assert.Equal(t, `'a b'`, quoteDSNValue("a b"))
	assert.Equal(t, `'a\\b\'c'`, quoteDSNValue(`a\b'c`))
}
