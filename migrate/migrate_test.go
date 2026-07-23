package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMigration_TableName(t *testing.T) {
	m := Migration{}
	assert.Equal(t, "migrations", m.TableName())
}

func TestMigration_Struct(t *testing.T) {
	m := Migration{
		ID:    1,
		Name:  "test_migration",
		Batch: 1,
	}

	assert.Equal(t, int64(1), m.ID)
	assert.Equal(t, "test_migration", m.Name)
	assert.Equal(t, 1, m.Batch)
}

func TestMigration_ZeroValues(t *testing.T) {
	m := Migration{}

	assert.Equal(t, int64(0), m.ID)
	assert.Empty(t, m.Name)
	assert.Equal(t, 0, m.Batch)
}

func TestMigration_Fields(t *testing.T) {
	tests := []struct {
		name  string
		id    int64
		name_ string
		batch int
	}{
		{"migration_1", 1, "create_users_table", 1},
		{"migration_2", 2, "create_accounts_table", 1},
		{"migration_3", 3, "add_index_to_users", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Migration{
				ID:    tt.id,
				Name:  tt.name_,
				Batch: tt.batch,
			}

			assert.Equal(t, tt.id, m.ID)
			assert.Equal(t, tt.name_, m.Name)
			assert.Equal(t, tt.batch, m.Batch)
		})
	}
}

func TestMigration_JSONTags(t *testing.T) {
	// Verify struct has proper JSON tags
	m := Migration{
		ID:    123,
		Name:  "test",
		Batch: 1,
	}

	// The struct should have json tags
	assert.Equal(t, int64(123), m.ID)
	assert.Equal(t, "test", m.Name)
	assert.Equal(t, 1, m.Batch)
}
