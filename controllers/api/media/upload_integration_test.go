package media

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Upload handler - success paths
// ---------------------------------------------------------------------------

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
