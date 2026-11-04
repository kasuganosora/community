package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// misskeyObjectPrefix is the object-storage key prefix Misskey has always
// written as "pubilc" (not "public"). Matching that path segment identifies
// those objects without naming a bucket or host.
const misskeyObjectPrefix = "pubilc"

// fetchLegacyEmojiAsset copies one remote emoji image into instance-owned
// /emoji-assets/ storage. Tests replace it; production uses the SSRF-checked
// importer.
var fetchLegacyEmojiAsset = emojiasset.ImportEmojiImage

// EmojiRehostCommand implements tootctl emoji rehost.
// Local custom emoji that still point at legacy object storage have no
// emojiasset.StorageKey, so Matrix group send refuses them. This copies the
// bytes into /emoji-assets/ and rewrites url and static_url. Rows that already
// have a StorageKey are skipped, so the command is safe to run again.
func EmojiRehostCommand() *cli.Command {
	return &cli.Command{
		Name:  "rehost",
		Usage: "Copy local custom emoji off legacy object storage into /emoji-assets/ (dry run unless --confirm)",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Only report what would change (the default; wins over --confirm)",
			},
			&cli.BoolFlag{
				Name:  "confirm",
				Usage: "Download the images, store them under /emoji-assets/, and rewrite url and static_url",
			},
		},
		Action: rehostEmojiCLI,
	}
}

func rehostEmojiCLI(c *cli.Context) error {
	write := c.Bool("confirm") && !c.Bool("dry-run")
	if write {
		if err := storage.Default().Check(c.Context); err != nil {
			return fmt.Errorf("storage backend unavailable: %w", err)
		}
	}
	return rehostLegacyEmoji(c.Context, db.GetDB().WithContext(c.Context), c.App.Writer, write, fetchLegacyEmojiAsset)
}

type emojiRehostStats struct {
	Already    int
	Candidates int
	Rehosted   int
	Failed     int
	Other      int
}

func rehostLegacyEmoji(ctx context.Context, conn *gorm.DB, out io.Writer, write bool, fetch func(context.Context, string) (*emojiasset.Asset, error)) error {
	if out == nil {
		out = io.Discard
	}
	if !config.AppBaseURLConfigured() {
		_, _ = fmt.Fprintf(out, "Warning: APP_BASE_URL is not set; new emoji URLs use %s\n", systemsettings.GetBaseURL())
	}
	mode := "DRY RUN (pass --confirm to copy into /emoji-assets/ and rewrite urls)"
	if write {
		mode = "COPYING into /emoji-assets/ and rewriting urls"
	}
	_, _ = fmt.Fprintf(out, "Local custom emoji on legacy object storage (path segment %q) — %s\n", misskeyObjectPrefix, mode)

	var rows []models.Emoji
	if err := conn.Where("domain = ? OR domain IS NULL", "").Order("shortcode ASC").Order("id ASC").Find(&rows).Error; err != nil {
		return fmt.Errorf("list local emoji: %w", err)
	}

	stats := emojiRehostStats{}
	var failures []string
	for _, emoji := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if emojiasset.StorageKey(emoji.Url) != "" {
			stats.Already++
			continue
		}
		sources := legacyEmojiSources(emoji)
		if len(sources) == 0 {
			stats.Other++
			_, _ = fmt.Fprintf(out, "  skip %s: not legacy object storage and not a local emoji asset\n", emoji.Shortcode)
			continue
		}
		stats.Candidates++
		if !write {
			_, _ = fmt.Fprintf(out, "  would rehost %s\n", emoji.Shortcode)
			continue
		}
		asset, err := fetchLegacyEmoji(ctx, fetch, sources)
		if err != nil {
			stats.Failed++
			failures = append(failures, emoji.Shortcode)
			_, _ = fmt.Fprintf(out, "  failed %s: %s\n", emoji.Shortcode, rehostFailureReason(err))
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = emojiasset.DeleteAsset(asset)
			return err
		}
		applied, err := applyRehostedEmoji(conn, emoji.Id, asset)
		if err != nil {
			_ = emojiasset.DeleteAsset(asset)
			stats.Failed++
			failures = append(failures, emoji.Shortcode)
			_, _ = fmt.Fprintf(out, "  failed %s: %s\n", emoji.Shortcode, rehostFailureReason(err))
			continue
		}
		if !applied {
			// Another run stored a local asset before this write.
			_ = emojiasset.DeleteAsset(asset)
			stats.Already++
			_, _ = fmt.Fprintf(out, "  skip %s: already stored locally\n", emoji.Shortcode)
			continue
		}
		stats.Rehosted++
		_, _ = fmt.Fprintf(out, "  rehosted %s\n", emoji.Shortcode)
	}

	_, _ = fmt.Fprintf(out, "Already local %d, candidates %d, rehosted %d, failed %d, left unchanged %d\n",
		stats.Already, stats.Candidates, stats.Rehosted, stats.Failed, stats.Other)
	_, _ = fmt.Fprintln(out, "Remote emoji were not modified.")
	if stats.Failed > 0 {
		return fmt.Errorf("%d emoji could not be copied into local storage: %s", stats.Failed, strings.Join(failures, ", "))
	}
	return nil
}

