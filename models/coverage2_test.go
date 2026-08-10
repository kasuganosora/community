package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// tag.go
// ============================================================================

func TestTag_TableName(t *testing.T) {
	tag := Tag{}
	assert.Equal(t, "tags", tag.TableName())
}

func TestMatchTags(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			name:     "single tag",
			text:     "Hello #golang",
			expected: []string{"#golang"},
		},
		{
			name:     "multiple tags",
			text:     "Hello #golang and #testing",
			expected: []string{"#golang", "#testing"},
		},
		{
			name:     "no tags",
			text:     "Hello world",
			expected: []string{},
		},
		{
			name:     "duplicate tags deduplicated",
			text:     "#go #go #go",
			expected: []string{"#go"},
		},
		{
			name:     "unicode tag",
			text:     "Hello #你好",
			expected: []string{"#你好"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchTags(tt.text)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTag_Fields(t *testing.T) {
	tag := Tag{
		Id:          1,
		Name:        "golang",
		StatusCount: 42,
	}
	assert.Equal(t, uint64(1), tag.Id)
	assert.Equal(t, "golang", tag.Name)
	assert.Equal(t, uint64(42), tag.StatusCount)
}

// ============================================================================
// group.go (additional methods)
// ============================================================================

func TestGroupMember_IsLocalUser(t *testing.T) {
	id := uint64(100)
	gm := &GroupMember{AccountId: &id}
	assert.True(t, gm.IsLocalUser())

	gm2 := &GroupMember{AccountId: nil}
	assert.False(t, gm2.IsLocalUser())
}

func TestGroupMember_IsMatrixUser(t *testing.T) {
	matrixId := "@bob:matrix.org"
	gm := &GroupMember{MatrixUserId: &matrixId}
	assert.True(t, gm.IsMatrixUser())

	gm2 := &GroupMember{MatrixUserId: nil}
	assert.False(t, gm2.IsMatrixUser())
}

func TestGroupMember_GetUserIdentifier(t *testing.T) {
	t.Run("local user", func(t *testing.T) {
		id := uint64(100)
		gm := &GroupMember{AccountId: &id}
		assert.Equal(t, "100", gm.GetUserIdentifier())
	})

	t.Run("matrix user", func(t *testing.T) {
		matrixId := "@bob:matrix.org"
		gm := &GroupMember{MatrixUserId: &matrixId}
		assert.Equal(t, "@bob:matrix.org", gm.GetUserIdentifier())
	})

	t.Run("neither", func(t *testing.T) {
		gm := &GroupMember{}
		assert.Equal(t, "", gm.GetUserIdentifier())
	})
}

func TestGroupMessage_TableName(t *testing.T) {
	gm := GroupMessage{}
	assert.Equal(t, "group_messages", gm.TableName())
}

func TestGroupMessage_IsFromLocal(t *testing.T) {
	id := uint64(100)
	gm := &GroupMessage{SenderId: &id}
	assert.True(t, gm.IsFromLocal())

	gm2 := &GroupMessage{SenderId: nil}
	assert.False(t, gm2.IsFromLocal())
}

func TestGroupMessage_IsFromMatrix(t *testing.T) {
	matrixId := "@bob:matrix.org"
	gm := &GroupMessage{MatrixSenderId: &matrixId}
	assert.True(t, gm.IsFromMatrix())

	gm2 := &GroupMessage{MatrixSenderId: nil}
	assert.False(t, gm2.IsFromMatrix())
}

func TestGroupMember_Fields(t *testing.T) {
	id := uint64(100)
	matrixId := "@bob:matrix.org"
	invitedBy := uint64(5)
	matrixInvitedBy := "@admin:matrix.org"
	gm := GroupMember{
		Id:              1,
		GroupId:         10,
		AccountId:       &id,
		MatrixUserId:    &matrixId,
		DisplayName:     "Bob",
		Role:            GroupMemberRoleAdmin,
		InvitedBy:       &invitedBy,
		MatrixInvitedBy: &matrixInvitedBy,
	}
	assert.Equal(t, uint64(1), gm.Id)
	assert.Equal(t, uint64(10), gm.GroupId)
	assert.Equal(t, "Bob", gm.DisplayName)
	assert.Equal(t, GroupMemberRoleAdmin, gm.Role)
}

func TestGroupMessage_Fields(t *testing.T) {
	id := uint64(100)
	matrixId := "@bob:matrix.org"
	eventId := "$event:matrix.org"
	gm := GroupMessage{
		Id:             1,
		GroupId:        10,
		SenderId:       &id,
		MatrixSenderId: &matrixId,
		Content:        "Hello",
		MatrixEventId:  &eventId,
	}
	assert.Equal(t, uint64(1), gm.Id)
	assert.Equal(t, uint64(10), gm.GroupId)
	assert.Equal(t, "Hello", gm.Content)
	assert.Equal(t, id, *gm.SenderId)
}
