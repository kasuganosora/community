package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Conversation
// ============================================================================

func TestConversation_TableName(t *testing.T) {
	c := Conversation{}
	assert.Equal(t, "conversations", c.TableName())
}

func TestConversation_Fields(t *testing.T) {
	c := Conversation{
		Id:          1,
		Uri:         "test-uri",
		StatusesIds: []uint64{100, 200},
		AccountsIds: []uint64{10, 20},
	}

	assert.Equal(t, uint64(1), c.Id)
	assert.Equal(t, "test-uri", c.Uri)
	assert.Equal(t, []uint64{100, 200}, c.StatusesIds)
	assert.Equal(t, []uint64{10, 20}, c.AccountsIds)
}

// ============================================================================
// Conversation.AppendAccount
// ============================================================================

func TestConversation_AppendAccount_Empty(t *testing.T) {
	c := &Conversation{}

	c.AppendAccount(1)

	assert.Len(t, c.AccountsIds, 1)
	assert.Equal(t, uint64(1), c.AccountsIds[0])
}

func TestConversation_AppendAccount_Multiple(t *testing.T) {
	c := &Conversation{}

	c.AppendAccount(1)
	c.AppendAccount(2)
	c.AppendAccount(3)

	assert.Len(t, c.AccountsIds, 3)
	assert.Equal(t, uint64(1), c.AccountsIds[0])
	assert.Equal(t, uint64(3), c.AccountsIds[2])
}

func TestConversation_AppendAccount_Duplicate(t *testing.T) {
	c := &Conversation{}

	c.AppendAccount(1)
	c.AppendAccount(1)

	assert.Len(t, c.AccountsIds, 1)
	assert.Equal(t, uint64(1), c.AccountsIds[0])
}

// ============================================================================
// Conversation.AppendStatus
// ============================================================================

func TestConversation_AppendStatus_Empty(t *testing.T) {
	c := &Conversation{}

	c.AppendStatus(100)

	assert.Len(t, c.StatusesIds, 1)
	assert.Equal(t, uint64(100), c.StatusesIds[0])
}

func TestConversation_AppendStatus_Multiple(t *testing.T) {
	c := &Conversation{}

	c.AppendStatus(100)
	c.AppendStatus(200)
	c.AppendStatus(300)

	assert.Len(t, c.StatusesIds, 3)
	assert.Equal(t, uint64(100), c.StatusesIds[0])
	assert.Equal(t, uint64(300), c.StatusesIds[2])
}

func TestConversation_AppendStatus_Duplicate(t *testing.T) {
	c := &Conversation{}

	c.AppendStatus(100)
	c.AppendStatus(100)

	assert.Len(t, c.StatusesIds, 1)
	assert.Equal(t, uint64(100), c.StatusesIds[0])
}

// ============================================================================
// AccountConversation
// ============================================================================

func TestAccountConversation_TableName(t *testing.T) {
	ac := AccountConversation{}
	assert.Equal(t, "account_conversations", ac.TableName())
}

func TestAccountConversation_Fields(t *testing.T) {
	ac := AccountConversation{
		Id:             1,
		AccountId:      10,
		ConversationId: 20,
		LastStatusId:   0,
	}

	assert.Equal(t, uint64(1), ac.Id)
	assert.Equal(t, uint64(10), ac.AccountId)
	assert.Equal(t, uint64(20), ac.ConversationId)
	assert.Equal(t, uint64(0), ac.LastStatusId)
}

func TestAccountConversation_IsUnread_True(t *testing.T) {
	ac := &AccountConversation{
		LastStatusId: 0,
	}
	assert.True(t, ac.IsUnread())
}

func TestAccountConversation_IsUnread_False(t *testing.T) {
	ac := &AccountConversation{
		LastStatusId: 100,
	}
	assert.False(t, ac.IsUnread())
}
