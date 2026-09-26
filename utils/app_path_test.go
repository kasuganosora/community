package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveAppPath_Absolute(t *testing.T) {
	assert.Equal(t, "/etc/community/.env", ResolveAppPath("/etc/community/.env"))
}

func TestResolveAppPath_PrefersAppRootThenWorkingDir(t *testing.T) {
	root := t.TempDir()
	wd := t.TempDir()
	t.Chdir(wd)

	// Nothing exists yet: falls back to $APP_ROOT.
	t.Setenv("APP_ROOT", root)
	assert.Equal(t, filepath.Join(root, "nope.env"), ResolveAppPath("nope.env"))

	// Present only in the working directory.
	assert.NoError(t, os.WriteFile(filepath.Join(wd, "only-wd.env"), []byte("X=1"), 0o600))
	got, _ := filepath.EvalSymlinks(ResolveAppPath("only-wd.env"))
	want, _ := filepath.EvalSymlinks(filepath.Join(wd, "only-wd.env"))
	assert.Equal(t, want, got)

	// Present in both: $APP_ROOT wins.
	assert.NoError(t, os.WriteFile(filepath.Join(root, "only-wd.env"), []byte("X=2"), 0o600))
	assert.Equal(t, filepath.Join(root, "only-wd.env"), ResolveAppPath("only-wd.env"))
}

func TestResolveAppPath_FallsBackToSourceDir(t *testing.T) {
	t.Setenv("APP_ROOT", "")
	t.Chdir(t.TempDir())

	// go.mod only exists in the source tree.
	assert.Equal(t, filepath.Join(GetSourceDir(), "go.mod"), ResolveAppPath("go.mod"))
}
