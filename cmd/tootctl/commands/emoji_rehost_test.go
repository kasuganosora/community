package commands

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"

	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

func TestLegacyMisskeyObjectURLMatchesPrefixSegmentOnly(t *testing.T) {
	yes := []string{
		"https://files.example/bucket/pubilc/wave.webp",
		"http://files.example/pubilc/wave.webp",
		"https://files.example/bucket/pubilc/wave.webp?token=1",
		"https://files.example//pubilc//wave.webp",
	}
	no := []string{
		"",
		"pubilc/wave.webp",
		"https://files.example/public/wave.webp",
		"https://files.example/pubilc-backup/wave.webp",
		"https://files.example/bucket/wave.webp",
		"https://user:secret@files.example/pubilc/wave.webp",
		"https://files.example/pubilc/wave.webp#frag",
		"file:///pubilc/wave.webp",
		testBaseURL + "/emoji-assets/11111111-1111-1111-1111-111111111111.png",
	}
	for _, raw := range yes {
		require.True(t, legacyMisskeyObjectURL(raw), raw)
	}
	for _, raw := range no {
		require.False(t, legacyMisskeyObjectURL(raw), raw)
	}
}

func TestEmojiRehostDryRunWritesNothing(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))

	legacy := "https://files.example/bucket/pubilc/wave.webp"
	require.NoError(t, gdb.Create(&models.Emoji{
		Shortcode: "wave", Url: legacy, StaticUrl: legacy, VisibleInPicker: true,
	}).Error)
	require.NoError(t, gdb.Create(&models.Emoji{
		Shortcode: "wave", Domain: "remote.example", Url: legacy, StaticUrl: legacy,
	}).Error)

	out, err := runEmojiCLI(t, "rehost")
	require.NoError(t, err)
	require.Contains(t, out, "DRY RUN")
	require.Contains(t, out, "would rehost wave")
	require.Contains(t, out, "candidates 1")
	require.NotContains(t, out, "files.example")
	require.Empty(t, backend.Keys())
	require.Equal(t, legacy, reloadEmoji(t, gdb, "wave", "").Url)

	out, err = runEmojiCLI(t, "rehost", "--confirm", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "DRY RUN")
	require.Equal(t, legacy, reloadEmoji(t, gdb, "wave", "").Url)
	require.Equal(t, legacy, reloadEmoji(t, gdb, "wave", "remote.example").Url)
}

func TestEmojiRehostCopiesLegacyURLAndIsIdempotent(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))

	original := "https://files.example/bucket/pubilc/animated.webp"
	still := "https://files.example/bucket/pubilc/still.webp"
	require.NoError(t, gdb.Create(&models.Emoji{
		Shortcode: "wave", Url: original, StaticUrl: still, VisibleInPicker: true,
	}).Error)
	require.NoError(t, gdb.Create(&models.Emoji{
		Shortcode: "hotlink", Url: "https://cdn.example/emoji/hot.png", StaticUrl: "https://cdn.example/emoji/hot.png",
	}).Error)
	require.NoError(t, gdb.Create(&models.Emoji{
		Shortcode: "spelled", Url: "https://files.example/public/nope.png", StaticUrl: "https://files.example/public/nope.png",
	}).Error)
	require.NoError(t, gdb.Create(&models.Emoji{
		Shortcode: "off", Disabled: true, Url: "https://files.example/bucket/pubilc/off.png", StaticUrl: "https://files.example/bucket/pubilc/off.png",
	}).Error)
	require.NoError(t, gdb.Create(&models.Emoji{
		Shortcode: "remote_wave", Domain: "remote.example", Url: original, StaticUrl: original,
	}).Error)
	_, err := importEmojiData(context.Background(), "blob", pngBytes(t, color.White), "", false, false)
	require.NoError(t, err)
	kept := reloadEmoji(t, gdb, "blob", "")

	var calls []string
	restore := stubEmojiFetch(t, func(ctx context.Context, raw string) (*emojiasset.Asset, error) {
		calls = append(calls, raw)
		if strings.HasSuffix(raw, "/animated.webp") {
			return nil, errors.New(`Get "https://files.example/bucket/pubilc/animated.webp": connection refused`)
		}
		return emojiasset.StoreEmojiImage(ctx, bytes.NewReader(pngBytes(t, color.White)), "image/png")
	})
	defer restore()

	out, err := runEmojiCLI(t, "rehost", "--confirm")
	require.NoError(t, err)
	require.Contains(t, out, "rehosted wave")
	require.Contains(t, out, "rehosted off")
	require.Contains(t, out, "skip hotlink")
	require.Contains(t, out, "skip spelled")
	require.NotContains(t, out, "files.example")
	require.Equal(t, []string{
		"https://files.example/bucket/pubilc/off.png",
		original,
		still,
	}, calls)

	wave := reloadEmoji(t, gdb, "wave", "")
	require.NotEqual(t, original, wave.Url)
	require.NotEqual(t, still, wave.StaticUrl)
	require.NotEmpty(t, emojiasset.StorageKey(wave.Url))
	require.NotEmpty(t, emojiasset.StorageKey(wave.StaticUrl))
	require.True(t, strings.HasPrefix(wave.Url, testBaseURL+"/emoji-assets/"), wave.Url)
	require.True(t, strings.HasPrefix(wave.StaticUrl, testBaseURL+"/emoji-assets/"), wave.StaticUrl)
	require.NotEqual(t, wave.Url, wave.StaticUrl)
	require.Equal(t, "image/png", wave.ContentType)
	require.Positive(t, wave.FileSize)
	require.Positive(t, wave.Width)

	off := reloadEmoji(t, gdb, "off", "")
	require.NotEmpty(t, emojiasset.StorageKey(off.Url))
	require.True(t, off.Disabled)

	require.Equal(t, "https://cdn.example/emoji/hot.png", reloadEmoji(t, gdb, "hotlink", "").Url)
	require.Equal(t, "https://files.example/public/nope.png", reloadEmoji(t, gdb, "spelled", "").Url)
	require.Equal(t, original, reloadEmoji(t, gdb, "remote_wave", "remote.example").Url)
	require.Equal(t, kept.Url, reloadEmoji(t, gdb, "blob", "").Url)

	calls = nil
	out, err = runEmojiCLI(t, "rehost", "--confirm")
	require.NoError(t, err)
	require.Empty(t, calls)
	require.Contains(t, out, "candidates 0")
	require.Equal(t, wave.Url, reloadEmoji(t, gdb, "wave", "").Url)
}

