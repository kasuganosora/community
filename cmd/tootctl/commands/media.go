package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// MediaCommand implements tootctl media subcommands.
func MediaCommand() *cli.Command {
	return &cli.Command{
		Name:  "media",
		Usage: "Manage media files",
		Subcommands: []*cli.Command{
			MediaRemoveCommand(),
			MediaRemoveOrphansCommand(),
			MediaPurgeUnattachedCommand(),
			MediaRefreshCommand(),
			MediaUsageCommand(),
			MediaLookupCommand(),
			MediaBlurhashCommand(),
		},
	}
}

// MediaRemoveCommand implements tootctl media remove
// Version history: 2.5.0 - added.
func MediaRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:  "remove", //nolint:goconst
		Usage: "Remove locally cached copies of media attachments",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "days",
				Value: 7,
				Usage: "How old media attachments have to be before removal",
			},
			&cli.IntFlag{
				Name:    "concurrency", //nolint:goconst
				Aliases: []string{"c"},
				Value:   5,
				Usage:   "The number of workers to use", //nolint:goconst
			},
			&cli.BoolFlag{
				Name:  "prune-profiles",
				Usage: "Remove cached copies of avatars and headers",
			},
			&cli.BoolFlag{
				Name:  "remove-headers",
				Usage: "Remove cached copies of headers only",
			},
			&cli.BoolFlag{
				Name:  "include-follows",
				Usage: "Remove from all accounts regardless of follow status",
			},
			&cli.BoolFlag{
				Name:    "verbose", //nolint:goconst
				Aliases: []string{"v"},
				Usage:   "Print additional information",
			},
			&cli.BoolFlag{
				Name:  "dry-run", //nolint:goconst
				Usage: "Print expected results only",
			},
		},
		Action: removeMedia,
	}
}

