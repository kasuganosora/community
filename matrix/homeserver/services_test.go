package homeserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== AliasService tests =====

func TestAliasService_NewAndHelpers(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)
	assert.NotNil(t, s)
	assert.Equal(t, hs, s.hs)
}

func TestAliasService_SetAndGetByAlias(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)

	// Set alias (without # prefix, should be normalized)
	err := s.SetAlias("general", "!room1:test.local", "@alice:test.local")
	require.NoError(t, err)

	// Get by alias (without # prefix, should be normalized)
	roomID, err := s.GetRoomIDByAlias("general")
	require.NoError(t, err)
	assert.Equal(t, "!room1:test.local", roomID)
}

func TestAliasService_GetRoomIDByAlias_NotFound(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)

	_, err := s.GetRoomIDByAlias("#nonexistent:test.local")
	assert.Error(t, err)
}

func TestAliasService_GetRoomIDByAlias_FullAlias(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)

	err := s.SetAlias("#myroom:test.local", "!room2:test.local", "@bob:test.local")
	require.NoError(t, err)

	roomID, err := s.GetRoomIDByAlias("#myroom:test.local")
	require.NoError(t, err)
	assert.Equal(t, "!room2:test.local", roomID)
}

func TestAliasService_DeleteAlias(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)

	err := s.SetAlias("todelete", "!room3:test.local", "@alice:test.local")
	require.NoError(t, err)

	err = s.DeleteAlias("todelete")
	require.NoError(t, err)

	// Should no longer be found
	_, err = s.GetRoomIDByAlias("todelete")
	assert.Error(t, err)
}

func TestAliasService_GetAliasesForRoom(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)

	require.NoError(t, s.SetAlias("alias1", "!multi:test.local", "@a:test.local"))
	require.NoError(t, s.SetAlias("alias2", "!multi:test.local", "@b:test.local"))

	aliases, err := s.GetAliasesForRoom("!multi:test.local")
	require.NoError(t, err)
	assert.Len(t, aliases, 2)
}

func TestAliasService_GetAliasesForRoom_Empty(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)

	aliases, err := s.GetAliasesForRoom("!noroom:test.local")
	require.NoError(t, err)
	assert.Empty(t, aliases)
}

func TestAliasService_SetAlias_UpdatesExisting(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	s := NewAliasService(hs)

	require.NoError(t, s.SetAlias("update", "!old:test.local", "@a:test.local"))
	// Set same alias again with new room → should update
	require.NoError(t, s.SetAlias("update", "!new:test.local", "@b:test.local"))

	roomID, err := s.GetRoomIDByAlias("update")
	require.NoError(t, err)
	assert.Equal(t, "!new:test.local", roomID)
}

// ===== MatrixRoomAlias model tests =====

func TestMatrixRoomAlias_TableName(t *testing.T) {
	assert.Equal(t, "matrix_room_aliases", MatrixRoomAlias{}.TableName())
}

// ===== generateEventID tests =====

func TestGenerateEventID(t *testing.T) {
	id1 := generateEventID()
	id2 := generateEventID()
	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2, "event IDs should be unique")
	assert.Contains(t, id1, ":")
}

// ===== generateValidationToken tests (threepid.go) =====

func TestGenerateValidationToken(t *testing.T) {
	token := generateValidationToken()
	assert.NotEmpty(t, token)
}

// ===== convertAvatarToMXC tests (search.go) =====

func TestSearchService_ConvertAvatarToMXC(t *testing.T) {
	tests := []struct {
		name     string
		avatar   string
		expected string
	}{
		{"empty", "", ""},
		{"external URL", "https://example.com/img.png", ""},
		{"local upload", "/uploads/photo/abc123.png", "mxc://test.local/abc123"},
		{"local upload no ext", "/uploads/photo/xyz", "mxc://test.local/xyz"},
		{"local upload short", "/uploads/x", ""},
		{"non-upload path", "/other/path", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := minimalHS("test.local")
			s := NewSearchService(hs)
			result := s.convertAvatarToMXC(tt.avatar)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ===== NewUserSearchService test =====

func TestNewUserSearchService(t *testing.T) {
	hs := minimalHS("test.local")
	s := NewUserSearchService(hs)
	assert.NotNil(t, s)
}

// ===== NewSearchService test =====

func TestNewSearchService(t *testing.T) {
	hs := minimalHS("test.local")
	s := NewSearchService(hs)
	assert.NotNil(t, s)
}
