package account

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeImageFileHeader builds a multipart.FileHeader with the given content
// so validateImageFileHeader can inspect real magic bytes.
func makeImageFileHeader(t *testing.T, content []byte) *multipart.FileHeader {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "upload.bin")
	require.NoError(t, err)

	_, err = part.Write(content)
	require.NoError(t, err)

	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	_, fh, err := req.FormFile("file")
	require.NoError(t, err)

	return fh
}

func TestValidateImageFileHeader_ValidPNG(t *testing.T) {
	// Minimal valid PNG header (magic bytes).
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	}
	fh := makeImageFileHeader(t, pngData)
	err := validateImageFileHeader(fh)
	assert.NoError(t, err)
}

func TestValidateImageFileHeader_ValidJPEG(t *testing.T) {
	// JPEG SOI marker.
	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	fh := makeImageFileHeader(t, jpegData)
	err := validateImageFileHeader(fh)
	assert.NoError(t, err)
}

func TestValidateImageFileHeader_RejectsText(t *testing.T) {
	// Plain text / HTML should be rejected.
	textData := []byte("<html><script>alert(1)</script></html>")
	fh := makeImageFileHeader(t, textData)
	err := validateImageFileHeader(fh)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an image")
}

func TestValidateImageFileHeader_RejectsPDF(t *testing.T) {
	pdfData := []byte("%PDF-1.4 fake pdf content")
	fh := makeImageFileHeader(t, pdfData)
	err := validateImageFileHeader(fh)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an image")
}

func TestValidateImageFileHeader_RejectsGIF89aAsAllowed(t *testing.T) {
	// GIF89a magic bytes should be accepted as an image.
	gifData := []byte{'G', 'I', 'F', '8', '9', 'a', 0x01, 0x00, 0x01, 0x00}
	fh := makeImageFileHeader(t, gifData)
	err := validateImageFileHeader(fh)
	assert.NoError(t, err)
}
