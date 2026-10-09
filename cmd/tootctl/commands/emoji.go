package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// EmojiCommand implements tootctl emoji subcommands.
func EmojiCommand() *cli.Command {
	return &cli.Command{
		Name:  "emoji",
		Usage: "Manage custom emoji",
		Subcommands: []*cli.Command{
			EmojiExportCommand(),
			EmojiImportCommand(),
			EmojiPurgeCommand(),
		},
	}
}

// EmojiExportCommand implements tootctl emoji export
// Exports custom emoji to export.tar.gz at PATH.
// Version history: 3.1.4 - added.
func EmojiExportCommand() *cli.Command {
	return &cli.Command{
		Name:      "export",
		Usage:     "Export custom emoji to a .tar.gz archive",
		ArgsUsage: "PATH",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "overwrite",
				Usage: "Overwrite existing archive at PATH",
			},
			&cli.StringFlag{
				Name:  "category",
				Usage: "Export only the specified category",
			},
		},
		Action: exportEmoji,
	}
}

func exportEmoji(c *cli.Context) error {
	if c.NArg() < 1 {
		return errors.New("PATH is required")
	}

	outputPath := c.Args().First()
	overwrite := c.Bool("overwrite")
	category := c.String("category")

	// Check if file exists
	if _, err := os.Stat(outputPath); err == nil && !overwrite {
		return fmt.Errorf("file already exists at %s, use --overwrite to replace", outputPath)
	}

	// Get emojis to export
	query := db.GetDB().Model(&models.Emoji{})
	if category != "" {
		query = query.Where("category = ?", category)
	}

	var emojis []*models.Emoji
	if err := query.Find(&emojis).Error; err != nil {
		return fmt.Errorf("failed to query emojis: %w", err)
	}

	if len(emojis) == 0 {
		fmt.Println("No emojis found to export")

		return nil
	}

	fmt.Printf("Exporting %d emojis to %s...\n", len(emojis), outputPath)

	// Create output file
	file, err := os.Create(outputPath) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Create gzip writer
	gzWriter := gzip.NewWriter(file)
	defer func() { _ = gzWriter.Close() }()

	// Create tar writer
	tarWriter := tar.NewWriter(gzWriter)
	defer func() { _ = tarWriter.Close() }()

	exported := 0

	for _, emoji := range emojis {
		// Get the image file
		if emoji.Url == "" {
			continue
		}

		// Only this instance's stored assets can be exported; remote
		// emoji are hot-linked and have no stored file.
		key := emojiasset.StorageKey(emoji.Url)
		if key == "" {
			fmt.Printf("Skipping %s: image is not stored on this instance\n", emoji.Shortcode)

			continue
		}

		reader, _, err := storage.Default().Open(c.Context, key)
		if err != nil {
			fmt.Printf("Warning: failed to read emoji %s: %v\n", emoji.Shortcode, err)

			continue
		}

		ext := path.Ext(key)

		// Read all data
		data, err := io.ReadAll(reader)
		_ = reader.Close()

		if err != nil {
			fmt.Printf("Warning: failed to read emoji data %s: %v\n", emoji.Shortcode, err)

			continue
		}

		// Create tar header
		filename := emoji.Shortcode + ext
		header := &tar.Header{
			Name:    filename,
			Mode:    0644,
			Size:    int64(len(data)),
			ModTime: time.Now(),
		}

		if err := tarWriter.WriteHeader(header); err != nil {
			fmt.Printf("Warning: failed to write header for %s: %v\n", emoji.Shortcode, err)

			continue
		}

		if _, err := tarWriter.Write(data); err != nil {
			fmt.Printf("Warning: failed to write data for %s: %v\n", emoji.Shortcode, err)

			continue
		}

		exported++
	}

	fmt.Printf("Exported %d emojis to %s\n", exported, outputPath)

	return nil
}

// EmojiImportCommand implements tootctl emoji import
// Imports custom emoji from a .tar.gz archive.
// Version history: 2.5.0 - added.
func EmojiImportCommand() *cli.Command {
	return &cli.Command{
		Name:      "import",
		Usage:     "Import custom emoji from a .tar.gz archive",
		ArgsUsage: "PATH",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "prefix",
				Usage: "Add prefix to generated shortcodes",
			},
			&cli.StringFlag{
				Name:  "suffix",
				Usage: "Add suffix to generated shortcodes",
			},
			&cli.BoolFlag{
				Name:  "overwrite",
				Usage: "Replace existing emoji with same shortcode",
			},
			&cli.StringFlag{
				Name:  "category",
				Usage: "Group processed emoji under CATEGORY",
			},
			&cli.BoolFlag{
				Name:  "unlisted", //nolint:goconst
				Usage: "Processed emoji will not be shown in the picker",
			},
		},
		Action: importEmoji,
	}
}

