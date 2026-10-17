package dialect

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type changedRow struct {
	ID     uint64 `gorm:"primaryKey"`
	Name   string
	Avatar *string
	Hits   int
}

func (changedRow) TableName() string { return "dialect_changed_rows" }

func checkUpdateChanged(t *testing.T, conn *gorm.DB) {
	t.Helper()
	require.NoError(t, conn.Migrator().DropTable(&changedRow{}))
	require.NoError(t, conn.AutoMigrate(&changedRow{}))
	require.NoError(t, conn.Create(&changedRow{ID: 1, Name: "a"}).Error)
	upd := func(values map[string]any) int64 {
		res := UpdateChanged(conn.Model(&changedRow{}).Where("id = ?", 1), values, map[string]any{"hits": gorm.Expr("hits + 1")})
		require.NoError(t, res.Error)
		return res.RowsAffected
	}
	require.EqualValues(t, 0, upd(map[string]any{"name": "a", "avatar": nil}), "same values, NULL = NULL: no write")
	require.EqualValues(t, 1, upd(map[string]any{"name": "a", "avatar": "x"}), "NULL -> value is a change")
	require.EqualValues(t, 0, upd(map[string]any{"name": "a", "avatar": "x"}))
	require.EqualValues(t, 1, upd(map[string]any{"name": "b", "avatar": "x"}))
	require.EqualValues(t, 1, upd(map[string]any{"name": "b", "avatar": nil}), "value -> NULL is a change")
	var row changedRow
	require.NoError(t, conn.First(&row, 1).Error)
	require.Equal(t, 3, row.Hits, "extra columns are written only with a real change")
	require.Equal(t, "b", row.Name)
	require.Nil(t, row.Avatar)
}

func TestUpdateChangedSQLite(t *testing.T) {
	conn, err := gorm.Open(OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	checkUpdateChanged(t, conn)
}

func TestUpdateChangedPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	conn, err := gorm.Open(OpenPostgres(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Migrator().DropTable(&changedRow{}) })
	checkUpdateChanged(t, conn)
}
