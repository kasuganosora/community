package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTwoFactorSecurityPinScriptAddsHashedColumn(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE user_two_factors (
		user_id bigint PRIMARY KEY,
		salt varchar(64) NOT NULL DEFAULT '',
		secret text,
		pending_secret text,
		pending_created_at timestamp,
		enabled_at timestamp,
		last_used_step bigint NOT NULL DEFAULT 0,
		failed_attempts bigint NOT NULL DEFAULT 0,
		locked_until timestamp,
		created_at timestamp,
		updated_at timestamp
	)`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO user_two_factors (user_id, salt, secret) VALUES (7, 's', 'enc')`).Error)

	require.NoError(t, TwoFactorSecurityPinScript{}.Up(conn))
	require.NoError(t, TwoFactorSecurityPinScript{}.Up(conn), "idempotent")
	require.True(t, conn.Migrator().HasColumn("user_two_factors", "pin_hash"))

	var hash string
	require.NoError(t, conn.Raw(`SELECT pin_hash FROM user_two_factors WHERE user_id = 7`).Scan(&hash).Error)
	require.Empty(t, hash, "existing TOTP rows gain an empty hash, not a reset")

	var secret string
	require.NoError(t, conn.Raw(`SELECT secret FROM user_two_factors WHERE user_id = 7`).Scan(&secret).Error)
	require.Equal(t, "enc", secret)

	require.NoError(t, TwoFactorSecurityPinScript{}.Down(conn))
	require.False(t, conn.Migrator().HasColumn("user_two_factors", "pin_hash"))
	require.NoError(t, TwoFactorSecurityPinScript{}.Down(conn), "down is idempotent")
}
