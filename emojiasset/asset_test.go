package emojiasset

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func assetWorkspace(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	old, configured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://emoji.example", true)
	t.Cleanup(func() { config.SetAppBaseURL(old, configured) })
}
func samplePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, w, h))))
	return buf.Bytes()
}
func localBytes(t *testing.T, raw string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("uploads", "emojis", filepath.Base(raw)))
	require.NoError(t, err)
	return data
}

func TestStoreValidatedFormatsAndStaticPreviews(t *testing.T) {
	fixtures := map[string][]byte{}
	for _, name := range []string{"animated.png", "default-plus-animation.png", "animated.webp"} {
		b, err := os.ReadFile("testdata/" + name)
		require.NoError(t, err)
		fixtures[name] = b
	}
	plain := samplePNG(t, 3, 2)
	var jpegBytes bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil))
	palette := color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}}
	first := image.NewPaletted(image.Rect(0, 0, 3, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 3, 2), palette)
	first.SetColorIndex(0, 0, 1)
	second.SetColorIndex(0, 0, 2)
	var gifBytes bytes.Buffer
	require.NoError(t, gif.EncodeAll(&gifBytes, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{5, 5}}))
	webp, err := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvAAAAAAcQ/Y/+ByKi/wEA")
	require.NoError(t, err)
	cases := []struct {
		name, mime string
		data       []byte
		firstGreen bool
	}{{"png", "image/png", plain, false}, {"jpeg", "image/jpeg", jpegBytes.Bytes(), false}, {"gif", "image/gif", gifBytes.Bytes(), false}, {"webp", "image/webp", webp, false}, {"apng", "image/apng", fixtures["animated.png"], false}, {"apng-default-excluded", "image/png", fixtures["default-plus-animation.png"], true}, {"animated-webp", "image/webp", fixtures["animated.webp"], false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assetWorkspace(t)
			a, err := StoreEmojiImage(context.Background(), bytes.NewReader(tc.data), tc.mime)
			require.NoError(t, err)
			require.Equal(t, int64(len(tc.data)), a.Size)
			require.Equal(t, tc.data, localBytes(t, a.URL), "original animation preserved")
			require.NotEqual(t, a.URL, a.StaticURL)
			preview, err := png.Decode(bytes.NewReader(localBytes(t, a.StaticURL)))
			require.NoError(t, err)
			require.Equal(t, a.Width, preview.Bounds().Dx())
			require.Equal(t, a.Height, preview.Bounds().Dy())
			if tc.firstGreen {
				r, g, _, _ := preview.At(0, 0).RGBA()
				require.Zero(t, r)
				require.Greater(t, g, uint32(0), "actual APNG first frame rather than fallback image")
			}
			require.NoError(t, DeleteAsset(a))
			files, err := filepath.Glob("uploads/emojis/*")
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestRejectUnsafeInvalidAndOversizedUploadsWithoutFiles(t *testing.T) {
	oversized := samplePNG(t, 1025, 1)
	tests := []struct {
		name, mime string
		data       []byte
	}{{"svg", "image/svg+xml", []byte(`<svg onload="alert(1)"/>`)}, {"html", "image/png", []byte(`<html>not an image</html>`)}, {"mismatch", "image/jpeg", samplePNG(t, 1, 1)}, {"truncated", "image/png", []byte("\x89PNG\r\n\x1a\n")}, {"oversized-pixels", "image/png", oversized}, {"oversized-bytes", "image/png", bytes.Repeat([]byte{0}, int(MaxSize)+1)}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assetWorkspace(t)
			a, err := StoreEmojiImage(context.Background(), bytes.NewReader(tc.data), tc.mime)
			require.Error(t, err)
			require.Nil(t, a)
			_, err = os.Stat("uploads")
			require.True(t, os.IsNotExist(err))
		})
	}
}

