package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/urfave/cli/v2"
)

// MediaCommand implements tootctl media subcommands
func MediaCommand() *cli.Command {
	return &cli.Command{
		Name:  "media",
		Usage: "Manage media files",
		Subcommands: []*cli.Command{
			MediaRemoveCommand(),
			MediaRemoveOrphansCommand(),
			MediaRefreshCommand(),
			MediaUsageCommand(),
			MediaLookupCommand(),
		},
	}
}

// MediaRemoveCommand implements tootctl media remove
// Version history: 2.5.0 - added
func MediaRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:  "remove",
		Usage: "Remove locally cached copies of media attachments",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "days",
				Value: 7,
				Usage: "How old media attachments have to be before removal",
			},
			&cli.IntFlag{
				Name:    "concurrency",
				Aliases: []string{"c"},
				Value:   5,
				Usage:   "The number of workers to use",
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
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Print additional information",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Print expected results only",
			},
		},
		Action: removeMedia,
	}
}

func removeMedia(c *cli.Context) error {
	days := c.Int("days")
	concurrency := c.Int("concurrency")
	pruneProfiles := c.Bool("prune-profiles")
	removeHeaders := c.Bool("remove-headers")
	includeFollows := c.Bool("include-follows")
	verbose := c.Bool("verbose")
	dryRun := c.Bool("dry-run")

	if pruneProfiles && removeHeaders {
		return fmt.Errorf("--prune-profiles and --remove-headers cannot be combined")
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	fmt.Printf("Removing media older than %d days (cutoff: %s)\n", days, cutoff.Format("2006-01-02"))

	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
	}

	if pruneProfiles || removeHeaders {
		return removeProfileMedia(cutoff, includeFollows, removeHeaders, verbose, dryRun)
	}

	// Remove regular media attachments
	return removeMediaAttachments(cutoff, concurrency, verbose, dryRun)
}

func removeMediaAttachments(cutoff time.Time, concurrency int, verbose bool, dryRun bool) error {
	var count int64
	db.GetDB().Model(&models.Attachment{}).
		Where("created_at < ?", cutoff).
		Count(&count)

	fmt.Printf("Found %d media attachments to process\n", count)

	if dryRun || count == 0 {
		return nil
	}

	// Process in batches
	batchSize := 100
	jobs := make(chan []uint64, 10)
	var wg sync.WaitGroup
	removed := 0
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ids := range jobs {
				for _, id := range ids {
					var attachment models.Attachment
					if err := db.GetDB().First(&attachment, id).Error; err != nil {
						continue
					}

					// Delete from storage
					if attachment.SavePath != "" {
						storage.Delete(attachment.SavePath)
					}
					if attachment.PreviewSavePath != "" && attachment.PreviewSavePath != attachment.SavePath {
						storage.Delete(attachment.PreviewSavePath)
					}

					// Delete from database
					db.GetDB().Delete(&attachment)

					mu.Lock()
					removed++
					if verbose && removed%100 == 0 {
						fmt.Printf("Removed %d attachments...\n", removed)
					}
					mu.Unlock()
				}
			}
		}()
	}

	// Feed jobs
	var lastID uint64 = 0
	for {
		var attachments []models.Attachment
		err := db.GetDB().Where("created_at < ? AND id > ?", cutoff, lastID).
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
	fmt.Printf("Removed %d media attachments\n", removed)
	return nil
}

func removeProfileMedia(cutoff time.Time, includeFollows bool, headersOnly bool, verbose bool, dryRun bool) error {
	query := db.GetDB().Model(&models.Account{}).
		Where("domain IS NOT NULL AND domain != ''")

	if !includeFollows {
		// Only remove from accounts not followed locally
		query = query.Where("id NOT IN (SELECT DISTINCT follow_id FROM follows)")
	}

	var accounts []*models.Account
	if err := query.Find(&accounts).Error; err != nil {
		return err
	}

	fmt.Printf("Processing %d remote accounts...\n", len(accounts))

	removed := 0
	for _, account := range accounts {
		if dryRun {
			if verbose {
				fmt.Printf("[DRY-RUN] Would remove media for: %s\n", account.Username)
			}
			continue
		}

		if !headersOnly && account.Avatar != "" {
			storage.Delete(account.Avatar)
			account.Avatar = ""
			removed++
		}

		if account.Header != "" {
			storage.Delete(account.Header)
			account.Header = ""
			removed++
		}

		db.GetDB().Model(account).Updates(map[string]interface{}{
			"avatar": account.Avatar,
			"header": account.Header,
		})
	}

	fmt.Printf("Removed %d profile images\n", removed)
	return nil
}

// MediaRemoveOrphansCommand implements tootctl media remove-orphans
// Version history: 3.1.0 - added
func MediaRemoveOrphansCommand() *cli.Command {
	return &cli.Command{
		Name:  "remove-orphans",
		Usage: "Scan for and remove orphaned media files",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "start-after",
				Usage: "Attachment key where to start",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Print expected results only",
			},
			&cli.StringFlag{
				Name:  "prefix",
				Usage: "Traverse only a specific prefix",
			},
			&cli.BoolFlag{
				Name:  "fix-permissions",
				Usage: "Fix S3 ACL permissions",
			},
		},
		Action: removeOrphanedMedia,
	}
}

