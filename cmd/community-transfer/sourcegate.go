package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/datatransfer/sourcecheck"
)

// exitSourceRunning is the exit code when the source is not stopped.
const exitSourceRunning = 3

// runSourceCheck is replaceable in tests.
var runSourceCheck = sourceCheck

func sourceCheck(ctx context.Context, d direct, source string) (*sourcecheck.Result, error) {
	table := map[string]string{"misskey": "note", "mastodon": "statuses"}[source]
	if table == "" {
		return nil, errors.New("the source-stopped check needs --source misskey or --source mastodon")
	}
	db, err := sourcecheck.OpenPG(withSSLMode(*d.dsn, *d.sslmode))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rc, err := sourcecheck.OpenRedis(*d.redisURL)
	if err != nil {
		return nil, err
	}
	if rc != nil {
		defer rc.Close()
	}
	var queues []string
	for _, q := range strings.Split(*d.queues, ",") {
		if q = strings.TrimSpace(q); q != "" {
			queues = append(queues, q)
		}
	}
	fmt.Fprintf(os.Stderr, "checking that the source is stopped (write sample interval %s)…\n", *d.checkInterval)
	return sourcecheck.Run(ctx, sourcecheck.Config{
		Source: sourcecheck.NewPGSource(db, table), Interval: *d.checkInterval, WebURL: *d.webURL,
		Redis: rc, Prefix: *d.redisPrefix, Queues: queues,
	}), nil
}

// sourceGate blocks --final-sync while the source still runs. --fetch-only
// may run against a live source and is not checked; plain direct runs
// (no --final-sync) only get a reminder so existing rehearsal commands keep
// working. Returns a non-zero exit code to stop.
func sourceGate(ctx context.Context, d direct, source string) int {
	if *d.fetchOnly {
		fmt.Fprintln(os.Stderr, "info: --fetch-only may run while the source is live; the source-stopped check runs with --final-sync.")
		return 0
	}
	if !*d.finalSync || *d.reuseSpool {
		return 0
	}
	res, err := runSourceCheck(ctx, d, source)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprint(os.Stderr, sourcecheck.Format(res))
	// Records go to --work-dir (created 0700 if missing). Without one the
	// result is only printed; the run then stops at the usual validation.
	recordDir := ""
	if d.workDir != nil && *d.workDir != "" {
		if err := datatransfer.EnsurePrivateDir(*d.workDir); err == nil {
			recordDir = *d.workDir
		} else {
			fmt.Fprintln(os.Stderr, "WARN work directory is not writable; the source check result is not recorded")
		}
	}
	if recordDir != "" {
		writeJSONFile(filepath.Join(recordDir, "source-check.json"), res)
	}
	if res.Status == sourcecheck.Stopped {
		return 0
	}
	if !*d.force {
		fmt.Fprintf(os.Stderr, "ERROR: the source is still RUNNING (failed checks: %s). Stop Misskey web and worker, confirm with --check-source-stopped, then rerun --final-sync. Use --force only for rehearsals on a copy.\n", strings.Join(res.Failed(), ", "))
		return exitSourceRunning
	}
	o := sourcecheck.NewOverride(res, sourcecheck.Operator(), "cli")
	fmt.Fprintln(os.Stderr, "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Fprintln(os.Stderr, o.LogLine())
	fmt.Fprintln(os.Stderr, "--force: final sync against a source that is NOT stopped; writes made after the read are LOST. Rehearsal/copy use only.")
	fmt.Fprintln(os.Stderr, "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	if recordDir == "" {
		fmt.Fprintln(os.Stderr, "WARN no writable --work-dir: the override is only logged above")
	} else if f, err := os.OpenFile(filepath.Join(recordDir, "source-check-overrides.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_ = json.NewEncoder(f).Encode(o)
		_ = f.Close()
	} else {
		fmt.Fprintln(os.Stderr, "WARN could not record the override in the work directory")
	}
	return 0
}

func writeJSONFile(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}
