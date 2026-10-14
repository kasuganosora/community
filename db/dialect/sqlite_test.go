package dialect

import (
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestSQLiteSharedBigintIdentity(t *testing.T) {
	conn, err := gorm.Open(OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	type Identity struct {
		ID   uint64 `gorm:"type:bigint(20);primaryKey;autoIncrement"`
		Name string
	}
	require.NoError(t, conn.AutoMigrate(&Identity{}))
	first, second := Identity{Name: "one"}, Identity{Name: "two"}
	require.NoError(t, conn.Create(&first).Error)
	require.NoError(t, conn.Create(&second).Error)
	require.NotZero(t, first.ID)
	require.Greater(t, second.ID, first.ID)
	require.NoError(t, conn.AutoMigrate(&Identity{}))
	var loaded Identity
	require.NoError(t, conn.First(&loaded, second.ID).Error)
	require.Equal(t, "two", loaded.Name)
}
