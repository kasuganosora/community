package main

import (
	"slices"
	"testing"
)

func TestExporterArgsSkipUnattachedDriveFiles(t *testing.T) {
	s := func(v string) *string { return &v }
	b := func(v bool) *bool { return &v }
	i, f := 1, 0.0
	d := direct{dsn: s("postgres://x"), baseURL: s("https://maid.lat"), idMethod: s("aidx"), version: s("v"), sslmode: s(""), objectMode: s(""), objectBaseURL: s(""),
		objectDir: s(""), sourceDir: s(""), mediaDir: s(""), includeRemote: b(false), batchSize: &i, maxRowsPerSec: &f, batchSleep: &f, skipUnattached: b(false), workDir: s(""), exporterDir: s(""), python: s("python3"), fetchOnly: b(false), finalSync: b(false), reuseSpool: b(false), keepSecretSpool: b(false)}
	if slices.Contains(exporterArgs(d, "misskey", "e.py", "st", "out"), "--skip-unattached-drive-files") {
		t.Fatal("drive-only files are migrated by default")
	}
	d.skipUnattached = b(true)
	if !slices.Contains(exporterArgs(d, "misskey", "e.py", "st", "out"), "--skip-unattached-drive-files") {
		t.Fatal("flag not passed to the exporter")
	}
}
