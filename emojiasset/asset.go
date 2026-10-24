// Package emojiasset validates emoji images and keeps both original animation
// and a decoded static preview in instance-owned storage, never as hotlinks.
package emojiasset

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	_ "golang.org/x/image/webp"
)

const (
	MaxSize            int64 = 2 * 1024 * 1024
	MaxDimension             = 1024
	maxFrames                = 128
	maxAnimationPixels       = 16 * 1024 * 1024
)

type Asset struct {
	URL         string `json:"url"`
	StaticURL   string `json:"static_url"`
	ContentType string `json:"content_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Size        int64  `json:"size"`
}

// StoreEmojiImage validates declared MIME, actual decoding, dimensions and
// animation budgets before creating either local file. An empty/octet-stream
// MIME is accepted only when the actual bytes decode to a supported image.
func StoreEmojiImage(ctx context.Context, reader io.Reader, declaredType string) (*Asset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, errors.New("image is required")
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxSize {
		return nil, errors.New("emoji image exceeds 2 MiB")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	preview, format, width, height, err := decodeImage(data, declaredType)
	if err != nil {
		return nil, err
	}
	var static bytes.Buffer
	if err = png.Encode(&static, preview); err != nil {
		return nil, fmt.Errorf("encode emoji preview: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	ext := map[string]string{"png": "png", "jpeg": "jpg", "gif": "gif", "webp": "webp"}[format]
	name := uuid.NewString()
	key := "emojis/" + name + "." + ext
	staticKey := "emojis/" + name + "-static.png"
	// Emoji images go through the configured storage backend (local
	// uploads/emojis or S3 "emojis/" keys) and are always served by Serve.
	backend := storage.Default()
	if err = backend.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/"+format); err != nil {
		_ = backend.Delete(context.Background(), key)
		return nil, err
	}
	if err = backend.Put(ctx, staticKey, bytes.NewReader(static.Bytes()), int64(static.Len()), "image/png"); err != nil {
		_ = backend.Delete(context.Background(), key)
		_ = backend.Delete(context.Background(), staticKey)
		return nil, err
	}
	asset := &Asset{URL: systemsettings.GetBaseURL() + "/emoji-assets/" + path.Base(key), StaticURL: systemsettings.GetBaseURL() + "/emoji-assets/" + path.Base(staticKey), ContentType: "image/" + format, Width: width, Height: height, Size: int64(len(data))}
	if err = ctx.Err(); err != nil {
		_ = DeleteAsset(asset)
		return nil, err
	}
	return asset, nil
}

func ImportEmojiImage(ctx context.Context, remoteURL string) (*Asset, error) {
	data, contentType, err := activitypub.FetchRemoteMedia(ctx, remoteURL, MaxSize)
	if err != nil {
		return nil, err
	}
	return StoreEmojiImage(ctx, bytes.NewReader(data), contentType)
}

// DecodeLimits bounds canvas size and total decoded animation work separately
// from an endpoint's encoded-byte quota. Values must be positive.
type DecodeLimits struct {
	MaxDimension       int
	MaxPixels          int
	MaxFrames          int
	MaxAnimationPixels int
}

var emojiDecodeLimits = DecodeLimits{MaxDimension, MaxDimension * MaxDimension, maxFrames, maxAnimationPixels}

func decodeImage(data []byte, declared string) (image.Image, string, int, int, error) {
	return DecodeImage(data, declared, emojiDecodeLimits)
}

// DecodeImage validates every animation frame and returns its static first frame.
// It neither reads remote URLs nor writes files.
func DecodeImage(data []byte, declared string, limits DecodeLimits) (image.Image, string, int, int, error) {
	fail := func(err error) (image.Image, string, int, int, error) { return nil, "", 0, 0, err }
	if declared != "" {
		mediaType, _, err := mime.ParseMediaType(declared)
		if err != nil {
			return fail(errors.New("invalid image content type"))
		}
		switch mediaType {
		case "application/octet-stream", "image/png", "image/apng", "image/jpeg", "image/gif", "image/webp":
		default:
			return fail(errors.New("unsupported emoji image content type"))
		}
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fail(fmt.Errorf("invalid or unsupported emoji image: %w", err))
	}
	if format != "png" && format != "jpeg" && format != "gif" && format != "webp" {
		return fail(errors.New("unsupported emoji image format"))
	}
	if config.Width < 1 || config.Height < 1 || config.Width > limits.MaxDimension || config.Height > limits.MaxDimension || int64(config.Width)*int64(config.Height) > int64(limits.MaxPixels) {
		return fail(errors.New("image dimensions exceed the decoded-pixel limit"))
	}
	if declared != "" {
		mediaType, _, _ := mime.ParseMediaType(declared)
		if mediaType != "application/octet-stream" && mediaType != "image/"+format && !(format == "png" && mediaType == "image/apng") {
			return fail(errors.New("image content type does not match its bytes"))
		}
	}
	var decoded image.Image
	if format == "gif" {
		if err := validateGIFBudgetWithLimits(data, limits); err != nil {
			return fail(err)
		}
		animation, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return fail(fmt.Errorf("invalid GIF: %w", err))
		}
		canvas := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
		draw.Draw(canvas, animation.Image[0].Bounds(), animation.Image[0], animation.Image[0].Bounds().Min, draw.Src)
		decoded = canvas
	} else if format == "png" {
		decoded, err = decodePNG(data, config.Width, config.Height, limits)
		if err != nil {
			return fail(err)
		}
	} else if format == "webp" {
		decoded, err = decodeWebP(data, config.Width, config.Height, limits)
		if err != nil {
			return fail(err)
		}
	} else {
		decoded, _, err = image.Decode(bytes.NewReader(data))
		if err != nil {
			return fail(fmt.Errorf("invalid emoji image: %w", err))
		}
	}
	return decoded, format, config.Width, config.Height, nil
}

// GIF frame budgets are checked before DecodeAll allocates per-frame images.
func validateGIFBudget(data []byte) error {
	return validateGIFBudgetWithLimits(data, emojiDecodeLimits)
}
func validateGIFBudgetWithLimits(data []byte, limits DecodeLimits) error {
	if len(data) < 13 {
		return errors.New("truncated GIF")
	}
	offset := 13
	if data[10]&0x80 != 0 {
		offset += 3 * (1 << ((data[10] & 7) + 1))
	}
	frames, pixels := 0, 0
	for offset < len(data) {
		kind := data[offset]
		offset++
		switch kind {
		case 0x3b:
			if frames == 0 {
				return errors.New("GIF contains no frames")
			}
			return nil
		case 0x21:
			if offset >= len(data) {
				return errors.New("truncated GIF extension")
			}
			offset++
		case 0x2c:
			if offset+9 > len(data) {
				return errors.New("truncated GIF frame")
			}
			width, height := int(binary.LittleEndian.Uint16(data[offset+4:])), int(binary.LittleEndian.Uint16(data[offset+6:]))
			frames++
			pixels += width * height
			if width < 1 || height < 1 || width > limits.MaxDimension || height > limits.MaxDimension || frames > limits.MaxFrames || pixels > limits.MaxAnimationPixels {
				return errors.New("GIF animation exceeds frame or decoded-pixel budget")
			}
			packed := data[offset+8]
			offset += 9
			if packed&0x80 != 0 {
				offset += 3 * (1 << ((packed & 7) + 1))
			}
			offset++ // LZW minimum code size
		default:
			return errors.New("invalid GIF block")
		}
		for {
			if offset >= len(data) {
				return errors.New("truncated GIF data")
			}
			n := int(data[offset])
			offset++
			if n == 0 {
				break
			}
			offset += n
			if offset > len(data) {
				return errors.New("truncated GIF data")
			}
		}
	}
	return errors.New("GIF trailer missing")
}

var assetName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(-static)?\.(png|jpg|gif|webp)$`)