func TestEmojiRehostKeepsRowWhenDownloadFails(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))

	legacy := "https://files.example/bucket/pubilc/missing.webp"
	require.NoError(t, gdb.Create(&models.Emoji{Shortcode: "missing", Url: legacy, StaticUrl: legacy}).Error)
	restore := stubEmojiFetch(t, func(_ context.Context, raw string) (*emojiasset.Asset, error) {
		return nil, errors.New("Get " + raw + ": image server returned HTTP 404")
	})
	defer restore()

	out, err := runEmojiCLI(t, "rehost", "--confirm")
	require.ErrorContains(t, err, "missing")
	require.Contains(t, out, "failed missing: download or validation failed")
	require.NotContains(t, out, "files.example")
	require.Equal(t, legacy, reloadEmoji(t, gdb, "missing", "").Url)
	require.Empty(t, backend.Keys())
}

func TestEmojiRehostDropsAssetWhenRowDisappears(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))

	legacy := "https://files.example/bucket/pubilc/gone.webp"
	row := models.Emoji{Shortcode: "gone", Url: legacy, StaticUrl: legacy}
	require.NoError(t, gdb.Create(&row).Error)
	restore := stubEmojiFetch(t, func(ctx context.Context, _ string) (*emojiasset.Asset, error) {
		require.NoError(t, gdb.Delete(&models.Emoji{}, row.Id).Error)
		return emojiasset.StoreEmojiImage(ctx, bytes.NewReader(pngBytes(t, color.White)), "image/png")
	})
	defer restore()

	out, err := runEmojiCLI(t, "rehost", "--confirm")
	require.Error(t, err)
	require.Contains(t, out, "failed gone")
	require.Empty(t, backend.Keys(), "asset stored for a missing row must be removed")
}

func runEmojiCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	app := &cli.App{Name: "tootctl", Commands: []*cli.Command{EmojiCommand()}, Writer: &out, ErrWriter: &out}
	err := app.Run(append([]string{"tootctl", "emoji"}, args...))

	return out.String(), err
}

func stubEmojiFetch(t *testing.T, fetch func(context.Context, string) (*emojiasset.Asset, error)) func() {
	t.Helper()

	prev := fetchLegacyEmojiAsset
	fetchLegacyEmojiAsset = fetch

	return func() { fetchLegacyEmojiAsset = prev }
}

func reloadEmoji(t *testing.T, gdb *gorm.DB, shortcode, domain string) models.Emoji {
	t.Helper()

	var emoji models.Emoji
	require.NoError(t, gdb.Where("shortcode = ? AND domain = ?", shortcode, domain).First(&emoji).Error)

	return emoji
}
