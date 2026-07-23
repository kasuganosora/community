package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGroup_TableName(t *testing.T) {
	g := Group{}
	assert.Equal(t, "groups", g.TableName())
}

func TestGroupMember_TableName(t *testing.T) {
	gm := GroupMember{}
	assert.Equal(t, "group_members", gm.TableName())
}

func TestGroup_IsBridged(t *testing.T) {
	t.Run("not bridged - nil room id", func(t *testing.T) {
		g := &Group{MatrixRoomId: nil}
		assert.False(t, g.IsBridged())
	})

	t.Run("not bridged - empty room id", func(t *testing.T) {
		empty := ""
		g := &Group{MatrixRoomId: &empty}
		assert.False(t, g.IsBridged())
	})

	t.Run("bridged", func(t *testing.T) {
		roomId := "!room123:matrix.org"
		g := &Group{MatrixRoomId: &roomId}
		assert.True(t, g.IsBridged())
	})
}

func TestGroupType_Constants(t *testing.T) {
	assert.Equal(t, GroupTypeLocal, GroupType(0))
	assert.Equal(t, GroupTypeBridged, GroupType(1))
	assert.Equal(t, GroupTypeMatrix, GroupType(2))
}

func TestGroupMemberRole_Constants(t *testing.T) {
	assert.Equal(t, GroupMemberRoleMember, GroupMemberRole(0))
	assert.Equal(t, GroupMemberRoleAdmin, GroupMemberRole(1))
	assert.Equal(t, GroupMemberRoleOwner, GroupMemberRole(2))
}