func removeMedia(c *cli.Context) error {
	days := c.Int("days")
	concurrency := max(c.Int("concurrency"), 1)
	pruneProfiles := c.Bool("prune-profiles")
	removeHeaders := c.Bool("remove-headers")
	includeFollows := c.Bool("include-follows")
	verbose := c.Bool("verbose")
	dryRun := c.Bool("dry-run")
	out := c.App.Writer

	if pruneProfiles && removeHeaders {
		return errors.New("--prune-profiles and --remove-headers cannot be combined")
	}

	if days < 1 {
		return errors.New("--days must be at least 1")
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	_, _ = fmt.Fprintf(out, "Removing cached remote media older than %d days (cutoff: %s)\n", days, cutoff.Format("2006-01-02"))

	if dryRun {
		_, _ = fmt.Fprintln(out, "Dry run mode - no changes will be made")
	}

	if pruneProfiles || removeHeaders {
		return removeProfileMedia(c.Context, out, cutoff, includeFollows, removeHeaders, verbose, dryRun)
	}

	return removeMediaAttachments(c.Context, out, cutoff, concurrency, verbose, dryRun)
}

// remoteCachedMedia selects attachments of remote accounts that have a
// locally stored copy (file_path set, e.g. by "media refresh") and were
// created before cutoff. Local users' media is never selected.
func remoteCachedMedia(gdb *gorm.DB, cutoff time.Time) *gorm.DB {
	return gdb.Model(&models.MediaAttachment{}).
		Where("remote_url IS NOT NULL AND remote_url <> '' AND file_path <> '' AND created_at < ?", cutoff).
		Where("account_id IN (?)", gdb.Model(&models.Account{}).Select("id").Where("domain IS NOT NULL AND domain <> ''"))
}

// removeMediaAttachments drops the cached copies of old remote attachments
// (Mastodon semantics): stored objects are deleted through the storage
// backend and the row falls back to its remote_url. Rows are kept.
func removeMediaAttachments(ctx context.Context, out io.Writer, cutoff time.Time, concurrency int, verbose bool, dryRun bool) error {
	gdb := db.GetDB().WithContext(ctx)

	var count int64
	if err := remoteCachedMedia(gdb, cutoff).Count(&count).Error; err != nil {
		return fmt.Errorf("count cached remote media: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Found %d cached remote media attachments to process\n", count)

	if dryRun || count == 0 {
		return nil
	}

	jobs := make(chan models.MediaAttachment, 100)

	var (
		wg                      sync.WaitGroup
		mu                      sync.Mutex
		removed, shared, failed int
	)

	for range concurrency {
		wg.Go(func() {
			for attachment := range jobs {
				result := uncacheAttachment(ctx, gdb, &attachment)

				mu.Lock()
				switch result {
				case uncacheRemoved:
					removed++
				case uncacheShared:
					shared++
				default:
					failed++
				}
				if verbose && (removed+shared)%100 == 0 {
					_, _ = fmt.Fprintf(out, "Processed %d attachments...\n", removed+shared)
				}
				mu.Unlock()
			}
		})
	}

	var (
		lastID  uint64
		feedErr error
	)

	for {
		var batch []models.MediaAttachment

		feedErr = remoteCachedMedia(gdb, cutoff).Where("id > ?", lastID).Order("id ASC").Limit(100).Find(&batch).Error
		if feedErr != nil || len(batch) == 0 {
			break
		}

		lastID = batch[len(batch)-1].Id
		for _, a := range batch {
			jobs <- a
		}
	}

	close(jobs)
	wg.Wait()

	_, _ = fmt.Fprintf(out, "Removed cached copies of %d attachments (%d shared files kept, %d failed)\n", removed, shared, failed)

	if feedErr != nil {
		return fmt.Errorf("query cached remote media: %w", feedErr)
	}

	return nil
}

type uncacheResult int

const (
	uncacheRemoved uncacheResult = iota
	uncacheShared
	uncacheFailed
)

// uncacheAttachment deletes the stored files of one remote attachment and
// clears its file columns. Files another row still points at are kept.
func uncacheAttachment(ctx context.Context, gdb *gorm.DB, a *models.MediaAttachment) uncacheResult {
	var others int64
	if err := gdb.Model(&models.MediaAttachment{}).Where("id <> ? AND file_path = ?", a.Id, a.FilePath).Count(&others).Error; err != nil {
		return uncacheFailed
	}

	result := uncacheShared
	if others == 0 {
		if err := a.DeleteStoredFiles(ctx); err != nil {
			return uncacheFailed
		}

		result = uncacheRemoved
	}

	err := gdb.Model(&models.MediaAttachment{}).Where("id = ?", a.Id).
		Updates(map[string]any{"file_path": "", "thumbnail_path": nil}).Error
	if err != nil {
		return uncacheFailed
	}

	return result
}

// removeProfileMedia handles --prune-profiles / --remove-headers. Remote
// avatars and headers are hot-linked (stored as their remote URLs), so only
// legacy references to locally stored copies (plain storage keys below
// avatars/) are deleted and cleared; URLs are never touched.
func removeProfileMedia(ctx context.Context, out io.Writer, _ time.Time, includeFollows bool, headersOnly bool, verbose bool, dryRun bool) error {
	gdb := db.GetDB().WithContext(ctx)
	query := gdb.Model(&models.Account{}).Where("domain IS NOT NULL AND domain <> ''")

	if !includeFollows {
		query = query.Where("id NOT IN (?)", gdb.Model(&models.Follow{}).Select("follow_id"))
	}

	var accounts []*models.Account
	if err := query.Find(&accounts).Error; err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Processing %d remote accounts...\n", len(accounts))

	removed := 0

	for _, account := range accounts {
		fields := map[string]*string{"header": &account.Header, "header_static": &account.HeaderStatic}
		if !headersOnly {
			fields["avatar"] = &account.Avatar
			fields["avatar_static"] = &account.AvatarStatic
		}

		updates := map[string]any{}

		for column, value := range fields {
			key := cachedProfileKey(*value)
			if key == "" {
				continue
			}

			if dryRun {
				if verbose {
					_, _ = fmt.Fprintf(out, "[DRY-RUN] Would remove %s %s of %s\n", column, key, account.Username)
				}

				removed++

				continue
			}

			var others int64
			if err := gdb.Model(&models.Account{}).Where("id <> ? AND (avatar = ? OR avatar_static = ? OR header = ? OR header_static = ?)", account.Id, *value, *value, *value, *value).Count(&others).Error; err != nil {
				return err
			}

			if others == 0 {
				if err := storage.Default().Delete(ctx, key); err != nil {
					_, _ = fmt.Fprintf(out, "Failed to delete %s: %v\n", key, err)

					continue
				}
			}

			updates[column] = ""
			removed++
		}

		if len(updates) > 0 {
			if err := gdb.Model(&models.Account{}).Where("id = ?", account.Id).Updates(updates).Error; err != nil {
				return err
			}
		}
	}

	_, _ = fmt.Fprintf(out, "Removed %d cached profile images (remote URLs are hot-linked and left untouched)\n", removed)

	return nil
}

// cachedProfileKey returns the storage key of a locally cached profile image
// reference, or "" for URLs and anything outside avatars/.
func cachedProfileKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") || filepath.IsAbs(raw) {
		return ""
	}

	key, _ := storage.NormalizeKey(raw)
	if !strings.HasPrefix(key, "avatars/") {
		return ""
	}

	if k, err := storage.CleanKey(key); err == nil {
		return k
	}

	return ""
}

