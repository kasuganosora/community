// Package defaultavatar renders public, deterministic account avatars without storage.
package defaultavatar

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const Size = 128
const MediaPrefix = "default_avatar_"

// Seed preserves the username's case; only the DNS identity is case-insensitive.
func Seed(username, domain string) string {
	sum := sha256.Sum256([]byte(username + "@" + strings.ToLower(domain)))
	return hex.EncodeToString(sum[:])
}
func ValidSeed(seed string) bool {
	if len(seed) != 64 || strings.ToLower(seed) != seed {
		return false
	}
	_, err := hex.DecodeString(seed)
	return err == nil
}
func Path(seed string) string { return "/api/v1/avatars/default/" + seed + ".png" }

// PNG uses 15 seed bits to mirror five rows of three cells. Colors are bounded
// for readable contrast, with a fixed light background and twelve pixel padding.
func PNG(seed string) ([]byte, error) {
	if !ValidSeed(seed) {
		return nil, errors.New("invalid avatar seed")
	}
	b, _ := hex.DecodeString(seed)
	img := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{240, 243, 248, 255}}, image.Point{}, draw.Src)
	fg := &image.Uniform{color.RGBA{48 + b[0]%128, 48 + b[1]%128, 48 + b[2]%128, 255}}
	for y := 0; y < 5; y++ {
		for x := 0; x < 3; x++ {
			bit := y*3 + x
			if !(x == 2 && y == 2) && (b[3+bit/8]>>uint(bit%8))&1 == 0 {
				continue
			}
			for _, col := range []int{x, 4 - x} {
				draw.Draw(img, image.Rect(14+col*20, 14+y*20, 34+col*20, 34+y*20), fg, image.Point{}, draw.Src)
			}
		}
	}
	var out bytes.Buffer
	err := png.Encode(&out, img)
	return out.Bytes(), err
}
func Serve(c *gin.Context) {
	file := c.Param("seed")
	seed, ok := strings.CutSuffix(file, ".png")
	if !ok || !ValidSeed(seed) {
		c.Status(http.StatusNotFound)
		return
	}
	etag := "\"avatar-v1-" + seed + "\""
	c.Header("ETag", etag)
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	data, err := PNG(seed)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "image/png", data)
}

// IsPlaceholder recognizes only the application's built-in legacy asset, not
// an uploaded image which happens to depict the same logo.
func IsPlaceholder(raw, baseURL string) bool {
	return raw == "" || raw == "/default-assets/avatar.png" || raw == strings.TrimRight(baseURL, "/")+"/default-assets/avatar.png"
}
