package matrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// UserMapper construction & cache
// ====================================================================

func TestNewUserMapper(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "a"})
	um := NewUserMapper(c)
	require.NotNil(t, um)
	assert.Equal(t, c, um.client)
	assert.NotNil(t, um.cache)
}

func TestUserMapper_ClearCache(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	// ClearCache should not panic on empty cache.
	um.ClearCache()
}

// ====================================================================
// IsMatrixUser (pure logic, only needs GlobalConfig)
// ====================================================================

func TestUserMapper_IsMatrixUser(t *testing.T) {
	setupTestGlobalConfig("http://unused")
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	t.Run("in namespace", func(t *testing.T) {
		assert.True(t, um.IsMatrixUser("@_c_alice:example.com"))
	})

	t.Run("not in namespace", func(t *testing.T) {
		assert.False(t, um.IsMatrixUser("@bob:example.com"))
	})

	t.Run("invalid id", func(t *testing.T) {
		assert.False(t, um.IsMatrixUser("invalid"))
	})

	t.Run("empty", func(t *testing.T) {
		assert.False(t, um.IsMatrixUser(""))
	})
}

// ====================================================================
// cacheUser / GetOrCreateLocalUserByMatrix (cache hit path)
// ====================================================================

func TestUserMapper_CacheHit_MatrixToLocal(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	// Manually seed the cache.
	user := &models.MatrixUser{
		Id:             1,
		LocalAccountId: 100,
		MatrixUserID:   "@remote:example.com",
		Direction:      "remote",
	}
	um.cacheUser(user)

	// Second call should hit cache and return the seeded user.
	got, err := um.GetOrCreateLocalUserByMatrix("@remote:example.com")
	require.NoError(t, err)
	assert.Equal(t, user, got)
}

func TestUserMapper_GetMatrixUserID_CacheHit(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	user := &models.MatrixUser{
		Id:             2,
		LocalAccountId: 200,
		MatrixUserID:   "@_c_alice:example.com",
		Direction:      "local",
	}
	um.cacheUser(user)

	id, err := um.GetMatrixUserID(200)
	require.NoError(t, err)
	assert.Equal(t, "@_c_alice:example.com", id)
}

func TestUserMapper_GetLocalAccountID_CacheHit(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	user := &models.MatrixUser{
		Id:             3,
		LocalAccountId: 300,
		MatrixUserID:   "@bob:example.com",
		Direction:      "remote",
	}
	um.cacheUser(user)

	id, err := um.GetLocalAccountID("@bob:example.com")
	require.NoError(t, err)
	assert.Equal(t, uint64(300), id)
}

// ====================================================================
// cacheUser does not populate localToMatrix for remote users
// ====================================================================

func TestUserMapper_CacheUser_RemoteNotInLocalMap(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	// A remote-direction user should not appear in localToMatrix cache.
	user := &models.MatrixUser{
		LocalAccountId: 999,
		MatrixUserID:   "@remote:x",
		Direction:      "remote",
	}
	um.cacheUser(user)

	um.cache.RLock()
	_, ok := um.cache.localToMatrix[999]
	um.cache.RUnlock()
	assert.False(t, ok, "remote user should not be in localToMatrix cache")
}

func TestUserMapper_CacheUser_LocalInLocalMap(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	user := &models.MatrixUser{
		LocalAccountId: 888,
		MatrixUserID:   "@_c_bob:x",
		Direction:      "local",
	}
	um.cacheUser(user)

	um.cache.RLock()
	got, ok := um.cache.localToMatrix[888]
	um.cache.RUnlock()
	assert.True(t, ok)
	assert.Equal(t, user, got)
}

// ====================================================================
// GetOrCreateMatrixUserByLocal (DB-backed, cache miss)
// ====================================================================

func TestUserMapper_GetOrCreateMatrixUserByLocal_DBHit(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Create a local account.
	account := &models.Account{Username: "alice", DisplayName: "Alice"}
	require.NoError(t, db.Create(account).Error)

	// Create the mapping record directly.
	existing := &models.MatrixUser{
		LocalAccountId:  account.Id,
		MatrixUserID:    "@_c_alice:example.com",
		MatrixLocalpart: "_c_alice",
		MatrixServer:    "example.com",
		Direction:       "local",
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "a"})
	um := NewUserMapper(c)

	got, err := um.GetOrCreateMatrixUserByLocal(account.Id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "@_c_alice:example.com", got.MatrixUserID)
	assert.Equal(t, "local", got.Direction)
}

