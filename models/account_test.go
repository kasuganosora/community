package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccount_IsLocal(t *testing.T) {
	tests := []struct {
		name     string
		account  Account
		expected bool
	}{
		{
			name:     "local account (nil domain)",
			account:  Account{Username: "localuser", Domain: nil},
			expected: true,
		},
		{
			name:     "remote account",
			account:  Account{Username: "remoteuser", Domain: strPtr("example.com")},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.account.IsLocal())
		})
	}
}

func TestAccounts_GetIds(t *testing.T) {
	accounts := Accounts{
		{Id: 1, Username: "user1"},
		{Id: 2, Username: "user2"},
		{Id: 3, Username: "user3"},
	}

	ids := accounts.GetIds()

	assert.Equal(t, []uint64{1, 2, 3}, ids)
}

func TestAccounts_GetIds_Empty(t *testing.T) {
	accounts := Accounts{}

	ids := accounts.GetIds()

	assert.Nil(t, ids)
}

func TestAccounts_Contains(t *testing.T) {
	accounts := Accounts{
		{Id: 1, Username: "user1"},
		{Id: 2, Username: "user2"},
		{Id: 3, Username: "user3"},
	}

	assert.True(t, accounts.Contains(1))
	assert.True(t, accounts.Contains(2))
	assert.True(t, accounts.Contains(3))
	assert.False(t, accounts.Contains(4))
	assert.False(t, accounts.Contains(0))
}

func TestAccounts_Contains_Empty(t *testing.T) {
	accounts := Accounts{}

	assert.False(t, accounts.Contains(1))
}

func TestMatchAccountTags(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			name:     "single mention",
			text:     "Hello @user1",
			expected: []string{"@user1"},
		},
		{
			name:     "multiple mentions",
			text:     "Hello @user1 and @user2",
			expected: []string{"@user1", "@user2"},
		},
		{
			name:     "mention with domain",
			text:     "Hello @user1@example.com",
			expected: []string{"@user1@example.com"},
		},
		{
			name:     "no mentions",
			text:     "Hello world",
			expected: []string{}, // Empty slice, not nil
		},
		{
			name:     "mention at end",
			text:     "Hello world @user1",
			expected: []string{"@user1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchAccountTags(tt.text)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function
func strPtr(s string) *string {
	return &s
}
