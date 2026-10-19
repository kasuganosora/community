package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/migrate"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type direct struct {
	dsn, baseURL, idMethod, version, workDir, sslmode, exporterDir                   *string
	objectMode, objectBaseURL, objectDir, sourceDir, mediaDir, python                *string
	fetchOnly, finalSync, includeRemote, reuseSpool, keepSecretSpool, skipUnattached *bool
	batchSize                                                                        *int
	maxRowsPerSec, batchSleep                                                        *float64
}

func directFlags() direct {
	return direct{
		dsn:            flag.String("source-dsn", "", "direct mode: libpq connection string of the source Misskey/Mastodon PostgreSQL (read-only role; no password, use PGPASSFILE)"),
		baseURL:        flag.String("source-base-url", "", "direct mode: public origin of the source server, e.g. https://maid.lat"),
		idMethod:       flag.String("id-method", "aidx", "direct mode (misskey): config.id of the source; mixed historical formats are detected per ID"),
		version:        flag.String("source-version", "unknown", "direct mode: source software version"),
		workDir:        flag.String("work-dir", "", "direct mode: private directory for fetch state, spool and copied media (reuse it to resume and for --final-sync)"),
		sslmode:        flag.String("sslmode", "", "direct mode: libpq sslmode added to --source-dsn when it has none (disable, require, verify-full, ...)"),
		exporterDir:    flag.String("exporter-dir", "", "direct mode: directory containing export_misskey.py / export_mastodon_db.py (default: ./scripts/migration or next to the binary)"),
		python:         flag.String("python", "python3", "direct mode: Python 3 interpreter for the source reader"),
		objectMode:     flag.String("object-storage-mode", "", "direct mode: error, copy or reference for media in object storage"),
		objectBaseURL:  flag.String("object-base-url", "", "direct mode: public URL prefix of the source bucket"),
		skipUnattached: flag.Bool("skip-unattached-drive-files", false, "direct mode (misskey): do not migrate local drive files that no note, avatar, banner or chat message references (default: import them as unattached media of their owner)"),
		objectDir:      flag.String("object-storage-dir", "", "direct mode: local mirror of the source bucket (copy mode)"),
		sourceDir:      flag.String("source-files-dir", "", "direct mode (misskey): local drive files directory snapshot"),
		mediaDir:       flag.String("media-dir", "", "direct mode (mastodon): public/system directory"),
		fetchOnly:      flag.Bool("fetch-only", false, "direct mode: bulk read into --work-dir and stop; the source may stay live"),
		finalSync:      flag.Bool("final-sync", false, "direct mode: after source writes are stopped, read new/updated/deleted rows, then import"),
		includeRemote:  flag.Bool("include-remote-cache", false, "direct mode: also migrate the remote-content cache (default: local data and what it references)"),
		reuseSpool:     flag.Bool("reuse-spool", false, "direct mode: skip reading the source and import the existing spool again (resume an interrupted import)"),
		// keepSecretSpool is registered in run() (shared with the streamed bundle path).
		batchSize:     flag.Int("batch-size", 5000, "direct mode: rows per keyset page / read-only source transaction"),
		maxRowsPerSec: flag.Float64("max-rows-per-sec", 0, "direct mode: throttle source reads (0 = unlimited)"),
		batchSleep:    flag.Float64("batch-sleep", 0, "direct mode: seconds to sleep after every source page"),
	}
}

func exporterScript(d direct, source string) (string, error) {
	name := map[string]string{"misskey": "export_misskey.py", "mastodon": "export_mastodon_db.py"}[source]
	if name == "" {
		return "", errors.New("direct mode needs --source misskey or --source mastodon")
	}
	var dirs []string
	if *d.exporterDir != "" {
		dirs = []string{*d.exporterDir}
	} else {
		dirs = []string{filepath.Join("scripts", "migration")}
		if exe, err := os.Executable(); err == nil {
			dirs = append(dirs, filepath.Join(filepath.Dir(exe), "scripts", "migration"), filepath.Join(filepath.Dir(exe), "..", "scripts", "migration"))
		}
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return filepath.Abs(p)
		}
	}
	return "", fmt.Errorf("%s not found; pass --exporter-dir", name)
}