// MediaRemoveOrphansCommand implements tootctl media remove-orphans
// Version history: 3.1.0 - added.
func MediaRemoveOrphansCommand() *cli.Command {
	return &cli.Command{
		Name:  "remove-orphans",
		Usage: "Scan for media files no database row references (dry run unless --confirm)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "start-after",
				Usage: "Storage key after which to resume scanning",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Only report orphans (the default; wins over --confirm)",
			},
			&cli.BoolFlag{
				Name:  "confirm",
				Usage: "Actually delete the orphaned objects",
			},
			&cli.StringFlag{
				Name:  "prefix",
				Usage: "Scan only keys below this prefix (must be inside media/, matrix/ or refreshed/)",
			},
			&cli.DurationFlag{
				Name:  "min-age",
				Value: 24 * time.Hour,
				Usage: "Grace period: ignore objects modified more recently than this (minimum 1h)",
			},
		},
		Action: removeOrphanedMedia,
	}
}

func removeOrphanedMedia(c *cli.Context) error {
	out := c.App.Writer
	deleteOrphans := c.Bool("confirm") && !c.Bool("dry-run")
	minAge := c.Duration("min-age")

	if minAge < minOrphanAge {
		return fmt.Errorf("--min-age must be at least %s", minOrphanAge)
	}

	prefixes, err := resolveOrphanPrefixes(c.String("prefix"))
	if err != nil {
		return err
	}

	backend := storage.Default()
	if err := backend.Check(c.Context); err != nil {
		return fmt.Errorf("storage backend unavailable: %w", err)
	}

	return runOrphanScan(c.Context, out, backend, db.GetDB(), orphanScanOptions{
		Prefixes:   prefixes,
		StartAfter: strings.TrimLeft(c.String("start-after"), "/"),
		MinAge:     minAge,
		Delete:     deleteOrphans,
	})
}

func runOrphanScan(ctx context.Context, out io.Writer, backend storage.Backend, gdb *gorm.DB, opts orphanScanOptions) error {
	opts.Out = out

	mode := "DRY RUN (pass --confirm to delete)"
	if opts.Delete {
		mode = "DELETING orphaned objects"
	}

	_, _ = fmt.Fprintf(out, "Scanning %s storage under %s for orphaned objects older than %s - %s\n", backend.Kind(), strings.Join(opts.Prefixes, ", "), opts.MinAge, mode)

	refs, err := collectReferencedKeys(ctx, gdb)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Loaded %d referenced storage keys from the database\n", refs.Len())

	if refs.owned == 0 {
		found := false
		for _, prefix := range opts.Prefixes {
			if err := backend.List(ctx, prefix, func(storage.ObjectInfo) error {
				found = true

				return storage.ErrStopList
			}); err != nil {
				return fmt.Errorf("list %s: %w", prefix, err)
			}
		}

		if found {
			return errNoReferences
		}
	}

	res, err := scanOrphans(ctx, backend, gdb, refs, opts)

	_, _ = fmt.Fprintf(out, "Scanned %d objects: %d referenced, %d within the grace period, %d orphaned (%s)", res.Scanned, res.Referenced, res.TooYoung, res.Orphans, humanBytes(res.OrphanBytes))
	if opts.Delete {
		_, _ = fmt.Fprintf(out, ", %d deleted, %d failed", res.Deleted, res.Failed)
	}

	_, _ = fmt.Fprintln(out)

	return err
}