// legacyEmojiSources returns the legacy object-storage URLs to try, original
// first and a distinct static image second. An empty result means this row
// should be left alone.
func legacyEmojiSources(emoji models.Emoji) []string {
	var sources []string
	if legacyMisskeyObjectURL(emoji.Url) {
		sources = append(sources, emoji.Url)
	}
	if emoji.StaticUrl != emoji.Url && legacyMisskeyObjectURL(emoji.StaticUrl) {
		sources = append(sources, emoji.StaticUrl)
	}
	return sources
}

// legacyMisskeyObjectURL reports whether raw is an absolute http(s) URL whose
// path contains the historical object-storage prefix as its own segment.
// A name that merely contains those letters, or a file already under
// /emoji-assets/, does not match.
func legacyMisskeyObjectURL(raw string) bool {
	if emojiasset.StorageKey(raw) != "" {
		return false
	}
	path, err := legacyEmojiPath(raw)
	if err != nil {
		return false
	}
	return pathHasSegment(path, misskeyObjectPrefix)
}

func legacyEmojiPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		return "", errors.New("not an absolute URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("not an absolute http(s) URL")
	}
	return u.Path, nil
}

func fetchLegacyEmoji(ctx context.Context, fetch func(context.Context, string) (*emojiasset.Asset, error), sources []string) (*emojiasset.Asset, error) {
	var last error
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		asset, err := fetch(ctx, source)
		if err == nil {
			return asset, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("no legacy image URL")
	}
	return nil, last
}

// applyRehostedEmoji writes the new asset URLs when the row still has no
// local StorageKey. applied is false when a concurrent run already rewrote it.
func applyRehostedEmoji(conn *gorm.DB, id uint64, asset *emojiasset.Asset) (bool, error) {
	if asset == nil || emojiasset.StorageKey(asset.URL) == "" {
		return false, errors.New("rehosted emoji is not a local asset")
	}
	applied := false
	err := conn.Transaction(func(tx *gorm.DB) error {
		var current models.Emoji
		if err := tx.First(&current, id).Error; err != nil {
			return err
		}
		if emojiasset.StorageKey(current.Url) != "" {
			return nil
		}
		err := tx.Model(&current).Updates(map[string]any{
			"url":          asset.URL,
			"static_url":   asset.StaticURL,
			"content_type": asset.ContentType,
			"file_size":    asset.Size,
			"width":        asset.Width,
			"height":       asset.Height,
		}).Error
		if err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

func pathHasSegment(rawPath, want string) bool {
	for seg := range strings.SplitSeq(rawPath, "/") {
		if seg == want {
			return true
		}
	}
	return false
}

// rehostFailureReason keeps bucket hosts and signed query strings out of the
// operator log. Known validator messages do not include the URL.
func rehostFailureReason(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := err.Error()
	if strings.Contains(msg, "://") || strings.Contains(msg, misskeyObjectPrefix) {
		return "download or validation failed"
	}
	return msg
}