func removeOrphanedMedia(c *cli.Context) error {
	startAfter := c.String("start-after")
	dryRun := c.Bool("dry-run")
	prefix := c.String("prefix")

	fmt.Println("Scanning for orphaned media files...")
	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
	}

	// Get all media URLs from database
	var attachmentURLs []string
	db.GetDB().Model(&models.Attachment{}).Pluck("save_path", &attachmentURLs)
	db.GetDB().Model(&models.Attachment{}).Pluck("preview_save_path", &attachmentURLs)

	var avatarURLs []string
	db.GetDB().Model(&models.Account{}).Where("avatar IS NOT NULL AND avatar != ''").Pluck("avatar", &avatarURLs)

	var headerURLs []string
	db.GetDB().Model(&models.Account{}).Where("header IS NOT NULL AND header != ''").Pluck("header", &headerURLs)

	// Combine all valid URLs
	validURLs := make(map[string]bool)
	for _, url := range attachmentURLs {
		validURLs[url] = true
	}
	for _, url := range avatarURLs {
		validURLs[url] = true
	}
	for _, url := range headerURLs {
		validURLs[url] = true
	}

	fmt.Printf("Found %d valid URLs in database\n", len(validURLs))

	// List files in storage
	files, err := listStorageFiles(prefix)
	if err != nil {
		return fmt.Errorf("failed to list storage files: %w", err)
	}

	orphans := 0
	for _, file := range files {
		if startAfter != "" && file < startAfter {
			continue
		}

		if !validURLs[file] {
			orphans++
			if dryRun {
				fmt.Printf("[ORPHAN] %s\n", file)
			} else {
				storage.Delete(file)
				fmt.Printf("[DELETED] %s\n", file)
			}
		}
	}

	fmt.Printf("Found %d orphaned files\n", orphans)
	return nil
}

// MediaRefreshCommand implements tootctl media refresh
// Version history: 3.0.0 - added
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

func refreshMedia(c *cli.Context) error {
	// Need at least one filter
	account := c.String("account")
	domain := c.String("domain")
	status := c.String("status")
	days := c.Int("days")

	if account == "" && domain == "" && status == "" && days == 0 {
		return fmt.Errorf("must specify --account, --domain, --status, or --days")
	}

	fmt.Println("Refreshing remote media attachments...")
	return nil // Placeholder implementation
}

// MediaUsageCommand implements tootctl media usage
// Version history: 3.0.1 - added
func MediaUsageCommand() *cli.Command {
	return &cli.Command{
		Name:   "usage",
		Usage:  "Calculate disk space consumed by Mastodon",
		Action: mediaUsage,
	}
}

func mediaUsage(c *cli.Context) error {
	fmt.Println("Calculating disk usage...")

	// Count attachments
	var attachmentCount int64
	db.GetDB().Model(&models.Attachment{}).Count(&attachmentCount)

	// Count accounts with avatars/headers
	var avatarCount int64
	db.GetDB().Model(&models.Account{}).Where("avatar IS NOT NULL AND avatar != ''").Count(&avatarCount)

	var headerCount int64
	db.GetDB().Model(&models.Account{}).Where("header IS NOT NULL AND header != ''").Count(&headerCount)

	// Count custom emojis
	var emojiCount int64
	db.GetDB().Model(&models.Emoji{}).Count(&emojiCount)

	fmt.Printf("\nMedia Usage Summary:\n")
	fmt.Printf("====================\n")
	fmt.Printf("Media attachments: %d\n", attachmentCount)
	fmt.Printf("Account avatars:   %d\n", avatarCount)
	fmt.Printf("Account headers:   %d\n", headerCount)
	fmt.Printf("Custom emojis:     %d\n", emojiCount)

	// Get storage info
	storageInfo := getStorageInfo()
	if storageInfo != "" {
		fmt.Printf("\nStorage: %s\n", storageInfo)
	}

	return nil
}

// MediaLookupCommand implements tootctl media lookup
// Version history: 3.1.0 - added
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
	fmt.Scanln(&mediaURL)

	// Search for the media in attachments
	var attachment models.Attachment
	err := db.GetDB().Where("save_path = ? OR preview_save_path = ? OR remote_url = ?", mediaURL, mediaURL, mediaURL).
		First(&attachment).Error

	if err != nil {
		fmt.Println("Media not found in any attachment")
		return nil
	}

	// Get the status that references this attachment
	var statusMedia models.StatusMediaAttachment
	err = db.GetDB().Where("media_id = ?", attachment.Id).First(&statusMedia).Error
	if err != nil {
		fmt.Printf("Found attachment ID %d but no status references it\n", attachment.Id)
		return nil
	}

	// Get the status
	var status models.Status
	if err := db.GetDB().First(&status, statusMedia.StatusId).Error; err != nil {
		fmt.Printf("Found attachment ID %d but status %d not found\n", attachment.Id, statusMedia.StatusId)
		return nil
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

// StatusesCommand implements tootctl statuses subcommands
func StatusesCommand() *cli.Command {
	return &cli.Command{
		Name:  "statuses",
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
}

func removeStatuses(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("USERNAME is required")
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
	if err := query.First(&account).Error; err != nil {
		return fmt.Errorf("account not found")
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

// listStorageFiles lists files in storage with a given prefix
func listStorageFiles(prefix string) ([]string, error) {
	// For local storage, list files in the uploads directory
	dir := "uploads"
	if prefix != "" {
		dir = filepath.Join(dir, prefix)
	}

	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() {
			relPath, _ := filepath.Rel("uploads", path)
			files = append(files, relPath)
		}
		return nil
	})

	return files, err
}

// getStorageInfo returns storage information
func getStorageInfo() string {
	// Check if cloud storage is enabled
	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		return "Cloud storage enabled"
	}

	// Check local storage
	dir := "uploads"
	var size int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, _ error) error {
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})

	return fmt.Sprintf("Local storage: %d bytes (%.2f MB)", size, float64(size)/1024/1024)
}