// MediaRefreshCommand implements tootctl media refresh
// Version history: 3.0.0 - added.
func MediaRefreshCommand() *cli.Command {
	return &cli.Command{
		Name:  "refresh",
		Usage: "Refetch remote media attachments",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "account",
				Usage: "String username@domain handle",
			},
			&cli.StringFlag{
				Name:  "domain",
				Usage: "FQDN string",
			},
			&cli.StringFlag{
				Name:  "status",
				Usage: "Local numeric ID of the status",
			},
			&cli.IntFlag{
				Name:  "days",
				Usage: "Number of days to limit this task to",
			},
			&cli.IntFlag{
				Name:    "concurrency",
				Aliases: []string{"c"},
				Value:   5,
			},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}},
			&cli.BoolFlag{Name: "dry-run"},
			&cli.BoolFlag{Name: "force", Usage: "Force redownload and overwrite"},
		},
		Action: refreshMedia,
	}
}

func refreshMedia(c *cli.Context) error { //nolint:gocyclo // complex filter composition, inherent to CLI multi-flag logic
	// Need at least one filter
	account := c.String("account")
	domain := c.String("domain")
	status := c.String("status")
	days := c.Int("days")
	concurrency := c.Int("concurrency")
	verbose := c.Bool("verbose")
	dryRun := c.Bool("dry-run")
	force := c.Bool("force")

	if account == "" && domain == "" && status == "" && days == 0 {
		return errors.New("must specify --account, --domain, --status, or --days")
	}

	fmt.Println("Refreshing remote media attachments...")

	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
	}

	// Build query for attachments
	// Only media of remote accounts; local users' files (including
	// imported ones that still carry their old remote_url) are never touched.
	query := db.GetDB().Model(&models.Attachment{}).
		Where("remote_url IS NOT NULL AND remote_url != ''").
		Where("account_id IN (?)", db.GetDB().Model(&models.Account{}).Select("id").Where("domain IS NOT NULL AND domain <> ''"))

	// Filter by account
	if account != "" {
		parts := strings.Split(account, "@")
		if len(parts) == 2 {
			// username@domain format
			var acc models.Account
			err := db.GetDB().Where("username = ? AND domain = ?", parts[0], parts[1]).First(&acc).Error
			if err != nil {
				return fmt.Errorf("account not found: %s", account)
			}
			// Get statuses from this account
			var statusIDs []uint64

			db.GetDB().Model(&models.Status{}).Where("account_id = ?", acc.Id).Pluck("id", &statusIDs)

			if len(statusIDs) == 0 {
				fmt.Println("No statuses found for this account")

				return nil
			}

			var mediaIDs []uint64

			db.GetDB().Model(&models.MediaAttachment{}).Where("status_id IN ?", statusIDs).Pluck("id", &mediaIDs)

			if len(mediaIDs) == 0 {
				fmt.Println("No media found for this account")

				return nil
			}

			query = query.Where("id IN ?", mediaIDs)
		} else {
			// Local username only
			var acc models.Account
			err := db.GetDB().Where("username = ? AND domain IS NULL", account).First(&acc).Error
			if err != nil {
				return fmt.Errorf("account not found: %s", account)
			}

			var statusIDs []uint64

			db.GetDB().Model(&models.Status{}).Where("account_id = ?", acc.Id).Pluck("id", &statusIDs)

			if len(statusIDs) == 0 {
				fmt.Println("No statuses found for this account")

				return nil
			}

			var mediaIDs []uint64

			db.GetDB().Model(&models.MediaAttachment{}).Where("status_id IN ?", statusIDs).Pluck("id", &mediaIDs)

			if len(mediaIDs) == 0 {
				fmt.Println("No media found for this account")

				return nil
			}

			query = query.Where("id IN ?", mediaIDs)
		}
	}

	// Filter by domain
	if domain != "" {
		// Get accounts from this domain
		var accountIDs []uint64

		db.GetDB().Model(&models.Account{}).Where("domain = ?", domain).Pluck("id", &accountIDs)

		if len(accountIDs) == 0 {
			fmt.Printf("No accounts found from domain %s\n", domain)

			return nil
		}

		var statusIDs []uint64

		db.GetDB().Model(&models.Status{}).Where("account_id IN ?", accountIDs).Pluck("id", &statusIDs)

		if len(statusIDs) == 0 {
			fmt.Printf("No statuses found from domain %s\n", domain)

			return nil
		}

		var mediaIDs []uint64

		db.GetDB().Model(&models.MediaAttachment{}).Where("status_id IN ?", statusIDs).Pluck("id", &mediaIDs)

		if len(mediaIDs) == 0 {
			fmt.Printf("No media found from domain %s\n", domain)

			return nil
		}

		query = query.Where("id IN ?", mediaIDs)
	}

	// Filter by status
	if status != "" {
		statusID, err := strconv.ParseUint(status, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid status ID: %s", status)
		}

		var mediaIDs []uint64

		db.GetDB().Model(&models.MediaAttachment{}).Where("status_id = ?", statusID).Pluck("id", &mediaIDs)

		if len(mediaIDs) == 0 {
			fmt.Println("No media found for this status")

			return nil
		}

		query = query.Where("id IN ?", mediaIDs)
	}

	// Filter by days (attachments older than N days)
	if days > 0 {
		cutoff := time.Now().AddDate(0, 0, -days)
		query = query.Where("created_at < ?", cutoff)
	}

	// Reusable base: every chained call below starts from these filters.
	query = query.Session(&gorm.Session{})

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("count attachments: %w", err)
	}

	fmt.Printf("Found %d attachments to refresh\n", count)

	if count == 0 || dryRun {
		return nil
	}

	// Process in batches
	batchSize := 100
	jobs := make(chan []uint64, 10)

	var wg sync.WaitGroup

	refreshed := 0
	failed := 0

	var mu sync.Mutex

	for range concurrency {
		wg.Go(func() {
			for ids := range jobs {
				for _, id := range ids {
					var attachment models.Attachment
					if err := db.GetDB().First(&attachment, id).Error; err != nil {
						continue
					}

					// Skip if already has local copy and not forcing
					if attachment.FilePath != "" && !force {
						continue
					}

					// Download from remote URL
					if attachment.RemoteUrl == "" {
						continue
					}

					if verbose {
						fmt.Printf("Downloading: %s\n", attachment.RemoteUrl)
					}

					data, err := downloadRemoteMedia(attachment.RemoteUrl)
					if err != nil {
						mu.Lock()
						failed++

						if verbose {
							fmt.Printf("Failed to download %s: %v\n", attachment.RemoteUrl, err)
						}
						mu.Unlock()

						continue
					}

					if err := storeRefreshedMedia(context.Background(), &attachment, data); err != nil {
						mu.Lock()
						failed++

						if verbose {
							fmt.Printf("Failed to store %s: %v\n", attachment.RemoteUrl, err)
						}
						mu.Unlock()

						continue
					}

					mu.Lock()

					refreshed++
					if refreshed%100 == 0 {
						fmt.Printf("Refreshed %d attachments...\n", refreshed)
					}
					mu.Unlock()
				}
			}
		})
	}

	// Feed jobs
	var lastID uint64 = 0

	for {
		var attachments []models.Attachment

		err := query.Where("id > ?", lastID).
			Order("id ASC").
			Limit(batchSize).
			Find(&attachments).Error

		if err != nil || len(attachments) == 0 {
			break
		}

		ids := make([]uint64, len(attachments))
		for i, a := range attachments {
			ids[i] = a.Id
		}

		lastID = attachments[len(attachments)-1].Id

		jobs <- ids
	}

	close(jobs)

	wg.Wait()

	fmt.Printf("\nRefresh completed: %d refreshed, %d failed\n", refreshed, failed)

	return nil //nolint:nilerr
}

