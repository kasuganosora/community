package oauth

import (
	"testing"
)

func TestIsValidRedirectUri(t *testing.T) {
	tests := []struct {
		name           string
		registeredUris string
		requestedUri   string
		expected       bool
	}{
		{
			name:           "exact match single uri",
			registeredUris: "https://app.example.com/callback",
			requestedUri:   "https://app.example.com/callback",
			expected:       true,
		},
		{
			name:           "exact match one of multiple uris",
			registeredUris: "https://app.example.com/callback\nhttps://app2.example.com/cb",
			requestedUri:   "https://app2.example.com/cb",
			expected:       true,
		},
		{
			name:           "no match",
			registeredUris: "https://app.example.com/callback",
			requestedUri:   "https://evil.example.com/callback",
			expected:       false,
		},
		{
			name:           "empty requested uri rejected",
			registeredUris: "https://app.example.com/callback",
			requestedUri:   "",
			expected:       false,
		},
		{
			name:           "whitespace requested uri rejected",
			registeredUris: "https://app.example.com/callback",
			requestedUri:   "   ",
			expected:       false,
		},
		// Critical security test: OOB registered should NOT allow arbitrary redirect URIs.
		// Previously, registering urn:ietf:wg:oauth:2.0:oob allowed ANY redirect_uri.
		{
			name:           "OOB registered does not allow arbitrary redirect (open redirect fix)",
			registeredUris: "urn:ietf:wg:oauth:2.0:oob",
			requestedUri:   "https://evil.example.com/callback",
			expected:       false,
		},
		{
			name:           "OOB registered and OOB requested is allowed",
			registeredUris: "urn:ietf:wg:oauth:2.0:oob",
			requestedUri:   "urn:ietf:wg:oauth:2.0:oob",
			expected:       true,
		},
		{
			name:           "OOB among multiple registered, arbitrary redirect rejected",
			registeredUris: "https://app.example.com/callback\nurn:ietf:wg:oauth:2.0:oob",
			requestedUri:   "https://attacker.example.com/callback",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidRedirectUri(tt.registeredUris, tt.requestedUri)
			if result != tt.expected {
				t.Errorf("isValidRedirectUri(%q, %q) = %v, want %v",
					tt.registeredUris, tt.requestedUri, result, tt.expected)
			}
		})
	}
}

func TestBuildRedirectUrl(t *testing.T) {
	t.Run("simple url with code and state", func(t *testing.T) {
		result := buildRedirectUrl("https://app.example.com/callback", "mycode", "mystate")
		// Should contain both code and state as encoded params
		if result == "" {
			t.Error("expected non-empty redirect URL")
		}

		if !contains(result, "code=mycode") {
			t.Errorf("expected redirect URL to contain code=mycode, got: %s", result)
		}

		if !contains(result, "state=mystate") {
			t.Errorf("expected redirect URL to contain state=mystate, got: %s", result)
		}
	})

	t.Run("state with special characters is encoded", func(t *testing.T) {
		// State containing '&' should be URL-encoded, not injected as a raw param
		maliciousState := "valid&extra=evil"
		result := buildRedirectUrl("https://app.example.com/callback", "mycode", maliciousState)

		// The '&' should be encoded as %26, preventing parameter injection
		if !contains(result, "extra%3Devil") && contains(result, "extra=evil") {
			t.Errorf("state parameter injection detected in URL: %s", result)
		}
	})

	t.Run("url with existing query params uses ampersand separator", func(t *testing.T) {
		result := buildRedirectUrl("https://app.example.com/callback?foo=bar", "mycode", "")
		if !contains(result, "&") {
			t.Errorf("expected ampersand separator in URL: %s", result)
		}
	})

	t.Run("code with special characters is encoded", func(t *testing.T) {
		// Normally code is hex, but test encoding anyway
		result := buildRedirectUrl("https://app.example.com/callback", "co#de", "st te")
		// Should be encoded properly
		if !contains(result, "co%23de") {
			t.Errorf("code not properly encoded in URL: %s", result)
		}
	})
}

func TestBuildDenyRedirectUrl(t *testing.T) {
	t.Run("simple url with error and state", func(t *testing.T) {
		result := buildDenyRedirectUrl("https://app.example.com/callback", "mystate")
		if result == "" {
			t.Error("expected non-empty redirect URL")
		}

		if !contains(result, "error=access_denied") {
			t.Errorf("expected redirect URL to contain error=access_denied, got: %s", result)
		}

		if !contains(result, "state=mystate") {
			t.Errorf("expected redirect URL to contain state=mystate, got: %s", result)
		}
	})

	t.Run("url without state", func(t *testing.T) {
		result := buildDenyRedirectUrl("https://app.example.com/callback", "")
		if !contains(result, "error=access_denied") {
			t.Errorf("expected redirect URL to contain error=access_denied, got: %s", result)
		}

		if contains(result, "state=") {
			t.Errorf("should not contain state param when state is empty: %s", result)
		}
	})

	t.Run("state with special characters is encoded", func(t *testing.T) {
		maliciousState := "valid&extra=evil"
		result := buildDenyRedirectUrl("https://app.example.com/callback", maliciousState)

		// The '&' should be encoded as %26, preventing parameter injection
		if !contains(result, "extra%3Devil") && contains(result, "extra=evil") {
			t.Errorf("state parameter injection detected in URL: %s", result)
		}
	})

	t.Run("url with existing query params uses ampersand separator", func(t *testing.T) {
		result := buildDenyRedirectUrl("https://app.example.com/callback?foo=bar", "")
		if !contains(result, "foo=bar") || !contains(result, "error=access_denied") || !contains(result, "&") {
			t.Errorf("expected existing params kept and error appended with &: %s", result)
		}

		if contains(result, "callback?foo=bar?error=") {
			t.Errorf("second question mark would break the query: %s", result)
		}
	})

	t.Run("error_description is url encoded", func(t *testing.T) {
		result := buildDenyRedirectUrl("https://app.example.com/callback", "")
		if !contains(result, "error_description=The+user+denied+the+request") &&
			!contains(result, "error_description=The%20user%20denied%20the%20request") {
			t.Errorf("error_description not properly URL-encoded: %s", result)
		}
	})
}

// contains is a simple substring check helper.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}

	return false
}
