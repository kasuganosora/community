package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasuganosora/community/cmd/community-transfer/internal/helpgate"
)

// buildTransfer compiles community-transfer so the tests exercise the real
// package initialization order of the binary.
func buildTransfer(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the community-transfer binary")
	}
	bin := filepath.Join(t.TempDir(), "community-transfer")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// minimalEnv has no DB_*/REDIS_* variables; HOME and Go caches are kept so
// nothing else changes.
func minimalEnv(extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	return append(env, extra...)
}

func TestHelpDoesNotTouchDatabase(t *testing.T) {
	bin := buildTransfer(t)
	dir := t.TempDir()
	// A .env that points at an unreachable database and Redis: if any of it
	// were loaded and used, connecting would fail and be logged.
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_PORT=1\nDB_DATABASE=x\nDB_USERNAME=x\nDB_PASSWORD=x\nREDIS_0_HOST=127.0.0.1\nREDIS_0_PORT=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--help"}, {"-h"}, {"-help"}, {"help"}, {"--scope", "personal", "--help"}} {
		for _, env := range [][]string{minimalEnv(), minimalEnv("ENV_FILE=" + envFile)} {
			cmd := exec.Command(bin, args...)
			cmd.Dir = dir
			cmd.Env = env
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v: %v\nstderr: %s", args, err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("%v: unexpected stderr (database/config touched?): %s", args, stderr.String())
			}
			if !strings.HasPrefix(stdout.String(), "Usage of community-transfer:\n") || !strings.Contains(stdout.String(), "-source-dsn string") {
				t.Fatalf("%v: help output = %q", args, stdout.String())
			}
		}
	}
}

// TestHelpUsageMatchesFlags keeps helpgate's static text identical to what
// the flag package prints for the flags main actually registers.
func TestHelpUsageMatchesFlags(t *testing.T) {
	bin := buildTransfer(t)
	cmd := exec.Command(bin, "-help-usage-sync-probe")
	cmd.Dir = t.TempDir()
	cmd.Env = minimalEnv("ENV_FILE=" + os.DevNull)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("unknown flag: err = %v, stderr: %s", err, stderr.String())
	}
	got := stderr.String()
	i := strings.Index(got, "Usage of ")
	if i < 0 {
		t.Fatalf("no usage in %q", got)
	}
	got = got[i:]
	got = got[strings.Index(got, "\n")+1:]
	if os.Getenv("UPDATE_USAGE") == "1" {
		if err := os.WriteFile(filepath.Join("internal", "helpgate", "usage.txt"), []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got != helpgate.Usage {
		t.Fatalf("internal/helpgate/usage.txt is stale; run UPDATE_USAGE=1 go test ./cmd/community-transfer -run TestHelpUsageMatchesFlags")
	}
}
