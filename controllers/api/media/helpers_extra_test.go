package media

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/models"
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
// cleanupUploadedFiles
// ---------------------------------------------------------------------------

func TestCleanupUploadedFiles_BothExist(t *testing.T) {
	dir := t.TempDir()

	// Create a file and a thumbnail.
	filePath := filepath.Join(dir, "img.png")
	require.NoError(t, os.WriteFile(filePath, []byte("data"), 0600))

	thumbName := "thumb_img.png"
	thumbPath := filepath.Join(dir, thumbName)
	require.NoError(t, os.WriteFile(thumbPath, []byte("thumb"), 0600))

	cleanupUploadedFiles(dir, "img.png", &thumbName)

	_, err := os.Stat(filePath)
	assert.True(t, os.IsNotExist(err), "file should be removed")
	_, err = os.Stat(thumbPath)
	assert.True(t, os.IsNotExist(err), "thumbnail should be removed")
}

func TestCleanupUploadedFiles_NoThumbnail(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "img.png")
	require.NoError(t, os.WriteFile(filePath, []byte("data"), 0600))

	// nil thumbnail path - should not panic and should remove the file.
	cleanupUploadedFiles(dir, "img.png", nil)

	_, err := os.Stat(filePath)
	assert.True(t, os.IsNotExist(err), "file should be removed")
}

func TestCleanupUploadedFiles_FileMissingNoError(t *testing.T) {
	dir := t.TempDir()
	// Neither file nor thumbnail exist; cleanup should be a no-op without error.
	thumbName := "thumb_img.png"
	assert.NotPanics(t, func() {
		cleanupUploadedFiles(dir, "missing.png", &thumbName)
	})
}

func TestCleanupUploadedFiles_EmptyFileName(t *testing.T) {
	dir := t.TempDir()
	// Empty fileName - should skip file removal entirely (no panic).
	assert.NotPanics(t, func() {
		cleanupUploadedFiles(dir, "", nil)
	})
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

	// Create the physical files that updateMediaError should remove.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "img.png"), []byte("d"), 0600))
	thumb := "thumb_img.png"
	require.NoError(t, os.WriteFile(filepath.Join(dir, thumb), []byte("t"), 0600))

	updateMediaError(media.Id, dir, "img.png", &thumb)

	// The media record must have been deleted.
	var got models.MediaAttachment
	err := gdb.First(&got, media.Id).Error
	assert.Error(t, err, "media record should be deleted")

	// The physical files must have been removed.
	_, err = os.Stat(filepath.Join(dir, "img.png"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(dir, thumb))
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
	updateMediaError(media.Id, "", "", nil)

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