func importEmoji(c *cli.Context) error {
	if c.NArg() < 1 {
		return errors.New("PATH is required")
	}

	inputPath := c.Args().First()
	prefix := c.String("prefix")
	suffix := c.String("suffix")
	overwrite := c.Bool("overwrite")
	category := c.String("category")
	unlisted := c.Bool("unlisted")

	// Open input file
	file, err := os.Open(inputPath) //nolint:gosec // G304: path validated
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	imported := 0
	skipped := 0

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Get shortcode from filename
		filename := filepath.Base(header.Name)
		ext := filepath.Ext(filename)
		shortcode := strings.TrimSuffix(filename, ext)

		// Apply prefix and suffix
		if prefix != "" {
			shortcode = prefix + shortcode
		}

		if suffix != "" {
			shortcode += suffix
		}

		// Validate extension
		switch strings.ToLower(ext) {
		case ".png", ".gif", ".webp", ".jpg", ".jpeg":
		default:
			continue
		}

		if header.Size > emojiasset.MaxSize {
			fmt.Printf("Skipping %s: file too large (%d bytes)\n", shortcode, header.Size)

			continue
		}

		// Read file data
		data := make([]byte, header.Size)
		if _, err := io.ReadFull(tarReader, data); err != nil {
			fmt.Printf("Warning: failed to read %s: %v\n", shortcode, err)

			continue
		}

		result, err := importEmojiData(c.Context, shortcode, data, category, unlisted, overwrite)
		if err != nil {
			fmt.Printf("Warning: failed to import %s: %v\n", shortcode, err)

			continue
		}

		if result == emojiSkipped {
			fmt.Printf("Skipping %s: already exists\n", shortcode)

			skipped++

			continue
		}

		imported++

		fmt.Printf("Imported: %s\n", shortcode)
	}

	fmt.Printf("\nImported %d emojis, skipped %d\n", imported, skipped)

	return nil
}

// EmojiPurgeCommand implements tootctl emoji purge
// Remove all custom emojis.
// Version history: 2.8.0 - added.
func EmojiPurgeCommand() *cli.Command {
	return &cli.Command{
		Name:  "purge",
		Usage: "Remove all custom emojis",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "remote-only",
				Usage: "Remove only from remote domains",
			},
		},
		Action: purgeEmoji,
	}
}

func purgeEmoji(c *cli.Context) error {
	remoteOnly := c.Bool("remote-only")

	query := db.GetDB().Model(&models.Emoji{})
	if remoteOnly {
		query = query.Where("domain IS NOT NULL AND domain != ''")
	}

	var count int64
	query.Count(&count)

	if count == 0 {
		fmt.Println("No emojis to purge")

		return nil
	}

	fmt.Printf("Purging %d emojis...\n", count)

	// Get all emojis to delete
	var emojis []*models.Emoji
	err := query.Find(&emojis).Error
	if err != nil {
		return fmt.Errorf("failed to query emojis: %w", err)
	}

	// Delete in batches with concurrency
	batchSize := 100
	jobs := make(chan []*models.Emoji, 10)

	var wg sync.WaitGroup

	deleted := 0

	var mu sync.Mutex

	// Start workers
	workers := 5
	for range workers {
		wg.Go(func() {
			for batch := range jobs {
				for _, emoji := range batch {
					// Soft delete (like the admin API): the row can be
					// restored, so its stored image is kept. Remote emoji
					// images are hot-linked and have no stored file.
					if err := db.GetDB().Delete(emoji).Error; err != nil {
						fmt.Printf("Warning: failed to delete %s: %v\n", emoji.Shortcode, err)

						continue
					}
					mu.Lock()
					deleted++
					mu.Unlock()
				}
			}
		})
	}

	// Feed jobs
	for i := 0; i < len(emojis); i += batchSize {
		end := min(i+batchSize, len(emojis))

		jobs <- emojis[i:end]
	}

	close(jobs)

	wg.Wait()

	fmt.Printf("Purged %d emojis\n", deleted)

	return nil
}