func TestUserMapper_GetOrCreateMatrixUserByLocal_CreateNew(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Create a local account.
	account := &models.Account{Username: "bob"}
	require.NoError(t, db.Create(account).Error)

	// Mock HS that accepts virtual user registration and profile updates.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)

	got, err := um.GetOrCreateMatrixUserByLocal(account.Id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "@_c_bob:example.com", got.MatrixUserID)
	assert.Equal(t, "local", got.Direction)
	assert.Equal(t, "bob", got.DisplayName)
}

func TestUserMapper_GetOrCreateMatrixUserByLocal_AccountNotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)

	_, err := um.GetOrCreateMatrixUserByLocal(99999)
	require.Error(t, err)
}

// ====================================================================
// GetOrCreateLocalUserByMatrix (DB-backed)
// ====================================================================

func TestUserMapper_GetOrCreateLocalUserByMatrix_DBHit(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Create the remote mapping record directly.
	existing := &models.MatrixUser{
		LocalAccountId:  500,
		MatrixUserID:    "@remote:example.com",
		MatrixLocalpart: "remote",
		MatrixServer:    "example.com",
		Direction:       "remote",
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)

	got, err := um.GetOrCreateLocalUserByMatrix("@remote:example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "remote", got.Direction)
}

func TestUserMapper_GetOrCreateLocalUserByMatrix_LocalMappingRejected(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)

	// A user ID in the local namespace should be rejected (avoids loop).
	_, err := um.GetOrCreateLocalUserByMatrix("@_c_alice:example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local mapping")
}

func TestUserMapper_GetOrCreateLocalUserByMatrix_InvalidID(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	_, err := um.GetOrCreateLocalUserByMatrix("invalid-id")
	require.Error(t, err)
}

func TestUserMapper_GetOrCreateLocalUserByMatrix_CreateNew(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Mock HS returns a profile.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"displayname": "Remote User",
			"avatar_url":  "mxc://example.com/avatar",
		})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)

	got, err := um.GetOrCreateLocalUserByMatrix("@carol:matrix.org")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "remote", got.Direction)
	assert.Equal(t, "@carol:matrix.org", got.MatrixUserID)
	assert.Equal(t, "carol", got.MatrixLocalpart)
	assert.Equal(t, "matrix.org", got.MatrixServer)
	assert.Equal(t, "Remote User", got.DisplayName)
	assert.Equal(t, "mxc://example.com/avatar", got.AvatarURL)

	// Local username should be generated.
	require.NotNil(t, got.LocalAccount)
	assert.Contains(t, got.LocalAccount.Username, "matrix_carol_matrix_org")
}

func TestUserMapper_GetOrCreateLocalUserByMatrix_CreateNew_ProfileError(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Mock HS returns an error for profile fetch — mapping should still be
	// created with empty profile.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"no profile"}`))
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)

	got, err := um.GetOrCreateLocalUserByMatrix("@dave:matrix.org")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "", got.DisplayName)
	assert.Equal(t, "", got.AvatarURL)
}

// ====================================================================
// UpdateUserProfile (DB-backed)
// ====================================================================

func TestUserMapper_UpdateUserProfile(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	user := &models.MatrixUser{
		LocalAccountId:  1,
		MatrixUserID:    "@u:example.com",
		MatrixLocalpart: "u",
		MatrixServer:    "example.com",
		Direction:       "remote",
		DisplayName:     "Old",
		AvatarURL:       "old-url",
	}
	require.NoError(t, db.Create(user).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	// Seed cache to test the cache update path too.
	um.cacheUser(user)

	err := um.UpdateUserProfile("@u:example.com", "New Name", "new-url")
	require.NoError(t, err)

	// Verify DB updated.
	var updated models.MatrixUser
	require.NoError(t, db.Where("matrix_user_id = ?", "@u:example.com").First(&updated).Error)
	assert.Equal(t, "New Name", updated.DisplayName)
	assert.Equal(t, "new-url", updated.AvatarURL)

	// Verify cache updated.
	um.cache.RLock()
	cached := um.cache.matrixToLocal["@u:example.com"]
	um.cache.RUnlock()
	assert.Equal(t, "New Name", cached.DisplayName)
}

// ====================================================================
// GetLocalAccountFromMatrixUser
// ====================================================================

func TestUserMapper_GetLocalAccountFromMatrixUser(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "frank"}
	require.NoError(t, db.Create(account).Error)

	matrixUser := &models.MatrixUser{
		LocalAccountId:  account.Id,
		MatrixUserID:    "@frank:example.com",
		MatrixLocalpart: "frank",
		MatrixServer:    "example.com",
		Direction:       "remote",
	}
	require.NoError(t, db.Create(matrixUser).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)

	got, err := um.GetLocalAccountFromMatrixUser("@frank:example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, account.Id, got.Id)
	assert.Equal(t, "frank", got.Username)
}