func withSSLMode(dsn, mode string) string {
	if mode == "" || strings.Contains(dsn, "sslmode") {
		return dsn
	}
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "sslmode=" + mode
	}
	return strings.TrimSpace(dsn) + " sslmode=" + mode
}

// exporterArgs builds the read-only source reader command line.
func exporterArgs(d direct, source, script, state, out string) []string {
	args := []string{script, "--base-url", *d.baseURL, "--output", out, "--state-dir", state,
		"--dsn", withSSLMode(*d.dsn, *d.sslmode), "--batch-size", strconv.Itoa(*d.batchSize),
		"--max-rows-per-sec", strconv.FormatFloat(*d.maxRowsPerSec, 'f', -1, 64), "--batch-sleep", strconv.FormatFloat(*d.batchSleep, 'f', -1, 64)}
	if source == "misskey" {
		args = append(args, "--id-method", *d.idMethod, "--source-version", *d.version)
		if *d.sourceDir != "" {
			args = append(args, "--source-dir", *d.sourceDir)
		}
		mode := *d.objectMode
		if mode == "" {
			mode = "error"
		}
		args = append(args, "--object-storage-mode", mode)
		if d.skipUnattached != nil && *d.skipUnattached {
			args = append(args, "--skip-unattached-drive-files")
		}
	} else {
		if *d.mediaDir != "" {
			args = append(args, "--media-dir", *d.mediaDir)
		}
		if *d.objectMode != "" {
			args = append(args, "--object-storage-mode", *d.objectMode)
		}
	}
	if *d.objectBaseURL != "" {
		args = append(args, "--object-base-url", *d.objectBaseURL)
	}
	if *d.objectDir != "" {
		args = append(args, "--object-storage-dir", *d.objectDir)
	}
	if *d.includeRemote {
		args = append(args, "--include-remote-cache")
	}
	if *d.fetchOnly {
		return append(args, "--fetch-only")
	}
	if *d.finalSync {
		args = append(args, "--final-sync")
	}
	return append(args, "--emit", "ndjson")
}

// runDirect reads the source database read-only through the keyset-paged
// reader (state directory = staging, resumable) and streams its records
// straight into the spool and then the Community database in chunks; no
// bundle.json is written or loaded.
func runDirect(d direct, source string, apply bool, reportPath string, chunkSize, maxFiles int, maxBytes int64) int {
	code := runDirectOnce(d, source, apply, reportPath, chunkSize, maxFiles, maxBytes)
	if code != 0 && *d.workDir != "" && !*d.fetchOnly && !*d.keepSecretSpool {
		scrubFailedRun(*d.workDir)
	}
	return code
}

// scrubFailedRun runs after any failed or interrupted direct run (preflight
// errors, import errors, signals). The spool keeps its credential records
// sealed (see datatransfer.SpoolSealingAvailable) so --reuse-spool can resume;
// the exporter's plaintext intermediates are wiped. The fetch state is wiped
// only once the spool is complete: until then it is the resume checkpoint of
// the source read and is kept (0700) until the read finishes.
func scrubFailedRun(workDir string) {
	complete := false
	spoolPath := filepath.Join(workDir, "spool.sqlite3")
	if _, err := os.Stat(spoolPath); err == nil {
		// Opening re-seals any plaintext credential rows an older run left.
		if sp, err := datatransfer.OpenSpool(spoolPath); err == nil {
			complete = sp.Meta("complete") == "1"
			_ = sp.Close()
		}
	}
	wiped, err := datatransfer.WipeExporterSecrets(workDir, complete)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not wipe every plaintext exporter file in %s (%s); remove out-*/.spool, out-*/bundle.json* and (if the spool is complete) state/ by hand.\n", workDir, safeReason(err))
	} else if len(wiped) > 0 {
		fmt.Fprintf(os.Stderr, "wiped %d plaintext exporter path(s) in %s after the failed run; rerun with --reuse-spool to resume from the sealed spool\n", len(wiped), workDir)
	}
	if !complete {
		fmt.Fprintf(os.Stderr, "NOTE: the source read did not finish; %s/state still holds plaintext source rows (including TOTP secrets) as the resume checkpoint. Rerun to finish, or wipe the work directory to abandon.\n", workDir)
	}
	if !datatransfer.SpoolSealingAvailable() {
		fmt.Fprintf(os.Stderr, "WARNING: ENCRYPTED_KEY is not set, so %s holds credential records (including TOTP secrets) in plaintext. Wipe it when done.\n", spoolPath)
	}
}

