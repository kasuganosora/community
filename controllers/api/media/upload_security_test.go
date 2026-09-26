package media

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestContext creates a test gin context with an authenticated test account.
func setupTestContext(w *httptest.ResponseRecorder) *gin.Context {
	c, _ := gin.CreateTestContext(w)
	c.Set("authenticated", true)
	c.Set("account", createTestAccount())

	return c
}

func TestNormalizeContentType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain", "image/jpeg", "image/jpeg"},
		{"with charset", "image/jpeg; charset=utf-8", "image/jpeg"},
		{"with multiple params", "image/png; charset=utf-8; boundary=something", "image/png"},
		{"with whitespace", "  Image/JPEG  ", "image/jpeg"},
		{"with param and whitespace", " Video/MP4 ; charset=utf-8 ", "video/mp4"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeContentType(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeDetectedMIME(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"jpeg", "image/jpeg", "image/jpeg"},
		{"wav variant", "audio/x-wav", "audio/wav"},
		{"with whitespace", "  image/png  ", "image/png"},
		{"upper case", "IMAGE/JPEG", "image/jpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeDetectedMIME(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateUniqueFileName(t *testing.T) {
	now := time.Now()
	name1 := generateUniqueFileName(now, ".jpg")
	name2 := generateUniqueFileName(now, ".jpg")

	assert.NotEmpty(t, name1)
	assert.NotEmpty(t, name2)
	assert.NotEqual(t, name1, name2, "consecutive calls should produce different names")
	assert.True(t, bytes.HasSuffix([]byte(name1), []byte(".jpg")))
}

func TestExtensionForContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		expectedExt string // expected extension that must be one of valid options
		validExts   []string
	}{
		{"jpeg", "image/jpeg", ".jpg", []string{".jpg"}},
		{"png", "image/png", ".png", []string{".png"}},
		{"mp4", "video/mp4", ".mp4", []string{".mp4"}},
		{"webm", "video/webm", ".webm", []string{".webm"}},
		{"quicktime", "video/quicktime", ".mov", []string{".mov"}},
		{"mp3", "audio/mpeg", ".mp3", []string{".mp3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extensionForContentType(tt.contentType)
			assert.NotEmpty(t, result)
			assert.Contains(t, tt.validExts, result, "extension should be one of the valid options")
		})
	}
}

// createFileHeader creates a multipart.FileHeader with the given parameters for testing.
func createFileHeader(t *testing.T, filename, contentType string, content []byte) *multipart.FileHeader {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("dummy", "dummy")

	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	_, fileHeader, err := req.FormFile("file")
	require.NoError(t, err)

	// Override Content-Type header to simulate declared type
	fileHeader.Header.Set("Content-Type", contentType)

	return fileHeader
}

func TestValidateFileUpload_MagicBytesMismatch(t *testing.T) {
	// PNG magic bytes but declared as image/jpeg
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG header
	}
	header := createFileHeader(t, "test.jpg", "image/jpeg", pngData)
	_, err := validateFileUpload(header)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestValidateFileUpload_ValidPNG(t *testing.T) {
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG header
		0x00, 0x00, 0x00, 0x0D,
	}
	header := createFileHeader(t, "test.png", "image/png", pngData)
	ct, err := validateFileUpload(header)
	require.NoError(t, err)
	assert.Equal(t, "image/png", ct)
}

func TestValidateFileUpload_ContentTypeWithParams(t *testing.T) {
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	}
	header := createFileHeader(t, "test.png", "image/png; charset=utf-8", pngData)
	ct, err := validateFileUpload(header)
	require.NoError(t, err)
	assert.Equal(t, "image/png", ct)
}

func TestValidateFileUpload_Oversize(t *testing.T) {
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	}
	header := createFileHeader(t, "test.png", "image/png", pngData)
	header.Size = MaxImageSize + 1
	_, err := validateFileUpload(header)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file size too large")
}

func TestUpload_MagicBytesMismatch(t *testing.T) {
	// Declare image/jpeg but send PDF content
	pdfContent := []byte("%PDF-1.4 fake pdf content that is not an image")
	req := createTestMultipartRequest(t, "file", "test.jpg", pdfContent)
	w := httptest.NewRecorder()
	c := setupTestContext(w)
	c.Request = req

	Upload(c)
	// Should be rejected due to magic bytes mismatch
	assert.Equal(t, 400, w.Code)
}

func TestNormalizeContentType_InjectedSemicolon(t *testing.T) {
	// Content-Type with injected parameter should still normalize correctly
	result := normalizeContentType("image/jpeg; foo=bar")
	assert.Equal(t, "image/jpeg", result)
}
