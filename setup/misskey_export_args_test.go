package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMisskeyExportArgsSkipSourceDirForObjectStorage(t *testing.T) {
	base := MisskeyParams{
		IDMethod: "aidx", MediaSource: "s3", MediaMode: "reference",
		FilesPath: "/misskey/files", ObjectStorage: ObjectStorageParams{PublicURL: "https://files.example"},
	}
	ref := misskeyExportArgs(base, "https://maid.lat", "/tmp/out", "/tmp/state")
	require.NotContains(t, ref, "--source-dir")
	require.NotContains(t, ref, "/misskey/files")
	require.Contains(t, ref, "reference")

	base.MediaMode = "copy"
	copyArgs := misskeyExportArgs(base, "https://maid.lat", "/tmp/out", "/tmp/state")
	require.NotContains(t, copyArgs, "--source-dir")
	require.Contains(t, copyArgs, "--object-storage-dir")

	local := misskeyExportArgs(MisskeyParams{IDMethod: "aidx", MediaSource: "local", FilesPath: "/data/files"}, "https://maid.lat", "/tmp/out", "/tmp/state")
	require.Contains(t, local, "--source-dir")
	require.Contains(t, local, "/data/files")
}

func TestNormalizeDropsS3FilesPath(t *testing.T) {
	m := MisskeyParams{
		Host: "db.internal", Port: 5432, Name: "misskey", User: "misskey", SSLMode: "disable", IDMethod: "aidx",
		MediaSource: "s3", MediaMode: "reference", FilesPath: "/misskey/files",
		ObjectStorage: ObjectStorageParams{PublicURL: "https://files.example/media"},
	}
	require.Nil(t, m.normalize())
	require.Empty(t, m.FilesPath)

	mastodon := MastodonParams{
		Host: "db.internal", Port: 5432, Name: "mastodon", User: "mastodon", SSLMode: "disable",
		MediaSource: "s3", MediaMode: "reference", FilesPath: "/mastodon/public/system",
		ObjectStorage: ObjectStorageParams{PublicURL: "https://files.example/media"},
	}
	require.Nil(t, mastodon.normalize())
	require.Empty(t, mastodon.FilesPath)
}

func TestDiscardBlockedExportRemovesState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SETUP_WORK_DIR", dir)
	require.NoError(t, os.MkdirAll(exportDir(), 0o700))
	require.NoError(t, os.MkdirAll(exportStateDir(), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(exportDir(), "bundle.json"), []byte("{}"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(exportStateDir(), "staging.sqlite3"), []byte("db"), 0o600))
	require.NoError(t, os.WriteFile(spoolPath(), []byte("spool"), 0o600))

	discardBlockedExportFiles()

	_, err := os.Stat(exportDir())
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(exportStateDir())
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(spoolPath())
	require.True(t, os.IsNotExist(err))
}
