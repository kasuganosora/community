package media

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Upload handler - success paths
// ---------------------------------------------------------------------------

func TestUploadCorruptImageLeavesNoRowOrObject(t *testing.T) {
	withTempWorkDir(t)
	gdb := setupMediaTestDB(t)
	payload := append([]byte{0xff, 0xd8, 0xff}, []byte("this is not a decodable jpeg")...)
	req := createMediaWithMultipartRequest(t, "bad.jpg", "image/jpeg", payload, nil)
	c, w := newHandlerContext(t, nil, req)
	Upload(c)
	require.NotEqual(t, http.StatusOK, w.Code, w.Body.String())
	var n int64
	require.NoError(t, gdb.Model(&models.MediaAttachment{}).Count(&n).Error)
	require.Zero(t, n)
	matches, err := filepath.Glob(filepath.Join("uploads", "media", "*", "*", "*", "*"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestUpload_SuccessNonImage(t *testing.T) {
	withTempWorkDir(t)
	gdb := setupMediaTestDB(t)

	// Build a multipart request with a valid MP3 (ID3 magic bytes) so that
	// magic-byte validation passes. The declared Content-Type is audio/mpeg.
	wavData := validWAV()
	req := createMediaWithMultipartRequest(t, "clip.wav", "audio/wav", wavData,
		map[string]string{"description": "an audio clip"})

	c, w := newHandlerContext(t, nil, req)
	Upload(c)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// A DB record should exist with the audio type and metadata.
	var count int64
	require.NoError(t, gdb.Model(&models.MediaAttachment{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got).Error)
	assert.Equal(t, "audio/wav", got.ContentType)
	assert.Equal(t, models.MediaTypeAudio, got.Type)
	assert.Equal(t, "an audio clip", got.Description)
	assert.Positive(t, got.FileSize)
}

func TestUpload_SuccessImageWithThumbnail(t *testing.T) {
	dir := withTempWorkDir(t)
	gdb := setupMediaTestDB(t)

	pngData := validPNGBytes()
	req := createMediaWithMultipartRequest(t, "pic.png", "image/png", pngData,
		map[string]string{"description": "a picture", "focus": "0.5,-0.2"})

	c, w := newHandlerContext(t, nil, req)
	Upload(c)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got).Error)
	assert.Equal(t, "image/webp", got.ContentType)
	assert.Equal(t, models.MediaTypeImage, got.Type)
	assert.Equal(t, "a picture", got.Description)
	assert.InDelta(t, 0.5, got.FocusX, 0.0001)
	assert.InDelta(t, -0.2, got.FocusY, 0.0001)
	assert.Positive(t, got.FileSize)
	// 1×1 input is padded to even dimensions for the encoder.
	require.NotNil(t, got.Width)
	require.NotNil(t, got.Height)
	assert.Equal(t, 2, *got.Width)
	assert.Equal(t, 2, *got.Height)

	// A thumbnail file should have been created on disk.
	matches, err := filepath.Glob(filepath.Join(dir, "uploads", "media", "*", "*", "*", "thumb_*"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(matches), 1, "thumbnail should exist on disk")
}

func TestDistinctUploadsKeepIndependentOriginalsAndThumbnails(t *testing.T) {
	withTempWorkDir(t)
	setupMediaTestDB(t)
	red := uploadSolidPNG(t, color.RGBA{R: 220, G: 20, B: 20, A: 255}, "red.png")
	blue := uploadSolidPNG(t, color.RGBA{R: 20, G: 40, B: 220, A: 255}, "blue.png")
	assertIndependentMedia(t, red, blue)

	var wg sync.WaitGroup
	var left, right models.MediaAttachment
	var leftCode, rightCode int
	var leftBody, rightBody string
	wg.Add(2)
	go func() {
		defer wg.Done()
		left, leftCode, leftBody = uploadSolidPNGRaw(color.RGBA{R: 20, G: 200, B: 40, A: 255}, "green.png")
	}()
	go func() {
		defer wg.Done()
		right, rightCode, rightBody = uploadSolidPNGRaw(color.RGBA{R: 230, G: 180, B: 20, A: 255}, "yellow.png")
	}()
	wg.Wait()
	require.Equal(t, http.StatusOK, leftCode, leftBody)
	require.Equal(t, http.StatusOK, rightCode, rightBody)
	assertIndependentMedia(t, left, right)
}

func solidPNG(c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func uploadSolidPNG(t *testing.T, c color.Color, name string) models.MediaAttachment {
	t.Helper()
	got, code, body := uploadSolidPNGRaw(c, name)
	require.Equal(t, http.StatusOK, code, body)
	return got
}

func uploadSolidPNGRaw(c color.Color, name string) (models.MediaAttachment, int, string) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		return models.MediaAttachment{}, 0, err.Error()
	}
	if _, err = part.Write(solidPNG(c)); err != nil {
		return models.MediaAttachment{}, 0, err.Error()
	}
	if err = writer.Close(); err != nil {
		return models.MediaAttachment{}, 0, err.Error()
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx, w := uploadHandlerContext(req)
	Upload(ctx)
	if w.Code != http.StatusOK {
		return models.MediaAttachment{}, w.Code, w.Body.String()
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		return models.MediaAttachment{}, w.Code, err.Error()
	}
	id, err := strconv.ParseUint(payload.ID, 10, 64)
	if err != nil {
		return models.MediaAttachment{}, w.Code, err.Error()
	}
	var got models.MediaAttachment
	if err = database.GetDB().First(&got, id).Error; err != nil {
		return models.MediaAttachment{}, w.Code, err.Error()
	}
	return got, w.Code, w.Body.String()
}

func uploadHandlerContext(req *http.Request) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req
	ctx.Set("authenticated", true)
	ctx.Set("account", createTestAccount())
	return ctx, w
}

func assertIndependentMedia(t *testing.T, a, b models.MediaAttachment) {
	t.Helper()
	require.NotEqual(t, a.Id, b.Id)
	require.NotEqual(t, a.FilePath, b.FilePath)
	require.NotEmpty(t, a.ThumbPath())
	require.NotEmpty(t, b.ThumbPath())
	require.NotEqual(t, a.ThumbnailStorageKey(), b.ThumbnailStorageKey(), "thumbnail objects must not share a storage key")
	aOrig, err := os.ReadFile(filepath.Join("uploads", filepath.FromSlash(a.StorageKey())))
	require.NoError(t, err)
	bOrig, err := os.ReadFile(filepath.Join("uploads", filepath.FromSlash(b.StorageKey())))
	require.NoError(t, err)
	require.NotEqual(t, aOrig, bOrig)
	aThumb, err := os.ReadFile(filepath.Join("uploads", filepath.FromSlash(a.ThumbnailStorageKey())))
	require.NoError(t, err)
	bThumb, err := os.ReadFile(filepath.Join("uploads", filepath.FromSlash(b.ThumbnailStorageKey())))
	require.NoError(t, err)
	require.NotEqual(t, aThumb, bThumb)
	aView, err := serializer.ToMediaAttachment(&a)
	require.NoError(t, err)
	bView, err := serializer.ToMediaAttachment(&b)
	require.NoError(t, err)
	require.NotEqual(t, aView.Url, bView.Url)
	require.NotEqual(t, aView.PreviewUrl, bView.PreviewUrl)
	require.Contains(t, aView.PreviewUrl, "/media/"+aView.Id+"/thumbnail/"+a.ThumbPath())
	require.Contains(t, bView.PreviewUrl, "/media/"+bView.Id+"/thumbnail/"+b.ThumbPath())
}

func TestUpload_FocusParsingInvalid(t *testing.T) {
	withTempWorkDir(t)
	gdb := setupMediaTestDB(t)

	// Invalid focus value should not error out; focus just stays 0.
	req := createMediaWithMultipartRequest(t, "clip.wav", "audio/wav", validWAV(),
		map[string]string{"focus": "not-a-number"})

	c, w := newHandlerContext(t, nil, req)
	Upload(c)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got).Error)
	assert.InDelta(t, 0.0, got.FocusX, 0.0001)
	assert.InDelta(t, 0.0, got.FocusY, 0.0001)
}