// maxRemoteMediaSize limits how much data is buffered into memory when
// re-fetching remote media. Remote attachments are expected to be media files
// (images/videos), so 50MB is a generous upper bound that also bounds memory use.
const maxRemoteMediaSize int64 = 50 * 1024 * 1024

// remoteHTTPClient is a shared client for fetching remote media.
// It uses a custom DialContext that blocks private/loopback/link-local
// addresses to prevent SSRF (e.g. accessing cloud metadata endpoints).
var remoteHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("stopped after 5 redirects")
		}
		// Re-validate every redirect target for SSRF.
		return validateRemoteURL(req.URL.String())
	},
	Transport: &http.Transport{
		DialContext: safeDialContext,
	},
}

// isBlockedAddress reports whether an IP address is private, loopback,
// link-local, or otherwise unsuitable for outbound requests (SSRF protection).
// Mirrors the guard in matrix/homeserver/media.go so behavior is consistent.
func isBlockedAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}

	return false
}

// validateRemoteURL validates a URL before making an outbound request.
// It enforces http/https schemes and blocks private/internal hosts (SSRF).
func validateRemoteURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}

	host := parsed.Hostname()
	if host == "" {
		return errors.New("URL host is empty")
	}

	// If the host is already an IP literal, validate it directly.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedAddress(ip) {
			return fmt.Errorf("host %s is a blocked address", host)
		}

		return nil
	}

	// Resolve hostname and block if any resolved IP is private/internal.
	// This catches DNS-based SSRF where a domain resolves to a private IP.
	resolver := net.Resolver{}
	ips, err := resolver.LookupIP(context.Background(), "ip", host) //nolint:noctx // SSRF 校验函数，使用独立 context
	if err != nil {
		return fmt.Errorf("failed to resolve host %s: %w", host, err)
	}

	for _, ip := range ips {
		if isBlockedAddress(ip) {
			return fmt.Errorf("host %s resolves to a blocked address %s", host, ip.String())
		}
	}

	return nil
}

