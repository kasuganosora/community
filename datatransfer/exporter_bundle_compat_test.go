package datatransfer

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExporterBundleCompat validates a bundle produced by
// scripts/migration/export_misskey.py or export_mastodon_db.py (local-only or --include-remote-cache):
// it must parse, and preflight must report no dangling references.
// Set EXPORT_BUNDLE_DIR to the exporter's --output directory.
func TestExporterBundleCompat(t *testing.T) {
	dir := os.Getenv("EXPORT_BUNDLE_DIR")
	if dir == "" {
		t.Skip("EXPORT_BUNDLE_DIR not set")
	}
	limits := Limits{MaxCompressedBytes: 1 << 40, MaxExpandedBytes: 1 << 40, MaxFileBytes: 16 << 30, MaxJSONBytes: 16 << 30, MaxFiles: 10000000, MaxRecords: 100000000}
	a, err := OpenDirectory(context.Background(), dir, limits)
	require.NoError(t, err)
	defer a.Close()
	b, err := ParseArchive(context.Background(), a, ParseOptions{Platform: "auto"})
	require.NoError(t, err)
	conn := importerDB(t)
	r, err := Preview(context.Background(), conn, b, Options{Scope: "site", TargetBaseURL: b.Source.BaseURL, StorageDir: t.TempDir(), ArchiveDir: dir, Files: a.Files})
	require.NoError(t, err)
	codes := map[string]int{}
	for _, i := range r.Issues {
		codes[i.Code]++
		if i.Code == "missing_reference" {
			t.Errorf("dangling reference: %s %s", i.Kind, i.SourceID)
		}
	}
	t.Logf("accounts=%d posts=%d media=%d issues=%v", len(b.Accounts), len(b.Posts), len(b.Media), codes)
}
