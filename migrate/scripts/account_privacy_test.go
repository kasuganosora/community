package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountPrivacyScriptKeepsHiddenCollectionsPrivate(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec("CREATE TABLE accounts (id bigint primary key, username varchar(64), hide_collections boolean default false, deleted_at timestamp)").Error)
	require.NoError(t, conn.Exec("INSERT INTO accounts (id, username, hide_collections) VALUES (1, 'a', true), (2, 'b', false)").Error)
	require.NoError(t, AccountPrivacyScript{}.Up(conn))
	require.NoError(t, AccountPrivacyScript{}.Up(conn), "idempotent")
	var rows []models.Account
	require.NoError(t, conn.Order("id").Find(&rows).Error)
	require.Equal(t, models.CollectionPrivate, rows[0].FollowingVisibility)
	require.Equal(t, models.CollectionPrivate, rows[0].FollowersVisibility)
	require.Equal(t, models.CollectionPublic, rows[1].FollowersVisibility)
	require.True(t, rows[1].SpeakAsCat)
	require.False(t, rows[1].AutoAcceptFollowed)
	require.Nil(t, rows[1].LastActiveAt)
}