// safeDialContext is a DialContext that blocks connections to private/loopback/
// link-local addresses. This is defense-in-depth against DNS rebinding: even if
// the resolved address changes between validateRemoteURL and the actual dial,
// the connection is rejected if it lands on a blocked range.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	for _, ip := range ips {
		if isBlockedAddress(ip.IP) {
			return nil, fmt.Errorf("dial %s: blocked address %s", host, ip.IP)
		}
	}

	dialer := &net.Dialer{}
	// Dial the original addr; the resolver above already validated the IPs.
	// Connect to the first resolved IP directly to avoid re-resolution races.
	if len(ips) > 0 {
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}

	return dialer.DialContext(ctx, network, addr)
}

// downloadRemoteMedia downloads media from a remote URL with SSRF protection
// and a bounded read to prevent memory exhaustion.
func downloadRemoteMedia(mediaURL string) ([]byte, error) {
	// Validate the URL before any network access (SSRF protection).
	if err := validateRemoteURL(mediaURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := remoteHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Bound the read to prevent a malicious server from exhausting memory.
	return io.ReadAll(io.LimitReader(resp.Body, maxRemoteMediaSize))
}

// MediaUsageCommand implements tootctl media usage
// Version history: 3.0.1 - added.
func MediaUsageCommand() *cli.Command {
	return &cli.Command{
		Name:   "usage",
		Usage:  "Calculate disk space consumed by Mastodon",
		Action: mediaUsage,
	}
}

func mediaUsage(c *cli.Context) error {
	return printMediaUsage(c.Context, c.App.Writer, storage.Default(), db.GetDB())
}

func printMediaUsage(ctx context.Context, out io.Writer, backend storage.Backend, gdb *gorm.DB) error {
	gdb = gdb.WithContext(ctx)
	_, _ = fmt.Fprintln(out, "Calculating storage usage...")

	counts := []struct {
		label string
		query *gorm.DB
	}{
		{"Media attachments", gdb.Model(&models.MediaAttachment{})},
		{"  stored locally / in bucket", gdb.Model(&models.MediaAttachment{}).Where("file_path <> ''")},
		{"  remote (hot-linked)", gdb.Model(&models.MediaAttachment{}).Where("file_path = '' AND remote_url <> ''")},
		{"Custom emojis", gdb.Model(&models.Emoji{})},
	}

	_, _ = fmt.Fprintf(out, "\nDatabase:\n")

	for _, q := range counts {
		var n int64
		if err := q.query.Count(&n).Error; err != nil {
			return fmt.Errorf("count %s: %w", strings.TrimSpace(q.label), err)
		}

		_, _ = fmt.Fprintf(out, "  %-30s %d\n", q.label+":", n)
	}

	if err := writeBackendUsage(ctx, out, backend, fmt.Sprintf("Storage (%s backend)", backend.Kind())); err != nil {
		return err
	}

	// Files that predate the switch to S3 and are still served from the
	// legacy local directory.
	if s3, ok := backend.(*storage.S3Backend); ok && s3.Config().LegacyLocalRoot != "" {
		legacy := &storage.LocalBackend{Root: s3.Config().LegacyLocalRoot}
		if err := writeBackendUsage(ctx, out, legacy, "Legacy local fallback ("+s3.Config().LegacyLocalRoot+"/)"); err != nil {
			return err
		}
	}

	return nil
}

func writeBackendUsage(ctx context.Context, out io.Writer, backend storage.Backend, title string) error {
	usage, err := storageUsage(ctx, backend)
	if err != nil {
		return fmt.Errorf("list storage: %w", err)
	}

	_, _ = fmt.Fprintf(out, "\n%s:\n", title)

	var objects, total int64

	for _, u := range usage {
		_, _ = fmt.Fprintf(out, "  %-14s %8d objects  %12s\n", u.Namespace+"/", u.Objects, humanBytes(u.Bytes))
		objects += u.Objects
		total += u.Bytes
	}

	_, _ = fmt.Fprintf(out, "  %-14s %8d objects  %12s (%d bytes)\n", "total", objects, humanBytes(total), total)

	return nil
}

// MediaLookupCommand implements tootctl media lookup
// Version history: 3.1.0 - added.
func MediaLookupCommand() *cli.Command {
	return &cli.Command{
		Name:   "lookup",
		Usage:  "Lookup the status where a media URL is displayed",
		Action: mediaLookup,
	}
}

func mediaLookup(c *cli.Context) error {
	var mediaURL string

	fmt.Print("Enter media URL: ")

	_, _ = fmt.Scanln(&mediaURL)

	// Search for the media in attachments
	var attachment models.Attachment

	err := db.GetDB().Where("file_path = ? OR thumbnail_path = ? OR remote_url = ?", mediaURL, mediaURL, mediaURL).
		First(&attachment).Error
	if err != nil {
		fmt.Println("Media not found in any attachment")

		return nil //nolint:nilerr
	}

	// Get the status that references this attachment
	if attachment.StatusId == nil {
		fmt.Printf("Found attachment ID %d but no status references it\n", attachment.Id)

		return nil
	}

	// Get the status
	var status models.Status
	if err := db.GetDB().First(&status, *attachment.StatusId).Error; err != nil {
		fmt.Printf("Found attachment ID %d but status %d not found\n", attachment.Id, *attachment.StatusId)

		return nil //nolint:nilerr
	}

	// Get the account
	var account models.Account

	db.GetDB().First(&account, status.AccountId)

	fmt.Printf("\nStatus found:\n")
	fmt.Printf("============\n")
	fmt.Printf("Status ID:    %d\n", status.Id)
	fmt.Printf("Account:      @%s\n", account.Username)
	fmt.Printf("Created at:   %s\n", status.CreatedAt)
	fmt.Printf("Visibility:   %d\n", status.Visibility)

	if status.Content.Content != "" {
		content := status.Content.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}

		fmt.Printf("Content:      %s\n", content)
	}

	return nil
}