type emojiImportResult int

const (
	emojiCreated emojiImportResult = iota
	emojiUpdated
	emojiSkipped
)

// importEmojiData validates and stores one emoji image under "emojis/" via
// emojiasset (the same path the admin API uses) and creates or updates the
// local emoji row.
func importEmojiData(ctx context.Context, shortcode string, data []byte, category string, unlisted, overwrite bool) (emojiImportResult, error) {
	gdb := db.GetDB().WithContext(ctx)

	var existing models.Emoji

	err := gdb.Where("shortcode = ? AND (domain = '' OR domain IS NULL)", shortcode).First(&existing).Error
	found := err == nil

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}

	if found && !overwrite {
		return emojiSkipped, nil
	}

	asset, err := emojiasset.StoreEmojiImage(ctx, bytes.NewReader(data), "")
	if err != nil {
		return 0, err
	}

	if !found {
		emoji := &models.Emoji{
			Shortcode:       shortcode,
			Url:             asset.URL,
			StaticUrl:       asset.StaticURL,
			Category:        category,
			VisibleInPicker: !unlisted,
			ContentType:     asset.ContentType,
			FileSize:        asset.Size,
			Width:           asset.Width,
			Height:          asset.Height,
		}
		if err := gdb.Create(emoji).Error; err != nil {
			_ = emojiasset.DeleteAsset(asset)

			return 0, err
		}

		return emojiCreated, nil
	}

	oldURL, oldStatic := existing.Url, existing.StaticUrl

	err = gdb.Model(&existing).Updates(map[string]any{
		"url":               asset.URL,
		"static_url":        asset.StaticURL,
		"category":          category,
		"visible_in_picker": !unlisted,
		"content_type":      asset.ContentType,
		"file_size":         asset.Size,
		"width":             asset.Width,
		"height":            asset.Height,
	}).Error
	if err != nil {
		_ = emojiasset.DeleteAsset(asset)

		return 0, err
	}

	deleteUnreferencedEmojiAssets(ctx, oldURL, oldStatic)

	return emojiUpdated, nil
}

// deleteUnreferencedEmojiAssets removes this instance's stored emoji files
// for the given URLs unless any emoji (including soft-deleted ones), pending
// emoji upload, avatar decoration or advertisement still points at them.
// Remote URLs are ignored.
func deleteUnreferencedEmojiAssets(ctx context.Context, urls ...string) {
	gdb := db.GetDB().WithContext(ctx)

	for _, u := range urls {
		key := emojiasset.StorageKey(u)
		if key == "" {
			continue
		}

		if emojiURLReferenced(gdb, u) {
			continue
		}

		if err := storage.Default().Delete(ctx, key); err != nil {
			fmt.Printf("Warning: failed to delete %s: %v\n", key, err)
		}
	}
}

func emojiURLReferenced(gdb *gorm.DB, u string) bool {
	checks := []struct {
		model any
		where string
		args  []any
	}{
		{&models.Emoji{}, "url = ? OR static_url = ?", []any{u, u}},
		{&models.EmojiUpload{}, "url = ? OR static_url = ?", []any{u, u}},
		{&models.AvatarDecoration{}, "image_url = ?", []any{u}},
		{&models.Advertisement{}, "image_url = ?", []any{u}},
	}

	for _, ch := range checks {
		if !gdb.Migrator().HasTable(ch.model) {
			continue
		}

		var n int64
		// Soft-deleted emoji keep their files so they can be restored.
		if err := gdb.Unscoped().Model(ch.model).Where(ch.where, ch.args...).Count(&n).Error; err != nil || n > 0 {
			return true
		}
	}

	return false
}

// GetEmojiStats returns emoji statistics.
func GetEmojiStats() (map[string]any, error) {
	var (
		total  int64
		local  int64
		remote int64
	)

	db.GetDB().Model(&models.Emoji{}).Count(&total)
	db.GetDB().Model(&models.Emoji{}).Where("domain IS NULL OR domain = ''").Count(&local)
	db.GetDB().Model(&models.Emoji{}).Where("domain IS NOT NULL AND domain != ''").Count(&remote)

	return map[string]any{
		"total":  total,
		"local":  local,
		"remote": remote,
	}, nil
}
