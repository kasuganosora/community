package internalfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withDevMode temporarily sets config.C.DevMode for the duration of t and
// restores the previous value when the test ends.
func withDevMode(t *testing.T, mode string) {
	t.Helper()

	original := config.C.DevMode
	config.C.DevMode = mode
	t.Cleanup(func() { config.C.DevMode = original })
}

func TestGet_DevMode(t *testing.T) {
	withDevMode(t, "true")

	result := Get()
	assert.NotNil(t, result)

	// In dev mode the backing FS is an os.DirFS rooted at resources/.
	_, ok := result.(fs.ReadDirFS)
	assert.True(t, ok, "dev-mode FS should support ReadDir (os.DirFS does)")
}

func TestGet_ProdMode(t *testing.T) {
	withDevMode(t, "false")

	result := Get()
	require.NotNil(t, result)

	// In production the backing FS is the embedded (sub-rooted) resources.
	// Assert it behaves as a read-only FS exposing a known bundled file.
	f, err := result.Open("i18n/en.toml")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
}

func TestGet_DefaultModeIsProd(t *testing.T) {
	// Any value other than "true" must fall through to the embedded FS so that
	// a misconfigured/empty DevMode never accidentally serves from disk.
	for _, mode := range []string{"", "false", "0", "anything"} {
		t.Run(mode, func(t *testing.T) {
			withDevMode(t, mode)
			result := Get()
			require.NotNil(t, result)
			f, err := result.Open("i18n/en.toml")
			require.NoError(t, err, "non-\"true\" DevMode must yield embedded FS")
			_ = f.Close()
		})
	}
}

func TestGet_ReturnsFS(t *testing.T) {
	result := Get()
	assert.NotNil(t, result)
}

// TestEmbeddedResourcesPresent guarantees that the go:embed directive actually
// captured the bundled resources. If the embed path is ever broken (e.g. the
// resources directory is moved), this test fails loudly rather than silently
// shipping a binary with an empty filesystem.
func TestEmbeddedResourcesPresent(t *testing.T) {
	withDevMode(t, "false")

	fsys := Get()

	// Every expected bundled file must exist in the embedded FS.
	expected := []string{
		"i18n/en.toml",
		"mails/welcome_mail_en.html",
		"mails/verify_email_en.html",
		"mails/recover_password_verify_email_en.html",
		"oauth_authorize.html",
	}

	for _, p := range expected {
		p := p
		t.Run(p, func(t *testing.T) {
			f, err := fsys.Open(p)
			require.NoError(t, err, "embedded file should exist: %s", p)
			defer func() { _ = f.Close() }()

			stat, err := f.Stat()
			require.NoError(t, err)
			assert.False(t, stat.IsDir())
		})
	}
}

func TestSecureJoin(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"simple", "mails/foo.html", "mails/foo.html", false},
		{"redundant separators and dot", "mails//./foo.html", "mails/foo.html", false},
		{"trailing slash", "mails/", "mails", false},
		{"dot stays inside", "mails/../mails/foo.html", "mails/foo.html", false},
		{"empty", "", "", true},
		{"absolute unix", "/etc/passwd", "", true},
		{"backslash absolute (windows)", `\etc\passwd`, "", true},
		{"parent traversal single", "..", "", true},
		{"parent traversal prefixed", "../etc/passwd", "", true},
		{"parent traversal embedded", "mails/../../etc/passwd", "", true},
		{"windows drive letter", "C:/Windows/System32/drivers/etc/hosts", "C:/Windows/System32/drivers/etc/hosts", false},
		{"backslash normalized", "mails\\..\\..\\etc\\passwd", "", true},
		{"dot is root", ".", "", true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := SecureJoin(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSafeOpen_PathTraversal asserts that path-traversal payloads cannot read
// files outside the bundled resources root, for both the embedded FS and the
// dev-mode disk FS.
func TestSafeOpen_PathTraversal(t *testing.T) {
	// Sanity check: /etc/passwd exists on the test host so we can prove an
	// escape would have leaked it.
	_, statErr := os.Stat("/etc/passwd")
	require.NoError(t, statErr, "/etc/passwd must exist for traversal test")

	attackVectors := []string{
		"../../../etc/passwd",
		"mails/../../../etc/passwd",
		"mails/../../etc/passwd",
		"../etc/passwd",
		"..",
		"/etc/passwd",
		"mails/../../../../../etc/passwd",
		"mails/..\\..\\..\\etc\\passwd",
	}

	openers := map[string]fs.FS{
		"embedded": func() fs.FS {
			withDevMode(t, "false")
			return Get()
		}(),
	}

	// Build a real on-disk dev-mode FS, mirroring how Get() behaves, so we can
	// also assert traversal protection for the os.DirFS backing implementation.
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "mails"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "mails", "real.html"), []byte("ok"), 0o600))
	openers["disk"] = os.DirFS(tmp)

	for fsName, fsys := range openers {
		fsys := fsys
		t.Run(fsName, func(t *testing.T) {
			for _, vec := range attackVectors {
				vec := vec
				t.Run(vec, func(t *testing.T) {
					f, err := SafeOpen(fsys, vec)
					if err == nil {
						// Must never succeed for a traversal payload.
						_ = f.Close()
						t.Fatalf("traversal payload %q was accepted by %s", vec, fsName)
					}
					assert.True(t, strings.Contains(err.Error(), "invalid") ||
						strings.Contains(err.Error(), "not exist"),
						"unexpected error for %q: %v", vec, err)
				})
			}
		})
	}
}

// TestSafeOpen_ValidPath proves a legitimate path still opens successfully on
// both backends.
func TestSafeOpen_ValidPath(t *testing.T) {
	t.Run("embedded", func(t *testing.T) {
		withDevMode(t, "false")
		fsys := Get()
		f, err := SafeOpen(fsys, "i18n/en.toml")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
	})

	t.Run("disk", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmp, "mails"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "mails", "x.html"), []byte("ok"), 0o600))

		fsys := os.DirFS(tmp)
		f, err := SafeOpen(fsys, "mails/x.html")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
	})
}
