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
// GroupMapper construction
// ====================================================================

func TestNewGroupMapper(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "a"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)
	require.NotNil(t, gm)
	assert.Equal(t, c, gm.client)
	assert.Equal(t, um, gm.userMapper)
}

// ====================================================================
// generateRoomAlias
// ====================================================================

func TestGroupMapper_GenerateRoomAlias(t *testing.T) {
	setupTestGlobalConfig("http://unused")
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	alias := gm.generateRoomAlias(42)
	assert.Equal(t, "_c__group_42", alias)
}

// ====================================================================
// CreateBridgedGroup
// ====================================================================

func TestGroupMapper_CreateBridgedGroup(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	owner := &models.Account{Username: "groupowner"}
	require.NoError(t, db.Create(owner).Error)

	// Mock HS returns a room ID for createRoom.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"room_id": "!grouproom:example.com"})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	group, err := gm.CreateBridgedGroup(owner.Id, "Test Group", "desc")
	require.NoError(t, err)
	require.NotNil(t, group)
	assert.Equal(t, "Test Group", group.Name)
	assert.Equal(t, "desc", group.Description)
	assert.Equal(t, models.GroupTypeBridged, group.Type)
	require.NotNil(t, group.MatrixRoomId)
	assert.Equal(t, "!grouproom:example.com", *group.MatrixRoomId)

	// Verify owner was added as a member.
	var memberCount int64
	db.Model(&models.GroupMember{}).Where("group_id = ?", group.Id).Count(&memberCount)
	assert.Equal(t, int64(1), memberCount)
}

func TestGroupMapper_CreateBridgedGroup_RoomError(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	owner := &models.Account{Username: "owner2"}
	require.NoError(t, db.Create(owner).Error)

	// Mock HS returns an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	group, err := gm.CreateBridgedGroup(owner.Id, "Fail Group", "")
	require.Error(t, err)
	assert.Nil(t, group)

	// The local group should have been rolled back.
	var count int64
	db.Model(&models.Group{}).Where("owner_id = ?", owner.Id).Count(&count)
	assert.Equal(t, int64(0), count)
}

// ====================================================================
// GetGroupByMatrixRoom
// ====================================================================

