package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// readAllFromFileHeader
// ---------------------------------------------------------------------------

func TestReadAllFromFileHeader(t *testing.T) {
	header := createFileHeader(t, "test.txt", "text/plain", []byte("hello world"))

	data, err := readAllFromFileHeader(header)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(data))
}

func TestReadAllFromFileHeader_Empty(t *testing.T) {
	header := createFileHeader(t, "empty.txt", "text/plain", []byte{})

	data, err := readAllFromFileHeader(header)
	require.NoError(t, err)
	assert.Empty(t, data)
}

// ---------------------------------------------------------------------------
// storedMedia.cleanup
// ---------------------------------------------------------------------------

func TestStoredMediaCleanup(t *testing.T) {
	root := t.TempDir()
	defer storage.SetDefault(&storage.LocalBackend{Root: root})()

	thumb := "thumb_img.png"
	s := &storedMedia{FileName: "img.png", FilePath: "2026/01/01/img.png", ThumbnailPath: &thumb}
	assert.Equal(t, []string{"media/2026/01/01/img.png", "media/2026/01/01/thumb_img.png"}, s.keys())
	for _, k := range s.keys() {
		require.NoError(t, storage.Default().Put(context.Background(), k, strings.NewReader("x"), 1, ""))
	}
	s.cleanup()
	for _, k := range s.keys() {
		_, err := os.Stat(filepath.Join(root, k))
		assert.True(t, os.IsNotExist(err), k)
	}
	// Missing objects, nil and empty values are no-ops.
	assert.NotPanics(t, func() { s.cleanup(); (*storedMedia)(nil).cleanup(); (&storedMedia{}).cleanup() })
}

// ---------------------------------------------------------------------------
// updateMediaError
// ---------------------------------------------------------------------------

func TestUpdateMediaError_DeletesRecordAndCleansFiles(t *testing.T) {
	gdb := setupMediaTestDB(t)

	// Insert a media record to be cleaned up.
	media := &models.MediaAttachment{
		AccountId: 1, FileName: "img.png", FilePath: "p",
		ContentType: "image/png", Type: models.MediaTypeImage,
	}
	require.NoError(t, gdb.Create(media).Error)

	// Store objects through the backend that updateMediaError should remove.
	root := t.TempDir()
	defer storage.SetDefault(&storage.LocalBackend{Root: root})()
	thumb := "thumb_img.png"
	stored := &storedMedia{FileName: "img.png", FilePath: "2026/01/01/img.png", ThumbnailPath: &thumb}
	for _, k := range stored.keys() {
		require.NoError(t, storage.Default().Put(context.Background(), k, strings.NewReader("d"), 1, ""))
	}

	updateMediaError(media.Id, stored)

	// The media record must have been deleted.
	var got models.MediaAttachment
	err := gdb.First(&got, media.Id).Error
	assert.Error(t, err, "media record should be deleted")

	// The stored objects must have been removed.
	_, err = os.Stat(filepath.Join(root, "media/2026/01/01/img.png"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(root, "media/2026/01/01", thumb))
	assert.True(t, os.IsNotExist(err))
}

func TestUpdateMediaError_NoMediaDir(t *testing.T) {
	gdb := setupMediaTestDB(t)

	media := &models.MediaAttachment{
		AccountId: 1, FileName: "img.png", FilePath: "p",
		ContentType: "image/png", Type: models.MediaTypeImage,
	}
	require.NoError(t, gdb.Create(media).Error)

	// Empty mediaDir => only the DB record is deleted, no file cleanup attempted.
	updateMediaError(media.Id, nil)

	var got models.MediaAttachment
	err := gdb.First(&got, media.Id).Error
	assert.Error(t, err, "media record should be deleted")
}

// ---------------------------------------------------------------------------
// processUploadAsync (success path for a non-image content type)
// ---------------------------------------------------------------------------

func TestProcessUploadAsync_NonImage(t *testing.T) {
	dir := withTempWorkDir(t)
	gdb := setupMediaTestDB(t)

	// A media record in "processing" state.
	media := &models.MediaAttachment{
		AccountId: 1, FileName: "processing", FilePath: "",
		ContentType: "audio/mpeg", Type: models.MediaTypeUnknown,
	}
	require.NoError(t, gdb.Create(media).Error)

	// MP3 magic bytes (ID3) so the saved file is at least plausible; the content
	// itself is not re-validated here since processUploadAsync trusts the
	// already-validated content type.
	mp3Data := []byte{0x49, 0x44, 0x33, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	processUploadAsync(media.Id, mp3Data, "audio/mpeg")

	// The record should now be updated with file metadata.
	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got, media.Id).Error)
	assert.Equal(t, models.MediaTypeAudio, got.Type)
	assert.Equal(t, "audio/mpeg", got.ContentType)
	assert.NotEqual(t, "processing", got.FileName)
	assert.Equal(t, int64(len(mp3Data)), got.FileSize)
	assert.Contains(t, got.FilePath, ".mp3")
	assert.Nil(t, got.ThumbnailPath)

	// The physical file must exist under uploads/media/<date>/.
	matches, err := filepath.Glob(filepath.Join(dir, "uploads", "media", "*", "*", "*", "*.mp3"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "uploaded mp3 file should exist on disk")
}

// ---------------------------------------------------------------------------
// validateMagicBytes - additional branches
// ---------------------------------------------------------------------------

func TestValidateMagicBytes_ValidJPEG(t *testing.T) {
	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	header := createFileHeader(t, "test.jpg", "image/jpeg", jpegData)

	err := validateMagicBytes(header, "image/jpeg")
	assert.NoError(t, err)
}

func TestValidateMagicBytes_UnknownContent(t *testing.T) {
	// Random data that filetype cannot match.
	header := createFileHeader(t, "test.jpg", "image/jpeg", []byte("random text not a file"))

	err := validateMagicBytes(header, "image/jpeg")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestNormalizeDetectedMIME_ID3wavMapping(t *testing.T) {
	// Exercise the filetype library returns wav mapping by directly verifying
	// the helper maps audio/x-wav -> audio/wav (already covered elsewhere, but
	// confirm the filetype lib detects a real wav as the expected variant).
	wavData := []byte{
		0x52, 0x49, 0x46, 0x46, // "RIFF"
		0x24, 0x00, 0x00, 0x00,
		0x57, 0x41, 0x56, 0x45, // "WAVE"
	}
	kind, err := filetype.Match(wavData)
	require.NoError(t, err)
	assert.Equal(t, "audio/x-wav", kind.MIME.Value)
	assert.Equal(t, "audio/wav", normalizeDetectedMIME(kind.MIME.Value))
}

// ---------------------------------------------------------------------------
// RegisterRoutes
// ---------------------------------------------------------------------------

func TestRegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/api/v1")

	// Should not panic and should register the documented routes.
	assert.NotPanics(t, func() {
		RegisterRoutes(rg)
	})

	routes := r.Routes()
	paths := make(map[string]bool)
	for _, rt := range routes {
		paths[rt.Path+" "+rt.Method] = true
	}

	assert.True(t, paths["/api/v1/media POST"], "POST /api/v1/media should be registered")
	assert.True(t, paths["/api/v1/media/v2 POST"], "POST /api/v1/media/v2 should be registered")
	assert.True(t, paths["/api/v1/media/:id GET"], "GET /api/v1/media/:id should be registered")
	assert.True(t, paths["/api/v1/media/:id PUT"], "PUT /api/v1/media/:id should be registered")
	assert.True(t, paths["/api/v1/media/:id DELETE"], "DELETE /api/v1/media/:id should be registered")
}
