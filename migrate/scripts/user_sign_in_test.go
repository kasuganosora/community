package scripts

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserSignInScriptBackfillsLatestWebSessionWithoutInventingIP(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE users (
		id integer PRIMARY KEY,
		uuid varchar(36) NOT NULL DEFAULT '',
		username varchar(64) NOT NULL,
		is_ban numeric NOT NULL DEFAULT 0,
		is_admin numeric NOT NULL DEFAULT 0,
		email_hash varchar(255) NOT NULL,
		created_at datetime,
		updated_at datetime,
		invite_by integer NOT NULL DEFAULT 0,
		status integer NOT NULL DEFAULT 0,
		properties text,
		is_mail_verified numeric NOT NULL DEFAULT 0,
		deleted_at datetime
	)`).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE auth_tokens (
		token varchar(128) PRIMARY KEY,
		type integer NOT NULL,
		user_id integer NOT NULL,
		created_at datetime
	)`).Error)
	older := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	latest := time.Date(2026, 10, 1, 13, 0, 45, 0, time.UTC)
	require.NoError(t, conn.Exec(`INSERT INTO users (id, username, email_hash) VALUES (7, 'ler', 'hash')`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO auth_tokens (token, type, user_id, created_at) VALUES (?, 0, 7, ?), (?, 0, 7, ?), (?, 1, 7, ?)`,
		"old", older, "new", latest, "oauth", latest.Add(24*time.Hour)).Error)

	require.NoError(t, UserSignInScript{}.Up(conn))
	require.NoError(t, UserSignInScript{}.Up(conn), "idempotent")

	var user models.User
	require.NoError(t, conn.First(&user, 7).Error)
	require.NotNil(t, user.CurrentSignInAt)
	require.NotNil(t, user.LastSignInAt)
	require.True(t, user.CurrentSignInAt.Equal(latest), user.CurrentSignInAt.String())
	require.True(t, user.LastSignInAt.Equal(latest))
	require.Empty(t, user.CurrentSignInIP)
	require.Empty(t, user.LastSignInIP)
	require.Equal(t, 0, user.SignInCount)

	kept := latest.Add(time.Hour)
	require.NoError(t, conn.Model(&user).UpdateColumns(map[string]any{
		"current_sign_in_at": kept,
		"current_sign_in_ip": "203.0.113.10",
	}).Error)
	require.NoError(t, UserSignInScript{}.Up(conn))
	require.NoError(t, conn.First(&user, 7).Error)
	require.True(t, user.CurrentSignInAt.Equal(kept))
	require.Equal(t, "203.0.113.10", user.CurrentSignInIP)
}