// StatusesCommand implements tootctl statuses subcommands.
func StatusesCommand() *cli.Command {
	cmd := &cli.Command{
		Name:  "statuses", //nolint:goconst
		Usage: "Manage statuses",
		Subcommands: []*cli.Command{
			{
				Name:      "remove",
				Usage:     "Remove statuses from a specific account",
				ArgsUsage: "USERNAME",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "domain", Usage: "Domain of the account (for remote accounts)"},
				},
				Action: removeStatuses,
			},
		},
	}
	cmd.Subcommands = append(cmd.Subcommands, statusesBackfillCommands()...)
	cmd.Subcommands = append(cmd.Subcommands, statusesRefreshSensitiveCommand())

	return cmd
}

func removeStatuses(c *cli.Context) error {
	if c.NArg() < 1 {
		return errors.New("USERNAME is required")
	}

	username := c.Args().First()
	domain := c.String("domain")

	query := db.GetDB().Model(&models.Account{}).Where("username = ?", username)
	if domain != "" {
		query = query.Where("domain = ?", domain)
	} else {
		query = query.Where("domain IS NULL")
	}

	var account models.Account
	err := query.First(&account).Error
	if err != nil {
		return errors.New("account not found")
	}

	// Get status count
	var statusCount int64

	db.GetDB().Model(&models.Status{}).Where("account_id = ?", account.Id).Count(&statusCount)

	fmt.Printf("Removing %d statuses from account %s", statusCount, username)

	if domain != "" {
		fmt.Printf("@%s", domain)
	}

	fmt.Println()

	result := db.GetDB().Where("account_id = ?", account.Id).Delete(&models.Status{})
	if result.Error != nil {
		return fmt.Errorf("failed to remove statuses: %w", result.Error)
	}

	fmt.Printf("Removed %d statuses from account %s\n", result.RowsAffected, username)

	return nil
}

