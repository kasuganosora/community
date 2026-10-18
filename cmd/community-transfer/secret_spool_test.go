package main

import (
	"os"
	"path/filepath"
	"testing"
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
