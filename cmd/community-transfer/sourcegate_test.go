package main

import (
	"context"
	"os"
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
