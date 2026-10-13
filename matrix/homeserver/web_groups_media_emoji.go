package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	nethtml "golang.org/x/net/html"
	"gorm.io/gorm"
)

var webGroupEmojiImportMu sync.Mutex

// Only Matrix's explicit inline-emote markup is converted to an image DTO.
// Remote HTML is never returned by this parser or trusted as an image URL.
func webGroupInlineEmojis(roomID string, event MatrixEvent) []webGroupEmoji {
	if event.RoomID != roomID || event.Content["format"] != "org.matrix.custom.html" {
		return nil
	}
	body, _ := event.Content["formatted_body"].(string)
	if len(body) > 64<<10 {
		return nil
	}
	tokenizer := nethtml.NewTokenizer(strings.NewReader(body))
	seen := map[string]bool{}
	result := []webGroupEmoji{}
	for len(result) < 32 {
		kind := tokenizer.Next()
		if kind == nethtml.ErrorToken {
			break
		}
		if kind != nethtml.StartTagToken && kind != nethtml.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		if token.Data != "img" {
			continue
		}
		var src, alt string
		emote := false
		for _, attr := range token.Attr {
			switch attr.Key {
			case "src":
				src = attr.Val
			case "alt":
				alt = attr.Val
			case "data-mx-emoticon":
				emote = true
			}
		}
		shortcode := strings.Trim(alt, ":")
		if !emote || len(shortcode) == 0 || len(shortcode) > 36 || len(models.MatchEmojiTags(":"+shortcode+":")) != 1 || seen[shortcode] {
			continue
		}
		// Require the entire name to be a shortcode, not a matching substring.
		if models.MatchEmojiTags(":" + shortcode + ":")[0] != ":"+shortcode+":" {
			continue
		}
		url := webGroupMediaURL(roomID, event.EventID, src)
		if url == "" {
			continue
		}
		seen[shortcode] = true
		result = append(result, webGroupEmoji{Shortcode: shortcode, URL: url, StaticURL: url + "?thumbnail=1"})
	}
	return result
}

func webGroupEventReferencesMXC(roomID string, event MatrixEvent, mxc string) bool {
	if event.EventType == "m.reaction" {
		relation, _ := event.Content["m.relates_to"].(map[string]any)
		return relation["rel_type"] == "m.annotation" && relation["key"] == mxc
	}
	if value, ok := event.Content["url"].(string); ok && value == mxc {
		return true
	}
	url := webGroupMediaURL(roomID, event.EventID, mxc)
	if url == "" {
		return false
	}
	for _, emoji := range webGroupInlineEmojis(roomID, event) {
		if emoji.URL == url {
			return true
		}
	}
	return false
}

func webGroupEmojiMediaID(emoji models.Emoji) string {
	// Asset URLs contain an immutable UUID. Replacing the emoji creates a new
	// asset/ID; changing only its shortcode never creates another media object.
	digest := sha256.Sum256([]byte(emoji.Url))
	return "emoji_" + hex.EncodeToString(digest[:20])
}

// webGroupEmojiMXC copies only a validated, instance-owned emoji asset into the
// Matrix repository. No URL supplied by the browser or a remote emoji is ever
// fetched here. Stable media IDs share the same asset across messages/nodes.
func webGroupEmojiMXC(ctx context.Context, hs *HomeServer, emoji models.Emoji) (string, error) {
	conn := db.GetDB().WithContext(ctx)
	if err := conn.Where("id = ? AND domain = '' AND local_only = ? AND disabled = ?", emoji.Id, false, false).First(&emoji).Error; err != nil {
		return "", errors.New("custom emoji is not available for federation")
	}
	key := emojiasset.StorageKey(emoji.Url)
	if key == "" {
		return "", errors.New("custom emoji is not an instance-owned asset")
	}
	mediaID := webGroupEmojiMediaID(emoji)
	mxc := "mxc://" + hs.Config.ServerName + "/" + mediaID
	webGroupEmojiImportMu.Lock()
	defer webGroupEmojiImportMu.Unlock()
	var exists MatrixMedia
	err := conn.Where("server_name = ? AND media_id = ?", hs.Config.ServerName, mediaID).First(&exists).Error
	if err == nil {
		return mxc, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	reader, _, err := storage.Default().Open(ctx, key)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	staged, err := os.CreateTemp("", "community-group-emoji-*")
	if err != nil {
		return "", err
	}
	defer staged.Close()
	defer os.Remove(staged.Name())
	n, err := io.Copy(staged, io.LimitReader(reader, emojiasset.MaxSize+1))
	if err != nil {
		return "", err
	}
	if n == 0 || n > emojiasset.MaxSize {
		return "", errors.New("custom emoji exceeds the media size limit")
	}
	typ, err := inspectWebGroupUpload(staged)
	if err != nil || !webGroupRasterType(typ) {
		return "", errors.New("custom emoji image is invalid")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	media, err := NewMediaService(hs).UploadMedia(staged, &multipart.FileHeader{Filename: path.Base(key), Size: n}, typ, "_community_emoji")
	if err != nil {
		return "", err
	}
	originalID := media.MediaID
	err = conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&MatrixMedia{}).Where("id = ?", media.ID).Update("media_id", mediaID).Error; err != nil {
			return err
		}
		return tx.Model(&MatrixThumbnail{}).Where("media_id = ?", originalID).Update("media_id", mediaID).Error
	})
	if err != nil {
		_ = NewMediaService(hs).DeleteMedia(hs.Config.ServerName, originalID)
		// Another node may have won the stable-ID insert. Its independently
		// validated bytes are equivalent; never remove that winning record.
		if conn.Where("server_name = ? AND media_id = ?", hs.Config.ServerName, mediaID).First(&exists).Error == nil {
			return mxc, nil
		}
		return "", err
	}
	return mxc, nil
}
