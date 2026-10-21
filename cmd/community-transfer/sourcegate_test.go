package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasuganosora/community/datatransfer/sourcecheck"
)

func gateDirect(t *testing.T, fetchOnly, finalSync, force bool) direct {
	dir := t.TempDir()
	f, s, fo, rs := fetchOnly, finalSync, force, false
	return direct{workDir: &dir, fetchOnly: &f, finalSync: &s, force: &fo, reuseSpool: &rs}
}

func stubCheck(t *testing.T, status string) *int {
	calls := 0
	old := runSourceCheck
	runSourceCheck = func(context.Context, direct, string) (*sourcecheck.Result, error) {
		calls++
		r := &sourcecheck.Result{Status: status}
		if status == sourcecheck.Running {
			r.Checks = []sourcecheck.Check{{Name: sourcecheck.CheckSessions, Status: sourcecheck.Fail}}
		}
		return r, nil
	}
	t.Cleanup(func() { runSourceCheck = old })
	return &calls
}

func TestSourceGate(t *testing.T) {
	calls := stubCheck(t, sourcecheck.Running)
	if code := sourceGate(context.Background(), gateDirect(t, true, false, false), "misskey"); code != 0 || *calls != 0 {
		t.Fatalf("--fetch-only must not be blocked or checked: code=%d calls=%d", code, *calls)
	}
	if code := sourceGate(context.Background(), gateDirect(t, false, false, false), "misskey"); code != 0 || *calls != 0 {
		t.Fatalf("plain direct runs keep working: code=%d", code)
	}
	if code := sourceGate(context.Background(), gateDirect(t, false, true, false), "misskey"); code != exitSourceRunning {
		t.Fatalf("--final-sync against a running source must be refused, got %d", code)
	}
	d := gateDirect(t, false, true, true)
	if code := sourceGate(context.Background(), d, "misskey"); code != 0 {
		t.Fatalf("--force must override, got %d", code)
	}
	data, err := os.ReadFile(filepath.Join(*d.workDir, "source-check-overrides.jsonl"))
	if err != nil || !strings.Contains(string(data), `"failed_checks":["sessions"]`) {
		t.Fatalf("override not recorded: %v %s", err, data)
	}
}

func TestSourceGateStopped(t *testing.T) {
	stubCheck(t, sourcecheck.Stopped)
	d := gateDirect(t, false, true, false)
	if code := sourceGate(context.Background(), d, "misskey"); code != 0 {
		t.Fatalf("stopped source must pass, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(*d.workDir, "source-check.json")); err != nil {
		t.Fatal("last check result not recorded")
	}
}

// TestFinalSyncHelper is re-executed as a subprocess by
// TestFinalSyncChecksSourceBeforeTargetConfig.
func TestFinalSyncHelper(t *testing.T) {
	status := os.Getenv("CT_FAKE_SOURCE_STATUS")
	if status == "" {
		t.Skip("helper process only")
	}
	runSourceCheck = func(context.Context, direct, string) (*sourcecheck.Result, error) {
		r := &sourcecheck.Result{Status: status}
		if status == sourcecheck.Running {
			r.Checks = []sourcecheck.Check{{Name: sourcecheck.CheckSessions, Title: sourcecheck.Titles[sourcecheck.CheckSessions], Status: sourcecheck.Fail, Reasons: []string{"pid=42 user=misskey"}}}
		}
		return r, nil
	}
	os.Args = append([]string{"community-transfer"}, strings.Fields(os.Getenv("CT_ARGS"))...)
	os.Exit(guardedRun(run))
}

func TestFinalSyncChecksSourceBeforeTargetConfig(t *testing.T) {
	dir := t.TempDir()
	exporters := filepath.Join(dir, "exporters")
	if err := os.MkdirAll(exporters, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exporters, "export_misskey.py"), []byte("raise SystemExit(1)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	args := "--source misskey --source-dsn host=127.0.0.1 --source-base-url https://maid.example --exporter-dir " + exporters + " --work-dir " + work + " --final-sync --apply"
	runHelper := func(status string) (int, string) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFinalSyncHelper$")
		cmd.Env = append(os.Environ(), "CT_FAKE_SOURCE_STATUS="+status, "CT_ARGS="+args,
			"ENV_FILE="+filepath.Join(dir, "missing.env"), "DB_CONNECTION=", "DB_HOST=", "DB_DATABASE=", "DB_USERNAME=", "DB_PASSWORD=", "DB_DSN=")
		out, _ := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out)
	}

	code, out := runHelper(sourcecheck.Running)
	if code != exitSourceRunning {
		t.Fatalf("running source without target config: exit %d, want %d\n%s", code, exitSourceRunning, out)
	}
	for _, want := range []string{"source check: RUNNING", "No other connections to the Misskey DB", "pid=42 user=misskey", "the source is still RUNNING"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "source-check.json")); err != nil {
		t.Fatalf("result not recorded in the (created) work dir: %v", err)
	}

	code, out = runHelper(sourcecheck.Stopped)
	if code != 1 {
		t.Fatalf("stopped source without target config: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "source check: STOPPED") || !strings.Contains(out, "No database is configured") {
		t.Fatalf("want the check to pass and then the usual target config error:\n%s", out)
	}
}
