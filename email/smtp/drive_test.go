package smtp

import "testing"

func TestIsValidEmailAddress(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected bool
	}{
		{"valid email", "user@example.com", true},
		{"valid email with display name", "John Doe <john@example.com>", true},
		{"valid email with subdomain", "user@mail.example.com", true},
		{"valid email with plus", "user+tag@example.com", true},
		{"empty string", "", false},
		{"no at sign", "userexample.com", false},
		{"no domain", "user@", false},
		{"no local part", "@example.com", false},
		{"multiple at signs invalid", "user@@example.com", false},
		{"just domain", "example.com", false},
		{"with spaces only", "   ", false},
		{"trailing whitespace handled", "  user@example.com  ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidEmailAddress(tt.addr)
			if result != tt.expected {
				t.Errorf("isValidEmailAddress(%q) = %v, want %v", tt.addr, result, tt.expected)
			}
		})
	}
}
