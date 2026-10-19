package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kasuganosora/community/datatransfer"
)

func TestFinishSecretSpoolWipesUnlessKept(t *testing.T) {
	work := t.TempDir()
	write := func() {
		_ = os.MkdirAll(filepath.Join(work, "state"), 0o700)
		_ = os.WriteFile(filepath.Join(work, "spool.sqlite3"), []byte("twoFactorSecret"), 0o600)
		_ = os.WriteFile(filepath.Join(work, "state", "staging.sqlite3"), []byte("twoFactorSecret"), 0o600)
	}
	write()
	finishSecretSpool(work, true)
	if _, err := os.Stat(filepath.Join(work, "spool.sqlite3")); err != nil {
		t.Fatal("--keep-secret-spool must keep the spool")
	}
	finishSecretSpool(work, false)
	for _, p := range []string{"spool.sqlite3", "state"} {
		if _, err := os.Stat(filepath.Join(work, p)); !os.IsNotExist(err) {
			t.Fatalf("%s not wiped", p)
		}
	}
}

func TestScrubFailedRunWipesExporterPlaintextKeepsSpool(t *testing.T) {
	for _, complete := range []bool{true, false} {
		work := t.TempDir()
		sp, err := datatransfer.OpenSpool(filepath.Join(work, "spool.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		if complete {
			_ = sp.SetMeta("complete", "1")
		}
		_ = sp.Close()
		_ = os.MkdirAll(filepath.Join(work, "state"), 0o700)
		_ = os.WriteFile(filepath.Join(work, "state", "staging.sqlite3"), []byte("twoFactorSecret"), 0o600)
		_ = os.MkdirAll(filepath.Join(work, "out-1", ".spool"), 0o700)
		_ = os.WriteFile(filepath.Join(work, "out-1", ".spool", "credentials"), []byte("twoFactorSecret"), 0o600)
		_ = os.WriteFile(filepath.Join(work, "out-1", "media.bin"), []byte("media"), 0o600)
		scrubFailedRun(work)
		if _, err := os.Stat(filepath.Join(work, "out-1", ".spool")); !os.IsNotExist(err) {
			t.Fatal("exporter section spool must be wiped on failure")
		}
		if _, err := os.Stat(filepath.Join(work, "spool.sqlite3")); err != nil {
			t.Fatal("sealed spool must stay for --reuse-spool")
		}
		if _, err := os.Stat(filepath.Join(work, "out-1", "media.bin")); err != nil {
			t.Fatal("copied media must stay")
		}
		_, err = os.Stat(filepath.Join(work, "state"))
		if complete && !os.IsNotExist(err) {
			t.Fatal("state must be wiped once the spool is complete")
		}
		if !complete && err != nil {
			t.Fatal("state is the resume checkpoint while the read is incomplete")
		}
	}
}
