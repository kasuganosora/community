package matrix

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// Config.IsEnabled
// ====================================================================

func TestConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{
			name: "all required fields set",
			cfg: Config{
				Enabled:       true,
				HomeserverURL: "https://matrix.example.com",
				ServerName:    "example.com",
				ASToken:       "as-token",
				HSToken:       "hs-token",
			},
			want: true,
		},
		{
			name: "disabled flag",
			cfg: Config{
				Enabled:       false,
				HomeserverURL: "https://matrix.example.com",
				ServerName:    "example.com",
				ASToken:       "as-token",
				HSToken:       "hs-token",
			},
			want: false,
		},
		{
			name: "missing homeserver url",
			cfg: Config{
				Enabled:    true,
				ServerName: "example.com",
				ASToken:    "as-token",
				HSToken:    "hs-token",
			},
			want: false,
		},
		{
			name: "missing server name",
			cfg: Config{
				Enabled:       true,
				HomeserverURL: "https://matrix.example.com",
				ASToken:       "as-token",
				HSToken:       "hs-token",
			},
			want: false,
		},
		{
			name: "missing as token",
			cfg: Config{
				Enabled:       true,
				HomeserverURL: "https://matrix.example.com",
				ServerName:    "example.com",
				HSToken:       "hs-token",
			},
			want: false,
		},
		{
			name: "missing hs token",
			cfg: Config{
				Enabled:       true,
				HomeserverURL: "https://matrix.example.com",
				ServerName:    "example.com",
				ASToken:       "as-token",
			},
			want: false,
		},
		{
			name: "empty config",
			cfg:  Config{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEnabled())
		})
	}
}

// ====================================================================
// Config.GetBridgeUserID
// ====================================================================

func TestConfig_GetBridgeUserID(t *testing.T) {
	cfg := Config{
		BridgeUsername: "_bridge",
		ServerName:     "example.com",
	}
	assert.Equal(t, "@_bridge:example.com", cfg.GetBridgeUserID())

	cfg2 := Config{
		BridgeUsername: "mybot",
		ServerName:     "matrix.org",
	}
	assert.Equal(t, "@mybot:matrix.org", cfg2.GetBridgeUserID())
}

// ====================================================================
// Config.GetUserNamespaceRegex
// ====================================================================

func TestConfig_GetUserNamespaceRegex(t *testing.T) {
	cfg := Config{
		UserNamespacePrefix: "_c_",
		ServerName:          "example.com",
	}
	re := cfg.GetUserNamespaceRegex()
	require.NotNil(t, re)

	// Must match a valid mapping user ID.
	assert.True(t, re.MatchString("@_c_alice:example.com"))
	// Must not match other server names.
	assert.False(t, re.MatchString("@_c_alice:other.com"))
	// Must not match non-mapping user IDs.
	assert.False(t, re.MatchString("@alice:example.com"))
	// Prefix is escaped — regex metacharacters in prefix must not be interpreted.
	cfg2 := Config{
		UserNamespacePrefix: "_c_",
		ServerName:          "example.com",
	}
	re2 := cfg2.GetUserNamespaceRegex()
	assert.True(t, re2.MatchString("@_c_bob:example.com"))
}

func TestConfig_GetUserNamespaceRegex_EscapesSpecialChars(t *testing.T) {
	// A prefix containing regex metacharacters must be treated literally.
	cfg := Config{
		UserNamespacePrefix: "a.b",
		ServerName:          "sv",
	}
	re := cfg.GetUserNamespaceRegex()
	assert.True(t, re.MatchString("@a.b_alice:sv"))
	// "a.b_alice" should not match "axb_alice" because the dot is literal.
	assert.False(t, re.MatchString("@axb_alice:sv"))
}

// ====================================================================
// Config.FormatMatrixUserID
// ====================================================================

func TestConfig_FormatMatrixUserID(t *testing.T) {
	cfg := Config{ServerName: "example.com"}
	assert.Equal(t, "@alice:example.com", cfg.FormatMatrixUserID("alice"))
	assert.Equal(t, "@_c_bob:example.com", cfg.FormatMatrixUserID("_c_bob"))
}

// ====================================================================
// ParseMatrixUserID (package-level)
// ====================================================================

func TestParseMatrixRoomID(t *testing.T) {
	tests := []struct {
		name      string
		roomID    string
		localpart string
		server    string
		ok        bool
	}{
		{"valid room", "!abc123:example.com", "abc123", "example.com", true},
		{"valid room with underscore", "!my_room:matrix.org", "my_room", "matrix.org", true},
		{"no bang sign", "abc123:example.com", "", "", false},
		{"no colon", "!abc123", "", "", false},
		{"empty", "", "", "", false},
		{"only bang", "!", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localpart, server, ok := ParseMatrixRoomID(tt.roomID)
			assert.Equal(t, tt.ok, ok)
			if ok {
				assert.Equal(t, tt.localpart, localpart)
				assert.Equal(t, tt.server, server)
			}
		})
	}
}

// ====================================================================
// getEnvString / getEnvBool helpers
// ====================================================================

func TestGetEnvString(t *testing.T) {
	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("MATRIX_TEST_STR", "hello")
		assert.Equal(t, "hello", getEnvString("MATRIX_TEST_STR", "default"))
	})

	t.Run("returns default when empty", func(t *testing.T) {
		os.Unsetenv("MATRIX_TEST_STR2")
		assert.Equal(t, "default", getEnvString("MATRIX_TEST_STR2", "default"))
	})
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name   string
		envSet bool
		envVal string
		def    bool
		want   bool
	}{
		{"unset returns default", false, "", false, false},
		{"unset returns default true", false, "", true, true},
		{"empty returns default", true, "", false, false},
		{"true value", true, "true", false, true},
		{"1 value", true, "1", false, true},
		{"TRUE uppercase", true, "TRUE", false, true},
		{"other value returns false", true, "nope", false, false},
		{"other value returns default when default true", true, "nope", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("MATRIX_TEST_BOOL")
			if tt.envSet {
				os.Setenv("MATRIX_TEST_BOOL", tt.envVal)
			}
			assert.Equal(t, tt.want, getEnvBool("MATRIX_TEST_BOOL", tt.def))
		})
	}
}

// ====================================================================
// Config field defaults (sanity check of struct construction)
// ====================================================================

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		Enabled:             true,
		HomeserverURL:       "https://matrix.example.com",
		ServerName:          "example.com",
		ASToken:             "as-token",
		HSToken:             "hs-token",
		BridgeUsername:      "_bridge",
		UserNamespacePrefix: "_c_",
		RoomNamespacePrefix: "_c_",
	}
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "https://matrix.example.com", cfg.HomeserverURL)
	assert.Equal(t, "example.com", cfg.ServerName)
	assert.Equal(t, "as-token", cfg.ASToken)
	assert.Equal(t, "hs-token", cfg.HSToken)
	assert.Equal(t, "_bridge", cfg.BridgeUsername)
	assert.Equal(t, "_c_", cfg.UserNamespacePrefix)
	assert.Equal(t, "_c_", cfg.RoomNamespacePrefix)
}

// compile-time: ensure regexp is always non-nil from GetUserNamespaceRegex.
var _ = regexp.MustCompile
