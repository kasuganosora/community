package datatransfer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func zipped(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range names {
		w, err := z.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(`{"hello":"world"}`))
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	return b.Bytes()
}
func TestArchiveDataAndCleanup(t *testing.T) {
	a, err := OpenArchive(context.Background(), "backup.zip", bytes.NewReader(zipped(t, "actor.json", "media/test.bin")), Limits{})
	require.NoError(t, err)
	require.Len(t, a.Files, 2)
	b, err := a.Read("actor.json", 100)
	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"world"}`, string(b))
	p := a.Files["actor.json"].Path
	info, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.NoError(t, a.Close())
	_, err = os.Stat(p)
	require.True(t, os.IsNotExist(err))
}
func TestArchiveRejectsTraversalAmbiguityAndDuplicates(t *testing.T) {
	for _, name := range []string{"../outside", "/etc/passwd", "a/../b", "a//b", `a\b`, "C:/outside", "./actor.json"} {
		t.Run(name, func(t *testing.T) {
			a, err := OpenArchive(context.Background(), "archive.zip", bytes.NewReader(zipped(t, name)), Limits{})
			require.Error(t, err)
			require.Nil(t, a)
		})
	}
	a, err := OpenArchive(context.Background(), "archive.zip", bytes.NewReader(zipped(t, "actor.json", "actor.json")), Limits{})
	require.Error(t, err)
	require.Nil(t, a)
}
func TestArchiveRejectsLinksAndExpansionLimits(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink, Mode: 0600}))
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	_, err := OpenArchive(context.Background(), "backup.tar.gz", &b, Limits{})
	require.Error(t, err)
	data := zipped(t, "one.json", "two.json")
	for _, l := range []Limits{{MaxFiles: 1}, {MaxFileBytes: 5}, {MaxExpandedBytes: 20}, {MaxCompressedBytes: 10}} {
		_, err = OpenArchive(context.Background(), "backup.zip", bytes.NewReader(data), l)
		require.Error(t, err)
	}
}
func TestArchiveCancelledReadCleansPrivateTemporaryDirectory(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenArchive(ctx, "notes.json", bytes.NewBufferString("[]"), Limits{TempDir: root})
	require.ErrorIs(t, err, context.Canceled)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestDirectorySnapshotCannotDeleteSourceOrFollowSymlinks(t *testing.T) {
	source := t.TempDir()
	require.NoError(t, os.WriteFile(source+"/bundle.json", []byte(`{"schema":"community-transfer/v1","scope":"site"}`), 0600))
	a, e := OpenDirectory(context.Background(), source, Limits{})
	require.NoError(t, e)
	require.NotEqual(t, source, a.Root())
	require.NoError(t, os.WriteFile(source+"/bundle.json", []byte("changed"), 0600))
	data, e := a.Read("bundle.json", 1000)
	require.NoError(t, e)
	require.Contains(t, string(data), "community-transfer/v1")
	require.NoError(t, a.Close())
	_, e = os.Stat(source + "/bundle.json")
	require.NoError(t, e)
	require.NoError(t, os.Symlink("/etc/passwd", source+"/linked"))
	a, e = OpenDirectory(context.Background(), source, Limits{})
	require.Error(t, e)
	require.Nil(t, a)
}
