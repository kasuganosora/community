package storage

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPathInsideRoot(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		root     string
		sep      string
		expected bool
	}{
		{"exact root", "/uploads", "/uploads", "/", true},
		{"inside root", "/uploads/media/file.jpg", "/uploads", "/", true},
		{"sibling prefix attack", "/uploadsevil/file.jpg", "/uploads", "/", false},
		{"traversal escape", "/etc/passwd", "/uploads", "/", false},
		{"traversal escape cleaned", "/uploads", "/uploads", "/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPathInsideRoot(tt.path, tt.root, tt.sep)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSaveByMultipart_PathTraversal(t *testing.T) {
	// Attempt to save a file outside the uploads directory
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("malicious"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	_, fileHeader, err := req.FormFile("file")
	require.NoError(t, err)

	_, err = SaveByMultipart("../../etc/passwd", fileHeader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestDelete_PathTraversal(t *testing.T) {
	err := Delete("../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestSaveByMultipart_ValidPath(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.txt")
	require.NoError(t, err)
	testData := []byte("valid content")
	_, err = part.Write(testData)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	_, fileHeader, err := req.FormFile("file")
	require.NoError(t, err)

	testRelPath := "test_save_by_multipart_valid.txt"
	savePath, err := SaveByMultipart(testRelPath, fileHeader)
	require.NoError(t, err)
	assert.NotEmpty(t, savePath)

	// Verify file exists
	fullPath := filepath.Join("uploads", testRelPath)
	assert.FileExists(t, fullPath)

	// Read and verify content
	content, err := os.ReadFile(fullPath) //nolint:gosec // G304: test path
	require.NoError(t, err)
	assert.Equal(t, testData, content)

	// Cleanup
	_ = os.Remove(fullPath)
}

func TestLocalStorage_PrefixAttackRejection(t *testing.T) {
	// Verify that hasPrefixPath rejects sibling prefix attacks
	assert.False(t, hasPrefixPath("/tmp/uploadsevil", "/tmp/uploads"))
	assert.True(t, hasPrefixPath("/tmp/uploads", "/tmp/uploads"))
	assert.True(t, hasPrefixPath("/tmp/uploads/file.txt", "/tmp/uploads"))
	assert.False(t, hasPrefixPath("/tmp/other", "/tmp/uploads"))
}

// TestCloudStorage_DeleteTraversalDefense verifies that the CloudStorage.Delete
// method (local mode) rejects path traversal attempts. This is defense-in-depth
// since the package-level Delete() already guards, but the method must too.
func TestCloudStorage_DeleteTraversalDefense(t *testing.T) {
	// A CloudStorage that is NOT enabled (local mode): provider is nil / disabled.
	s := &CloudStorage{enabled: false, provider: nil}

	err := s.Delete("../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")

	// Deeply nested traversal also rejected
	err = s.Delete("media/../../etc/shadow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

// TestCloudStorage_DownloadTraversalDefense verifies that the CloudStorage.Download
// method (local mode) rejects path traversal attempts.
func TestCloudStorage_DownloadTraversalDefense(t *testing.T) {
	s := &CloudStorage{enabled: false, provider: nil}

	_, err := s.Download("../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