// ---------------------------------------------------------------------------
// UploadAsync handler
// ---------------------------------------------------------------------------

func TestUploadAsync_UnsupportedMediaType(t *testing.T) {
	setupMediaTestDB(t)

	// Send a PDF content-type, which is not in the allow list.
	pdfContent := []byte("%PDF-1.4 fake pdf content")
	req := createMediaWithMultipartRequest(t, "doc.pdf", "application/pdf", pdfContent, nil)

	c, w := newHandlerContext(t, nil, req)
	UploadAsync(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUploadAsync_BadMagicBytes(t *testing.T) {
	setupMediaTestDB(t)

	// Declare image/jpeg but provide non-image bytes.
	req := createMediaWithMultipartRequest(t, "test.jpg", "image/jpeg",
		[]byte("this is not really a jpeg at all"), nil)

	c, w := newHandlerContext(t, nil, req)
	UploadAsync(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUploadAsync_Accepted(t *testing.T) {
	withTempWorkDir(t)
	gdb := setupMediaTestDB(t)

	req := createMediaWithMultipartRequest(t, "clip.wav", "audio/wav", validWAV(),
		map[string]string{"description": "async clip", "focus": "0.1,0.2"})

	c, w := newHandlerContext(t, nil, req)
	UploadAsync(c)

	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "async clip")

	var final models.MediaAttachment
	require.Eventually(t, func() bool {
		return gdb.First(&final).Error == nil && final.ProcessState == models.ProcessReady && final.FileName != "processing"
	}, 15*time.Second, 20*time.Millisecond)
	assert.Equal(t, models.MediaTypeAudio, final.Type)
	assert.Equal(t, "audio/wav", final.ContentType)
	assert.InDelta(t, 0.1, final.FocusX, 0.0001)
	assert.InDelta(t, 0.2, final.FocusY, 0.0001)
	assert.Positive(t, final.FileSize)
	require.Eventually(t, func() bool { return len(asyncUploadSem) == 0 }, 15*time.Second, time.Millisecond)
}

func TestUploadAsync_NoFileBindingError(t *testing.T) {
	setupMediaTestDB(t)

	// A request with no "file" field at all triggers ShouldBind to fail.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/v2", body) //nolint:noctx
	req.Header.Set("Content-Type", writer.FormDataContentType())

	c, w := newHandlerContext(t, nil, req)
	UploadAsync(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
