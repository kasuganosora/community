package datatransfer

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func sourceIssueFixture() []Issue {
	var in []Issue
	for i := 0; i < 60; i++ {
		in = append(in, Issue{Severity: "info", Code: "misskey_meta_asset", Kind: "media", SourceID: fmt.Sprintf("9a%03d", i),
			Message: "Ownerless local drive file not migrated: secret-in-message.",
			Details: map[string]any{"usage": "unreferenced", "file_name": "old\u202eemoji\x00.png", "size": float64(1234),
				"content_type": "image/png", "url": "https://bucket.example/private?sig=SECRET", "access_key": "SECRET"}})
	}
	return append(in,
		Issue{Severity: "info", Code: "object_storage_reference", Kind: "media", Message: "1274 files", Details: map[string]any{"count": float64(1274)}},
		Issue{Severity: "warning", Code: "custom_adapter_thing", Kind: "user_profile", SourceID: "u1", Message: "password hash SECRET", Details: map[string]any{"usage": "SECRET"}},
		Issue{Severity: "warning", Code: "custom_adapter_thing", Kind: "user_profile", SourceID: "u2", Message: "token SECRET"},
		Issue{Severity: "info", Code: "misskey_meta_asset", Kind: "media", SourceID: "bad id; DROP", Details: map[string]any{"usage": "not referenced by <script>", "content_type": "text/html; charset=SECRET"}},
	)
}

func TestSourceIssuesAllowlistAndAggregate(t *testing.T) {
	out := sourceIssues(sourceIssueFixture(), true)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET", "messages, unknown codes and non-allowlisted details never reach the report")
	require.NotContains(t, string(raw), "custom_adapter_thing")
	require.NotContains(t, string(raw), "secret-in-message")
	require.Len(t, out, 3, "one issue per code and kind: %s", raw)

	meta := out[0]
	require.Equal(t, "misskey_meta_asset", meta.Code)
	require.Equal(t, "info", meta.Severity, "info stays info")
	require.Equal(t, "source_export", meta.Kind)
	require.Equal(t, "media", meta.SourceKind)
	require.Equal(t, 61, meta.Count)
	require.Len(t, meta.SampleIDs, maxWidthSamples)
	require.Equal(t, "9a000", meta.SourceID)
	require.Len(t, meta.Samples, maxSourceIssueSamples)
	require.Equal(t, IssueSample{SourceID: "9a000", Details: map[string]any{"usage": "unreferenced",
		"file_name": "oldemoji.png", "size": int64(1234), "content_type": "image/png"}}, meta.Samples[0])
	require.Contains(t, meta.Message, "61 record(s); first 50 listed in samples")

	obj := out[1]
	require.Equal(t, "object_storage_reference", obj.Code)
	require.Equal(t, 1, obj.Count)
	require.Equal(t, map[string]any{"count": int64(1274)}, obj.Samples[0].Details)

	unknown := out[2]
	require.Equal(t, SourceIssueCode, unknown.Code)
	require.Equal(t, "warning", unknown.Severity)
	require.Equal(t, 2, unknown.Count)
	require.Empty(t, unknown.SourceID)
	require.Empty(t, unknown.SourceKind)
	require.Empty(t, unknown.Samples)
}

func TestSourceIssuesPersonalUploadsKeepNoIDsOrDetails(t *testing.T) {
	for _, is := range sourceIssues(sourceIssueFixture(), false) {
		require.Empty(t, is.SourceID)
		require.Empty(t, is.SampleIDs)
		require.Empty(t, is.Samples)
		require.Empty(t, is.SourceKind)
	}
}

// A streamed import reports each exporter issue code once, with its total,
// in both preview and apply, and blocks on source errors as before.
func TestStreamedImportAggregatesSourceIssues(t *testing.T) {
	b := streamFixture(t, 3, 4)
	b.Issues = sourceIssueFixture()
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	sp := spoolBundle(t, b)
	for _, apply := range []bool{false, true} {
		report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2, Apply: apply})
		require.NoError(t, err, "%+v", report)
		require.True(t, report.CanExecute)
		byCode := map[string][]Issue{}
		for _, is := range report.Issues {
			if is.Kind == "source_export" {
				byCode[is.Code] = append(byCode[is.Code], is)
			}
		}
		require.Len(t, byCode["misskey_meta_asset"], 1, "apply=%v", apply)
		require.Equal(t, 61, byCode["misskey_meta_asset"][0].Count)
		require.Len(t, byCode["object_storage_reference"], 1)
		require.Len(t, byCode[SourceIssueCode], 1)
		raw, _ := json.Marshal(report)
		require.NotContains(t, string(raw), "SECRET")
	}
	b.Issues = append(b.Issues, Issue{Severity: "error", Code: "media_bytes_unavailable", Kind: "media", SourceID: "m1"})
	report, err := ImportSpool(context.Background(), importerDB(t), spoolBundle(t, b), o, StreamOptions{ChunkSize: 2})
	require.NoError(t, err)
	require.False(t, report.CanExecute)
}
