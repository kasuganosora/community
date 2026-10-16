// community-transfer imports an administrator-controlled database export or an
// account backup using the same validation and receipts as the web interface.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/migrate"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() { os.Exit(run()) }

func run() int {
	input := flag.String("input", "", "private source export directory or backup file")
	scope := flag.String("scope", "site", "site or personal")
	source := flag.String("source", "auto", "auto, community, misskey or mastodon")
	kind := flag.String("kind", "archive", "archive, notes, following, blocking, muting, domain_blocks, bookmarks, lists or filters")
	owner := flag.Uint64("account", 0, "target account ID for personal import")
	apply := flag.Bool("apply", false, "import account data; omitted validates only (target schema upgrades still run)")
	reportPath := flag.String("report", "", "write the safe report to a new private JSON file")
	maxBytes := flag.Int64("max-expanded-bytes", 1<<30, "maximum uncompressed snapshot size; provision free disk before increasing")
	maxFiles := flag.Int("max-files", 20000, "maximum number of archive files")
	maxRecords := flag.Int("max-records", 100000, "maximum normalized records")
	flag.Parse()
	if *input == "" || (*scope != "site" && *scope != "personal") || *maxBytes <= 0 || *maxFiles <= 0 || *maxRecords <= 0 {
		fmt.Fprintln(os.Stderr, "Provide --input, --scope site|personal and positive limits. Use the target Community environment configuration.")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Reserve the report before any database changes. Discovering an existing
	// or unwritable report path after a successful import is too late.
	var reportFile *os.File
	reportWritten := false
	if *reportPath != "" {
		var err error
		reportFile, err = os.OpenFile(*reportPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Report path must be a new writable private file.")
			return 1
		}
		defer func() {
			_ = reportFile.Close()
			if !reportWritten {
				_ = os.Remove(*reportPath)
			}
		}()
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
	limits := datatransfer.DefaultLimits()
	limits.MaxExpandedBytes = *maxBytes
	limits.MaxFiles = *maxFiles
	limits.MaxRecords = *maxRecords
	if *maxBytes > limits.MaxCompressedBytes {
		limits.MaxCompressedBytes = *maxBytes
	}
	info, err := os.Lstat(*input)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintln(os.Stderr, "Input must be a readable regular file or directory, not a symlink.")
		return 1
	}
	var archive *datatransfer.Archive
	if info.IsDir() {
		archive, err = datatransfer.OpenDirectory(ctx, *input, limits)
	} else if info.Mode().IsRegular() {
		var f *os.File
		f, err = os.Open(*input)
		if err == nil {
			archive, err = datatransfer.OpenArchive(ctx, filepath.Base(*input), f, limits)
			_ = f.Close()
		}
	} else {
		fmt.Fprintln(os.Stderr, "Unsupported input file type.")
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Backup could not be read safely. Check files, limits and free disk.")
		return 1
	}
	defer archive.Close()
	bundle, err := datatransfer.ParseArchive(ctx, archive, datatransfer.ParseOptions{Platform: *source, Kind: *kind})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unsupported or malformed backup. Select its source and kind.")
		return 1
	}
	storageDir, err := filepath.Abs(filepath.Join("uploads", "media"))
	if err != nil {
		return 1
	}
	opts := datatransfer.Options{Scope: *scope, TargetBaseURL: systemsettings.GetBaseURL(), ArchiveDir: archive.Root(), Files: archive.Files, StorageDir: storageDir, VerifiedReferences: map[string]bool{}}
	if *scope == "personal" {
		var account models.Account
		if *owner == 0 || conn.Where("id = ? AND domain IS NULL", *owner).First(&account).Error != nil || account.UserId == 0 {
			fmt.Fprintln(os.Stderr, "Personal import requires an existing local --account.")
			return 1
		}
		opts.OwnerAccountID = account.Id
		opts.OwnerUserID = account.UserId
		if bundle.Scope != "personal" || len(bundle.Keys) > 0 || len(bundle.Credentials) > 0 {
			fmt.Fprintln(os.Stderr, "Site identities and credentials are forbidden in personal imports.")
			return 2
		}
		if err = datatransfer.ResolvePersonalReferences(ctx, conn, bundle); err != nil {
			fmt.Fprintln(os.Stderr, "Account reference discovery failed.")
			return 1
		}
		for _, a := range bundle.Accounts {
			if a.ID != bundle.OwnerID {
				if verified, ok := a.Settings["personal_reference_verified"].(bool); ok && verified {
					opts.VerifiedReferences[a.ID] = true
				}
			}
		}
	}
	report, err := datatransfer.Preview(ctx, conn, bundle, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Preflight could not finish. No account data was imported.")
		return 1
	}
	exitCode := 0
	if !report.CanExecute {
		exitCode = 2
	} else if *apply {
		report, err = datatransfer.Import(ctx, conn, bundle, opts, nil)
		if err != nil {
			exitCode = 1
			fmt.Fprintln(os.Stderr, "Import did not complete. Review the safe report; receipts support a retry.")
		}
	}
	if report != nil {
		if reportFile != nil {
			e := json.NewEncoder(reportFile).Encode(report)
			closeErr := reportFile.Close()
			if e != nil || closeErr != nil {
				return 1
			}
			reportWritten = true
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
	}
	return exitCode
}