func runDirectOnce(d direct, source string, apply bool, reportPath string, chunkSize, maxFiles int, maxBytes int64) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *d.workDir == "" || *d.baseURL == "" || (*d.fetchOnly && *d.finalSync) || *d.batchSize <= 0 || *d.maxRowsPerSec < 0 || *d.batchSleep < 0 {
		fmt.Fprintln(os.Stderr, "Direct mode needs --source misskey|mastodon, --source-dsn, --source-base-url and --work-dir (--fetch-only and --final-sync are exclusive).")
		return 1
	}
	script, err := exporterScript(d, source)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := datatransfer.EnsurePrivateDir(*d.workDir); err != nil {
		fmt.Fprintln(os.Stderr, "Work directory is not writable.")
		return 1
	}
	// The exporter runs with umask 077; tighten anything an older run left.
	_ = datatransfer.TightenTree(*d.workDir)
	state := filepath.Join(*d.workDir, "state")
	spoolPath := filepath.Join(*d.workDir, "spool.sqlite3")
	// Seal plaintext secrets an older exporter left in the fetch state; the
	// exporter seals new rows itself with the key passed in its environment.
	if n, err := datatransfer.SealExporterState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Fetch state could not be sealed (%s).\n", safeReason(err))
		return 1
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "sealed secret fields of %d plaintext row(s) in %s\n", n, state)
	}
	var reportFile *os.File
	if reportPath != "" && !*d.fetchOnly {
		if reportFile, err = os.OpenFile(reportPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "Report path must be a new writable private file.")
			return 1
		}
		defer reportFile.Close()
	}
	if *d.fetchOnly {
		out := filepath.Join(*d.workDir, "out-fetch")
		cmd := exec.CommandContext(ctx, *d.python, exporterArgs(d, source, script, state, out)...)
		cmd.Env = exporterEnv()
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		err := cmd.Run()
		_ = datatransfer.TightenTree(*d.workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Source read failed (%v); rerun the same command to resume from the checkpoint.\n", exitReason(err))
			return 1
		}
		return 0
	}
	database.RequireDB()
	conn := db.GetDB().Session(&gorm.Session{Logger: logger.Discard})
	if _, err := systemsettings.CheckBaseURL(config.C.DevMode != "true"); err != nil {
		fmt.Fprintln(os.Stderr, "Target APP_BASE_URL must be configured correctly.")
		return 1
	}
	if err := migrate.Up(conn); err != nil {
		fmt.Fprintln(os.Stderr, "Target schema migration failed.")
		return 1
	}
	sp, err := datatransfer.OpenSpool(spoolPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Spool could not be opened:", err)
		return 1
	}
	defer sp.Close()
	out := sp.Meta("media_dir")
	if !*d.reuseSpool || sp.Meta("complete") != "1" {
		out = filepath.Join(*d.workDir, "out-"+time.Now().UTC().Format("20060102T150405"))
		cmd := exec.CommandContext(ctx, *d.python, exporterArgs(d, source, script, state, out)...)
		cmd.Env = exporterEnv()
		cmd.Stderr = os.Stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return 1
		}
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "Could not start the source reader:", err)
			return 1
		}
		started := time.Now()
		_, spoolErr := sp.ReadNDJSON(ctx, stdout, func(n int) {
			fmt.Fprintf(os.Stderr, "progress stage=spool done=%d speed=%.0f/s\n", n, float64(n)/time.Since(started).Seconds())
		})
		if spoolErr != nil {
			_, _ = io.Copy(io.Discard, stdout)
		}
		waitErr := cmd.Wait()
		var exitErr *exec.ExitError
		if waitErr != nil && !(errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 2) {
			fmt.Fprintf(os.Stderr, "Source read failed (%v); rerun the same command to resume from the checkpoint.\n", exitReason(waitErr))
			return 1
		}
		if spoolErr != nil {
			fmt.Fprintln(os.Stderr, "Source stream could not be spooled:", spoolErr)
			return 1
		}
		_ = sp.SetMeta("media_dir", out)
	}
	limits := datatransfer.DefaultLimits()
	limits.MaxFiles, limits.MaxExpandedBytes, limits.AllowEmpty = maxFiles, maxBytes, true
	if maxBytes > limits.MaxCompressedBytes {
		limits.MaxCompressedBytes = maxBytes
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return 1
	}
	archive, err := datatransfer.OpenDirectory(ctx, out, limits)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Copied media could not be read safely (%s); raise --max-files/--max-expanded-bytes.\n", safeReason(err))
		return 1
	}
	defer archive.Close()
	_ = datatransfer.TightenTree(*d.workDir)
	code := importSpool(ctx, conn, sp, archive, apply, reportFile, chunkSize)
	if code == 0 && apply {
		_ = sp.Close()
		finishSecretSpool(*d.workDir, *d.keepSecretSpool)
	}
	return code
}