// StorageKey returns the storage key ("emojis/<name>") of an asset URL that
// StoreEmojiImage generated on this instance, or "" for anything else
// (remote emoji, foreign hosts, unexpected names).
func StorageKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme+"://"+u.Host != systemsettings.GetBaseURL() || u.RawQuery != "" || u.Fragment != "" || u.User != nil || !strings.HasPrefix(u.Path, "/emoji-assets/") || !assetName.MatchString(strings.TrimPrefix(u.Path, "/emoji-assets/")) {
		return ""
	}
	return "emojis/" + path.Base(u.Path)
}

// ReferencedKey maps any stored emoji/brand image reference (a local or
// foreign "/emoji-assets/<name>" URL or path, or a bare "emojis/<name>" key)
// to the storage key it may point at. It is deliberately lenient and is
// meant for building "is this object referenced" sets, never for deleting.
func ReferencedKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	p := raw
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		p = u.Path
	}
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimPrefix(p, "uploads/")
	switch {
	case strings.HasPrefix(p, "emoji-assets/"):
		return "emojis/" + path.Base(p)
	case strings.HasPrefix(p, "emojis/"):
		return p
	}
	return ""
}

// DeleteAsset removes only this instance's generated emoji filenames. It is
// intended for rolling back a newly uploaded asset, not deleting shared assets.
func DeleteAsset(asset *Asset) error {
	if asset == nil {
		return nil
	}
	var errs []error
	for _, raw := range []string{asset.URL, asset.StaticURL} {
		key := StorageKey(raw)
		if key == "" {
			errs = append(errs, errors.New("not a local emoji asset"))
			continue
		}
		err := storage.Default().Delete(context.Background(), key)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Serve exposes only generated image filenames, never arbitrary uploads.
func Serve(c *gin.Context) {
	name := c.Param("file")
	if !assetName.MatchString(name) {
		c.Status(http.StatusNotFound)
		return
	}
	kind := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[path.Ext(name)]
	// The filename is a UUID minted once per upload, so this URL's bytes never
	// change. A year plus immutable is the long-term image cache; ServeContent
	// adds an ETag for revalidators that ignore immutable.
	ServeObject(c, "emojis/"+name, name, kind, "public, max-age=31536000, immutable")
}

// ServeObject streams a stored emoji object with fixed headers. Missing
// objects answer 404, backend failures 503.
func ServeObject(c *gin.Context, key, name, contentType, cacheControl string) {
	rc, info, err := storage.Default().Open(c.Request.Context(), key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.Status(http.StatusNotFound)
		} else {
			c.Status(http.StatusServiceUnavailable)
		}
		return
	}
	defer func() { _ = rc.Close() }()
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	if cacheControl != "" {
		c.Header("Cache-Control", cacheControl)
	}
	// Non-seekable backends never reach ServeContent, which is what would
	// otherwise mint the validator. The name is the content identity.
	if name != "" {
		c.Header("ETag", `"`+name+`"`)
	}
	if rs, ok := rc.(io.ReadSeeker); ok {
		http.ServeContent(c.Writer, c.Request, name, info.ModTime, rs)
		return
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, rc)
}
