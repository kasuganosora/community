package matrix

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"bearer token", "Bearer my-token-123", "my-token-123"}, //nolint:goconst
		{"raw token", "my-token-123", "my-token-123"},
		{"empty string", "", ""},
		{"only Bearer prefix", "Bearer ", "Bearer "}, //nolint:goconst
		{"short string", "Bear", "Bear"},
		{"exact 7 chars", "Bearer1", "Bearer1"},
		{"bearer lowercase", "bearer my-token", "bearer my-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBearerToken(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsProcessedTxn(t *testing.T) {
	// Reset the global state
	processedTxnsMu.Lock()
	processedTxns = make(map[string]time.Time)
	processedTxnsMu.Unlock()

	t.Run("first time returns false", func(t *testing.T) {
		assert.False(t, isProcessedTxn("txn-001"))
	})

	t.Run("second time returns true", func(t *testing.T) {
		assert.True(t, isProcessedTxn("txn-001"))
	})

	t.Run("different txn returns false", func(t *testing.T) {
		assert.False(t, isProcessedTxn("txn-002"))
	})

	t.Run("cleans up old entries", func(t *testing.T) {
		processedTxnsMu.Lock()
		processedTxns["old-txn"] = time.Now().Add(-2 * time.Hour)
		processedTxnsMu.Unlock()

		// This call should trigger cleanup of old-txn
		isProcessedTxn("txn-003")

		processedTxnsMu.Lock()
		_, exists := processedTxns["old-txn"]
		processedTxnsMu.Unlock()

		assert.False(t, exists, "old entry should have been cleaned up")
	})
}

// TestNewWebhookHandler is skipped because NewWebhookHandler calls NewASClient
// which calls LoadConfig -> systemsettings.GetDomain (requires DB).
// This test should be run in integration test environments with DB access.

func TestParseMatrixUserID(t *testing.T) {
	tests := []struct {
		name      string
		userID    string
		localpart string
		domain    string
		ok        bool
	}{
		{"valid user", "@alice:example.com", "alice", "example.com", true},
		{"with prefix", "@community_bob:matrix.org", "community_bob", "matrix.org", true},
		{"no at sign", "alice:example.com", "", "", false},
		{"no colon", "@alice", "", "", false},
		{"empty", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localpart, domain, ok := ParseMatrixUserID(tt.userID)
			assert.Equal(t, tt.ok, ok)

			if ok {
				assert.Equal(t, tt.localpart, localpart)
				assert.Equal(t, tt.domain, domain)
			}
		})
	}
}
