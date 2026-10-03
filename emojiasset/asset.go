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
	"time"

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
	local := &storage.LocalStorage{Root: "uploads"}
	if _, err = local.Save(key, data); err != nil {
		_ = local.Delete(key)
		return nil, err
	}
	if _, err = local.Save(staticKey, static.Bytes()); err != nil {
		_ = local.Delete(key)
		_ = local.Delete(staticKey)
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

func decodeImage(data []byte, declared string) (image.Image, string, int, int, error) {
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
	if config.Width < 1 || config.Height < 1 || config.Width > MaxDimension || config.Height > MaxDimension {
		return fail(errors.New("emoji dimensions must be between 1 and 1024 pixels"))
	}
	if declared != "" {
		mediaType, _, _ := mime.ParseMediaType(declared)
		if mediaType != "application/octet-stream" && mediaType != "image/"+format && !(format == "png" && mediaType == "image/apng") {
			return fail(errors.New("image content type does not match its bytes"))
		}
	}
	var decoded image.Image
	if format == "gif" {
		if err := validateGIFBudget(data); err != nil {
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
		decoded, err = decodePNG(data, config.Width, config.Height)
		if err != nil {
			return fail(err)
		}
	} else if format == "webp" {
		decoded, err = decodeWebP(data, config.Width, config.Height)
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
			if width < 1 || height < 1 || width > MaxDimension || height > MaxDimension || frames > maxFrames || pixels > maxAnimationPixels {
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

// DeleteAsset removes only this instance's generated emoji filenames. It is
// intended for rolling back a newly uploaded asset, not deleting shared assets.
func DeleteAsset(asset *Asset) error {
	if asset == nil {
		return nil
	}
	var errs []error
	for _, raw := range []string{asset.URL, asset.StaticURL} {
		u, err := url.Parse(raw)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if u.Scheme+"://"+u.Host != systemsettings.GetBaseURL() || u.RawQuery != "" || u.Fragment != "" || u.User != nil || !strings.HasPrefix(u.Path, "/emoji-assets/") || !assetName.MatchString(strings.TrimPrefix(u.Path, "/emoji-assets/")) {
			errs = append(errs, errors.New("not a local emoji asset"))
			continue
		}
		err = (&storage.LocalStorage{Root: "uploads"}).Delete("emojis/" + path.Base(u.Path))
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
	reader, err := (&storage.LocalStorage{Root: "uploads"}).Open("emojis/" + name)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	kind := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[path.Ext(name)]
	c.Header("Content-Type", kind)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(c.Writer, c.Request, name, time.Time{}, reader)
}
