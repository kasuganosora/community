package homeserver

import (
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExternalMatrixActorIsStableAndCannotImpersonateLoginAccount(t *testing.T) {
	hs, conn := externalActorDB(t)
	require.NoError(t, conn.AutoMigrate(&MatrixExternalActor{}, &models.AccountExtra{}))
	first, err := hs.EnsureExternalMatrixActor(conn, "@alice:elsewhere.example")
	require.NoError(t, err)
	second, err := hs.EnsureExternalMatrixActor(conn, "@alice:elsewhere.example")
	require.NoError(t, err)
	require.Equal(t, first.Id, second.Id)
	require.Nil(t, first.Domain)
	require.Zero(t, first.UserId)
	require.True(t, first.Bot)
	require.True(t, first.NoIndex)
	require.Error(t, models.ValidateUsername(first.Username))
	require.Contains(t, first.Note, "@alice:elsewhere.example")
	other, err := hs.EnsureExternalMatrixActor(conn, "@alice:another.example")
	require.NoError(t, err)
	require.NotEqual(t, first.Id, other.Id)
	for _, invalid := range []string{"@alice:test.local", "alice:elsewhere.example", "@a:host/path", "@a:host?query", "@a:host#fragment", "@a:bad host", "@a:" + strings.Repeat("x", 256)} {
		_, err = hs.EnsureExternalMatrixActor(conn, invalid)
		require.Error(t, err, invalid)
	}
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", first.Id).Update("is_ban", true).Error)
	_, err = hs.EnsureExternalMatrixActor(conn, "@alice:elsewhere.example")
	require.Error(t, err)
}

func TestExternalMatrixActorCreationRollsBackWithEventTransaction(t *testing.T) {
	hs, conn := externalActorDB(t)
	require.NoError(t, conn.AutoMigrate(&MatrixExternalActor{}, &models.AccountExtra{}))
	err := conn.Transaction(func(tx *gorm.DB) error {
		_, err := hs.EnsureExternalMatrixActor(tx, "@pending:remote.example")
		require.NoError(t, err)
		return gorm.ErrInvalidTransaction
	})
	require.ErrorIs(t, err, gorm.ErrInvalidTransaction)
	var count int64
	require.NoError(t, conn.Model(&MatrixExternalActor{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Model(&models.Account{}).Where("username LIKE ?", "_mx_%").Count(&count).Error)
	require.Zero(t, count)
}

// The shared SQLite fixture preserves the production MySQL bigint type,
// which is not SQLite's special INTEGER rowid alias. Supply fixture IDs
// without changing production creation hooks or identity mapping behavior.
func externalActorDB(t *testing.T) (*HomeServer, *gorm.DB) {
	hs, conn := directBridgeDB(t)
	nextID := uint64(100)
	require.NoError(t, conn.Callback().Create().Before("gorm:before_create").Register("test:external_actor_id", func(tx *gorm.DB) {
		if account, ok := tx.Statement.Dest.(*models.Account); ok && account.Id == 0 {
			nextID++
			account.Id = nextID
		}
	}))
	return hs, conn
}