// finishSecretSpool runs after a successful --apply: the spool and exporter
// staging hold plaintext source credentials (including TOTP secrets), so
// they are wiped unless --keep-secret-spool asks to keep them for debugging.
func finishSecretSpool(workDir string, keep bool) {
	if keep {
		fmt.Fprintf(os.Stderr, "WARNING: --keep-secret-spool: %s still contains plaintext source credentials (password hashes, signing keys, TOTP secrets and backup codes). Wipe it as soon as debugging is done.\n", workDir)
		return
	}
	wiped, err := datatransfer.WipeSecretSpool(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not wipe every secret-bearing spool file in %s (%s); remove spool.sqlite3*, state/ and out-*/.spool by hand.\n", workDir, safeReason(err))
		return
	}
	fmt.Fprintf(os.Stderr, "wiped %d secret-bearing spool path(s) in %s (copied media kept); a later run re-reads the source from scratch, receipts keep it idempotent\n", len(wiped), workDir)
}

func importSpool(ctx context.Context, conn *gorm.DB, sp *datatransfer.Spool, archive *datatransfer.Archive, apply bool, reportFile *os.File, chunkSize int) int {
	storageDir, err := filepath.Abs(filepath.Join("uploads", "media"))
	if err != nil {
		return 1
	}
	opts := datatransfer.Options{Scope: "site", TargetBaseURL: systemsettings.GetBaseURL(), ArchiveDir: archive.Root(), Files: archive.Files, StorageDir: storageDir, VerifiedReferences: map[string]bool{}}
	progress := newProgressPrinter()
	report, err := datatransfer.ImportSpool(ctx, conn, sp, opts, datatransfer.StreamOptions{ChunkSize: chunkSize, OnProgress: progress.print("validate")})
	exit := 0
	if err != nil {
		fmt.Fprintf(os.Stderr, "Preflight could not finish (%s). No account data was imported.\n", safeReason(err))
		return 1
	}
	if !report.CanExecute {
		exit = 2
	} else if apply {
		report, err = datatransfer.ImportSpool(ctx, conn, sp, opts, datatransfer.StreamOptions{ChunkSize: chunkSize, Apply: true, OnProgress: progress.print("import")})
		if err != nil {
			exit = 1
			debugCause(err)
			fmt.Fprintf(os.Stderr, "Import stopped (%s). Committed chunks are kept; rerun with the same arguments (direct mode: add --reuse-spool) to resume.\n", safeReason(err))
		}
	}
	if report != nil {
		if reportFile != nil {
			if json.NewEncoder(reportFile).Encode(report) != nil {
				return 1
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
	}
	return exit
}

type progressPrinter struct {
	stage   string
	started time.Time
	last    time.Time
	base    int
}

func newProgressPrinter() *progressPrinter { return &progressPrinter{} }

func (p *progressPrinter) print(phase string) func(datatransfer.StreamProgress) {
	return func(s datatransfer.StreamProgress) {
		now := time.Now()
		if s.Stage != p.stage {
			p.stage, p.started, p.base = s.Stage, now, s.Done
		} else if now.Sub(p.last) < 2*time.Second && s.Done < s.Total {
			return
		}
		p.last = now
		rate := float64(s.Done-p.base) / max(now.Sub(p.started).Seconds(), 0.001)
		pct, eta := 100.0, "0s"
		if s.Total > 0 {
			pct = float64(s.Done) * 100 / float64(s.Total)
			if rate > 0 {
				eta = (time.Duration(float64(s.Total-s.Done)/rate) * time.Second).String()
			} else {
				eta = "?"
			}
		}
		fmt.Fprintf(os.Stderr, "progress stage=%s/%s done=%d total=%d pct=%.1f speed=%.0f/s eta=%s\n", phase, s.Stage, s.Done, s.Total, pct, rate, eta)
	}
}

func exitReason(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit code %d", exitErr.ExitCode())
	}
	return safeReason(err)
}

// streamableBundle returns the bundle.json path for a directory or plain JSON
// file input; zip archives keep the in-memory path.
func streamableBundle(input string, info os.FileInfo) (string, bool) {
	if info.IsDir() {
		p := filepath.Join(input, "bundle.json")
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
		return "", false
	}
	if strings.HasSuffix(strings.ToLower(input), ".json") && info.Mode().IsRegular() {
		return input, true
	}
	return "", false
}

// runStreamedBundle spools bundle.json token by token and imports it in chunks.
func runStreamedBundle(ctx context.Context, conn *gorm.DB, input, bundlePath string, isDir bool, limits datatransfer.Limits, apply bool, reportFile *os.File, reportWritten *bool, chunkSize int) int {
	f, err := os.Open(bundlePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bundle.json is not readable.")
		return 1
	}
	defer f.Close()
	tmp, err := os.MkdirTemp("", "community-transfer-spool-")
	if err != nil {
		return 1
	}
	defer func() {
		if keepSecretSpool != nil && *keepSecretSpool {
			fmt.Fprintf(os.Stderr, "WARNING: --keep-secret-spool: %s contains plaintext source credentials (including TOTP secrets). Wipe it when debugging is done.\n", tmp)
			return
		}
		_ = datatransfer.WipeTree(tmp)
	}()
	sp, err := datatransfer.OpenSpool(filepath.Join(tmp, "spool.sqlite3"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Spool could not be created:", err)
		return 1
	}
	defer sp.Close()
	started := time.Now()
	st, err := sp.ReadBundleJSON(ctx, f, func(n int) {
		fmt.Fprintf(os.Stderr, "progress stage=spool done=%d speed=%.0f/s\n", n, float64(n)/time.Since(started).Seconds())
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unsupported or malformed bundle (%s).\n", safeReason(err))
		return 1
	}
	if sp.Header.Schema != datatransfer.Schema || sp.Header.Scope != "site" {
		fmt.Fprintln(os.Stderr, "Bundle must be a community-transfer/v1 site bundle for streamed import; use --stream=false otherwise.")
		return 1
	}
	fmt.Fprintf(os.Stderr, "spooled %d records in %s\n", st.Records, time.Since(started).Round(time.Millisecond))
	mediaDir := input
	if !isDir {
		mediaDir = filepath.Join(tmp, "empty")
		_ = os.MkdirAll(mediaDir, 0o700)
	}
	// The media index skips bundle.json itself (it is already spooled).
	limits.Skip = []string{"bundle.json", "manifest.json", ".spool"}
	limits.AllowEmpty = true
	archive, err := datatransfer.OpenDirectory(ctx, mediaDir, limits)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Bundle media could not be read safely (%s). Check --max-files/--max-expanded-bytes.\n", safeReason(err))
		return 1
	}
	defer archive.Close()
	code := importSpool(ctx, conn, sp, archive, apply, reportFile, chunkSize)
	if code == 0 && apply {
		bundleNotice()
	}
	if reportFile != nil && code != 1 {
		*reportWritten = true
	}
	return code
}

// bundleNotice reminds operators that an input bundle keeps plaintext source
// credentials; community-transfer never deletes operator-supplied input.
func bundleNotice() {
	fmt.Fprintln(os.Stderr, "NOTICE: the input bundle contains plaintext source credentials (password hashes, signing keys, TOTP secrets, backup codes). Once the import is verified, wipe it (e.g. shred -u bundle.json) and its copies.")
}

// exporterEnv passes the state sealing key (derived from ENCRYPTED_KEY) to
// the exporter so secret fields never rest in plaintext in state/.
func exporterEnv() []string {
	env := os.Environ()
	if k := datatransfer.StateKeyHex(); k != "" {
		env = append(env, datatransfer.StateKeyEnv+"="+k)
	}
	return env
}