// refreshedMediaExt keeps a short, safe extension from a remote URL.
func refreshedMediaExt(remoteURL string) string {
	u, err := url.Parse(remoteURL)
	if err != nil {
		return ""
	}

	ext := strings.ToLower(path.Ext(u.Path))
	if len(ext) < 2 || len(ext) > 6 {
		return ""
	}

	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}

	return ext
}

// storeRefreshedMedia writes re-fetched remote media below media/ (the same
// layout uploads use) and points the row at it. A previous cached copy is
// deleted afterwards unless another row shares it.
func storeRefreshedMedia(ctx context.Context, attachment *models.MediaAttachment, data []byte) error {
	now := time.Now()
	rel := fmt.Sprintf("%s/%d_%d%s", now.Format("2006/01/02"), now.UnixNano(), attachment.Id, refreshedMediaExt(attachment.RemoteUrl))
	key := models.MediaStorageKey(rel)

	contentType := attachment.ContentType
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}

	if err := storage.Default().Put(ctx, key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		return err
	}

	previous := *attachment
	gdb := db.GetDB().WithContext(ctx)

	err := gdb.Model(&models.MediaAttachment{}).Where("id = ?", attachment.Id).
		Updates(map[string]any{"file_path": rel, "thumbnail_path": nil, "file_size": int64(len(data))}).Error
	if err != nil {
		_ = storage.Default().Delete(context.Background(), key)

		return err
	}

	if previous.FilePath != "" {
		var others int64
		if gdb.Model(&models.MediaAttachment{}).Where("id <> ? AND file_path = ?", previous.Id, previous.FilePath).Count(&others).Error == nil && others == 0 {
			_ = previous.DeleteStoredFiles(ctx)
		}
	}

	attachment.FilePath = rel
	attachment.ThumbnailPath = nil
	attachment.FileSize = int64(len(data))

	return nil
}
