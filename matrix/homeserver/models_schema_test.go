package homeserver

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// MatrixEvent must not declare a relationship to MatrixRoom: GORM parsed the
// old "foreignKey:RoomID;references:RoomID" tag as has-one and tried to add
// an FK from matrix_rooms_hs.room_id to the non-unique matrix_events.room_id,
// which MySQL 8.4 rejects (error 6125) and which broke homeserver startup.
func TestMatrixEvent_HasNoRoomForeignKey(t *testing.T) {
	s, err := schema.Parse(&MatrixEvent{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	assert.Empty(t, s.Relationships.Relations)

	r, err := schema.Parse(&MatrixRoom{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	for _, rel := range r.Relationships.Relations {
		assert.NotEqual(t, "matrix_events", rel.FieldSchema.Table)
	}
}
