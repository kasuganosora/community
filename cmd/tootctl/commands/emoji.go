package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/urfave/cli/v2"
)

// EmojiCommand implements tootctl emoji subcommands
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
// Version history: 3.1.4 - added
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
		return fmt.Errorf("PATH is required")
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
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	exported := 0
	for _, emoji := range emojis {
		// Get the image file
		if emoji.Url == "" {
			continue
		}

		// Read the image data from storage using Download
		reader, err := storage.GetStorage().Download(emoji.Url)
		if err != nil {
			fmt.Printf("Warning: failed to read emoji %s: %v\n", emoji.Shortcode, err)
			continue
		}

		// Determine file extension
		ext := ".png"
		if strings.Contains(emoji.Url, ".gif") {
			ext = ".gif"
		}

		// Read all data
		data, err := io.ReadAll(reader)
		reader.Close()
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
// Version history: 2.5.0 - added
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
				Name:  "unlisted",
				Usage: "Processed emoji will not be shown in the picker",
			},
		},
		Action: importEmoji,
	}
}

func importEmoji(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("PATH is required")
	}

	inputPath := c.Args().First()
	prefix := c.String("prefix")
	suffix := c.String("suffix")
	overwrite := c.Bool("overwrite")
	category := c.String("category")
	unlisted := c.Bool("unlisted")

	// Open input file
	file, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

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
			shortcode = shortcode + suffix
		}

		// Validate extension
		if ext != ".png" && ext != ".gif" {
			continue
		}

		// Check file size (max 50KB)
		if header.Size > 50*1024 {
			fmt.Printf("Skipping %s: file too large (%d bytes)\n", shortcode, header.Size)
			continue
		}

		// Read file data
		data := make([]byte, header.Size)
		if _, err := io.ReadFull(tarReader, data); err != nil {
			fmt.Printf("Warning: failed to read %s: %v\n", shortcode, err)
			continue
		}

		// Check if emoji exists
		var existing models.Emoji
		err = db.GetDB().Where("shortcode = ?", shortcode).First(&existing).Error
		if err == nil {
			if !overwrite {
				fmt.Printf("Skipping %s: already exists\n", shortcode)
				skipped++
				continue
			}
			// Update existing
			existing.Url = "imported/" + shortcode + ext
			existing.Category = category
			existing.VisibleInPicker = !unlisted
			if err := db.GetDB().Save(&existing).Error; err != nil {
				fmt.Printf("Warning: failed to update %s: %v\n", shortcode, err)
				continue
			}
		} else {
			// Create new
			emoji := &models.Emoji{
				Shortcode:       shortcode,
				Url:             "imported/" + shortcode + ext,
				Category:        category,
				VisibleInPicker: !unlisted,
			}
			if err := db.GetDB().Create(emoji).Error; err != nil {
				fmt.Printf("Warning: failed to create %s: %v\n", shortcode, err)
				continue
			}
		}

		// Save to storage (support both local and cloud storage)
		objectKey := shortcode + ext
		if cloudStorage := storage.GetStorage(); cloudStorage.IsEnabled() {
			if _, err := cloudStorage.UploadReader(bytes.NewReader(data), objectKey, int64(len(data))); err != nil {
				fmt.Printf("Warning: failed to save %s to cloud: %v\n", shortcode, err)
				continue
			}
		} else {
			if _, err := storage.Save(objectKey, data); err != nil {
				fmt.Printf("Warning: failed to save %s: %v\n", shortcode, err)
				continue
			}
		}

		imported++
		fmt.Printf("Imported: %s\n", shortcode)
	}

	fmt.Printf("\nImported %d emojis, skipped %d\n", imported, skipped)
	return nil
}

// EmojiPurgeCommand implements tootctl emoji purge
// Remove all custom emojis.
// Version history: 2.8.0 - added
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
	if err := query.Find(&emojis).Error; err != nil {
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
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				for _, emoji := range batch {
					// Delete from storage
					if emoji.Url != "" {
						_ = storage.Delete(emoji.Url)
					}
					// Delete from database
					db.GetDB().Delete(emoji)
					mu.Lock()
					deleted++
					mu.Unlock()
				}
			}
		}()
	}

	// Feed jobs
	for i := 0; i < len(emojis); i += batchSize {
		end := i + batchSize
		if end > len(emojis) {
			end = len(emojis)
		}
		jobs <- emojis[i:end]
	}
	close(jobs)

	wg.Wait()

	fmt.Printf("Purged %d emojis\n", deleted)
	return nil
}

// GetEmojiStats returns emoji statistics
func GetEmojiStats() (map[string]interface{}, error) {
	var total int64
	var local int64
	var remote int64

	db.GetDB().Model(&models.Emoji{}).Count(&total)
	db.GetDB().Model(&models.Emoji{}).Where("domain IS NULL OR domain = ''").Count(&local)
	db.GetDB().Model(&models.Emoji{}).Where("domain IS NOT NULL AND domain != ''").Count(&remote)

	return map[string]interface{}{
		"total":  total,
		"local":  local,
		"remote": remote,
	}, nil
}