func TestGroupMapper_GetGroupByMatrixRoom(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	roomID := "!findroom:example.com"
	group := &models.Group{
		OwnerId:      1,
		Name:         "Find Me",
		Type:         models.GroupTypeBridged,
		MatrixRoomId: &roomID,
	}
	require.NoError(t, db.Create(group).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	got, err := gm.GetGroupByMatrixRoom(roomID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, group.Id, got.Id)
	assert.Equal(t, "Find Me", got.Name)
}

func TestGroupMapper_GetGroupByMatrixRoom_NotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	got, err := gm.GetGroupByMatrixRoom("!notfound:example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ====================================================================
// AddMatrixMemberToGroup / RemoveMemberFromGroup
// ====================================================================

func TestGroupMapper_AddMatrixMemberToGroup(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	group := &models.Group{OwnerId: 1, Name: "Members"}
	require.NoError(t, db.Create(group).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	err := gm.AddMatrixMemberToGroup(group.Id, "@matrixmember:example.com", "MatrixMember", models.GroupMemberRoleMember)
	require.NoError(t, err)

	var member models.GroupMember
	require.NoError(t, db.Where("group_id = ?", group.Id).First(&member).Error)
	assert.Equal(t, "MatrixMember", member.DisplayName)
	require.NotNil(t, member.MatrixUserId)
	assert.Equal(t, "@matrixmember:example.com", *member.MatrixUserId)
}

func TestGroupMapper_RemoveMemberFromGroup_ByMatrixID(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	group := &models.Group{OwnerId: 1, Name: "Removable"}
	require.NoError(t, db.Create(group).Error)

	matrixID := "@remove:example.com"
	member := &models.GroupMember{
		GroupId:      group.Id,
		MatrixUserId: &matrixID,
		DisplayName:  "Remove Me",
	}
	require.NoError(t, db.Create(member).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	err := gm.RemoveMemberFromGroup(group.Id, nil, &matrixID)
	require.NoError(t, err)

	var count int64
	db.Model(&models.GroupMember{}).Where("group_id = ?", group.Id).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestGroupMapper_RemoveMemberFromGroup_ByAccountID(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "localmember"}
	require.NoError(t, db.Create(account).Error)

	group := &models.Group{OwnerId: 1, Name: "LocalRemovable"}
	require.NoError(t, db.Create(group).Error)

	member := &models.GroupMember{
		GroupId:   group.Id,
		AccountId: &account.Id,
	}
	require.NoError(t, db.Create(member).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	err := gm.RemoveMemberFromGroup(group.Id, &account.Id, nil)
	require.NoError(t, err)

	var count int64
	db.Model(&models.GroupMember{}).Where("group_id = ?", group.Id).Count(&count)
	assert.Equal(t, int64(0), count)
}

// ====================================================================
// GetGroupMembers
// ====================================================================

func TestGroupMapper_GetGroupMembers(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	group := &models.Group{OwnerId: 1, Name: "MultiMember"}
	require.NoError(t, db.Create(group).Error)

	matrixID1 := "@m1:example.com"
	matrixID2 := "@m2:example.com"
	members := []*models.GroupMember{
		{GroupId: group.Id, MatrixUserId: &matrixID1, DisplayName: "M1"},
		{GroupId: group.Id, MatrixUserId: &matrixID2, DisplayName: "M2"},
	}
	for _, m := range members {
		require.NoError(t, db.Create(m).Error)
	}

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	got, err := gm.GetGroupMembers(group.Id)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// ====================================================================
// IsGroupMember
// ====================================================================

func TestGroupMapper_IsGroupMember_True(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "ismember"}
	require.NoError(t, db.Create(account).Error)

	group := &models.Group{OwnerId: 1, Name: "Check"}
	require.NoError(t, db.Create(group).Error)

	member := &models.GroupMember{
		GroupId:   group.Id,
		AccountId: &account.Id,
	}
	require.NoError(t, db.Create(member).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	isMember, err := gm.IsGroupMember(group.Id, &account.Id, nil)
	require.NoError(t, err)
	assert.True(t, isMember)
}

func TestGroupMapper_IsGroupMember_False(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	group := &models.Group{OwnerId: 1, Name: "Check2"}
	require.NoError(t, db.Create(group).Error)

	accountID := uint64(9999)
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	isMember, err := gm.IsGroupMember(group.Id, &accountID, nil)
	require.NoError(t, err)
	assert.False(t, isMember)
}

// ====================================================================
// BridgeExistingGroup
// ====================================================================

func TestGroupMapper_BridgeExistingGroup(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	group := &models.Group{OwnerId: 1, Name: "Existing", Type: models.GroupTypeLocal, Visibility: "private"}
	require.NoError(t, db.Create(group).Error)

	// Mock HS returns a room ID.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"room_id": "!bridged:example.com"})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	err := gm.BridgeExistingGroup(group.Id)
	require.NoError(t, err)

	// Verify group updated.
	var updated models.Group
	require.NoError(t, db.First(&updated, group.Id).Error)
	assert.Equal(t, models.GroupTypeBridged, updated.Type)
	require.NotNil(t, updated.MatrixRoomId)
	assert.Equal(t, "!bridged:example.com", *updated.MatrixRoomId)
}

func TestGroupMapper_BridgeExistingGroup_AlreadyBridged(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	roomID := "!already:example.com"
	group := &models.Group{
		OwnerId:      1,
		Name:         "Already",
		Type:         models.GroupTypeBridged,
		MatrixRoomId: &roomID,
	}
	require.NoError(t, db.Create(group).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	err := gm.BridgeExistingGroup(group.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already bridged")
}

func TestGroupMapper_BridgeExistingGroup_NotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	gm := NewGroupMapper(c, um)

	err := gm.BridgeExistingGroup(99999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group not found")
}