func TestAnimationBudgetsCheckedBeforeDecode(t *testing.T) {
	gifdata := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00")
	// A compressed-data-free oversized descriptor is rejected on its dimensions.
	gifdata = append(gifdata, 0x2c, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0, 2, 0, 0x3b)
	require.ErrorContains(t, validateGIFBudget(gifdata), "budget")
	webp, err := os.ReadFile("testdata/animated.webp")
	require.NoError(t, err)
	corrupt := append([]byte(nil), webp...)
	i := bytes.Index(corrupt, []byte("ANMF"))
	require.Greater(t, i, 0)
	corrupt[i+8+6] = 0xff
	corrupt[i+8+7] = 0xff
	corrupt[i+8+8] = 0xff
	_, _, _, _, err = decodeImage(corrupt, "image/webp")
	require.ErrorContains(t, err, "budget")
	corrupt = append([]byte(nil), webp...)
	binary.LittleEndian.PutUint32(corrupt[4:], 0xffffffff)
	_, _, _, _, err = decodeImage(corrupt, "image/webp")
	require.ErrorContains(t, err, "container length")
	apng, err := os.ReadFile("testdata/animated.png")
	require.NoError(t, err)
	i = bytes.Index(apng, []byte("acTL"))
	require.Greater(t, i, 0)
	payload := append([]byte(nil), apng[i+4:i+12]...)
	binary.BigEndian.PutUint32(payload, maxFrames+1)
	bad := append([]byte(nil), apng[:i-4]...)
	bad = append(bad, pngChunk("acTL", payload)...)
	bad = append(bad, apng[i+16:]...)
	_, _, _, _, err = decodeImage(bad, "image/png")
	require.ErrorContains(t, err, "frame count")
}

func TestCanceledUploadAndStorageFailure(t *testing.T) {
	assetWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a, err := StoreEmojiImage(ctx, bytes.NewReader(samplePNG(t, 1, 1)), "image/png")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, a)
	require.NoError(t, os.WriteFile("uploads", []byte("not a directory"), 0600))
	a, err = StoreEmojiImage(context.Background(), bytes.NewReader(samplePNG(t, 1, 1)), "image/png")
	require.Error(t, err)
	require.Nil(t, a)
}

func TestServeOnlyGeneratedImagesAndDeleteOnlyLocalAssets(t *testing.T) {
	assetWorkspace(t)
	a, err := StoreEmojiImage(context.Background(), bytes.NewReader(samplePNG(t, 2, 2)), "image/png")
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/emoji-assets/:file", Serve)
	for _, raw := range []string{a.URL, a.StaticURL} {
		assetPath := strings.TrimPrefix(raw, "https://emoji.example")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, assetPath, nil))
		require.Equal(t, 200, w.Code)
		require.Equal(t, "image/png", w.Header().Get("Content-Type"))
		require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
		require.NotEmpty(t, w.Header().Get("ETag"))
		_, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
		require.NoError(t, err)
		cached := httptest.NewRecorder()
		conditional := httptest.NewRequest(http.MethodGet, assetPath, nil)
		conditional.Header.Set("If-None-Match", w.Header().Get("ETag"))
		router.ServeHTTP(cached, conditional)
		require.Equal(t, http.StatusNotModified, cached.Code)
		require.Empty(t, cached.Body.Bytes())
	}
	for _, name := range []string{"secret.txt", "../../secret", "foo.svg", "a-static.png"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "file", Value: name}}
		Serve(c)
		require.Equal(t, http.StatusNotFound, c.Writer.Status())
	}
	require.Error(t, DeleteAsset(&Asset{URL: "https://attacker.test/emoji-assets/" + filepath.Base(a.URL), StaticURL: a.StaticURL}))
	require.NotEmpty(t, localBytes(t, a.URL))
}

func TestImportBlocksPrivateURLs(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/image.png", "http://169.254.169.254/latest/meta-data", "http://[::1]/x", "file:///etc/passwd", "https://user:secret@example.org/a.png", "data:image/png;base64,abcd"} {
		_, err := ImportEmojiImage(context.Background(), raw)
		require.Error(t, err, raw)
	}
}
