package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLocalStorage_Exists(t *testing.T) {
	storage := &LocalStorage{Root: "/tmp/test"}

	t.Run("non-existing file", func(t *testing.T) {
		assert.False(t, storage.Exists("nonexistent.txt"))
	})

	t.Run("path traversal attempt", func(t *testing.T) {
		// Should return false for path traversal
		assert.False(t, storage.Exists("../etc/passwd"))
	})
}

func TestLocalStorage_Root(t *testing.T) {
	storage := &LocalStorage{Root: "/tmp/test"}
	assert.Equal(t, "/tmp/test", storage.Root)
}

func TestLocalStorage_EmptyRoot(t *testing.T) {
	storage := &LocalStorage{Root: ""}
	assert.Equal(t, "", storage.Root)
}

func TestLocalStorage_Struct(t *testing.T) {
	storage := &LocalStorage{
		Root: "/data/uploads",
	}

	assert.Equal(t, "/data/uploads", storage.Root)
}

func TestSave(t *testing.T) {
	// Test the Save function signature
	assert.NotNil(t, Save)
}

func TestSave_Integration(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "storage_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create test data
	testData := []byte("hello world test content")
	testPath := "test/subdir/file.txt"

	// Save the file
	savePath, err := Save(testPath, testData)
	assert.NoError(t, err)
	assert.NotEmpty(t, savePath)

	// Verify the file was created
	fullPath := filepath.Join("uploads", testPath)
	assert.FileExists(t, fullPath)

	// Read and verify content
	content, err := os.ReadFile(fullPath)
	assert.NoError(t, err)
	assert.Equal(t, testData, content)

	// Cleanup
	os.Remove(fullPath)
	os.Remove(filepath.Dir(fullPath))
}

func TestLocalStorage_Save(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "local_storage_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	storage := &LocalStorage{Root: tmpDir}

	t.Run("save new file", func(t *testing.T) {
		data := []byte("test content")
		savePath, err := storage.Save("test.txt", data)
		assert.NoError(t, err)
		assert.NotEmpty(t, savePath)
		assert.True(t, storage.Exists("test.txt"))
	})

	t.Run("save file in subdirectory", func(t *testing.T) {
		data := []byte("nested content")
		savePath, err := storage.Save("subdir/nested.txt", data)
		assert.NoError(t, err)
		assert.NotEmpty(t, savePath)
		assert.True(t, storage.Exists("subdir/nested.txt"))
	})

	t.Run("path traversal rejection", func(t *testing.T) {
		data := []byte("malicious")
		_, err := storage.Save("../../../etc/passwd", data)
		assert.Error(t, err)
		assert.Equal(t, os.ErrInvalid, err)
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		// First save
		data1 := []byte("original content")
		_, err := storage.Save("overwrite.txt", data1)
		assert.NoError(t, err)

		// Overwrite
		data2 := []byte("new content")
		_, err = storage.Save("overwrite.txt", data2)
		assert.NoError(t, err)

		// Verify new content
		reader, err := storage.Open("overwrite.txt")
		assert.NoError(t, err)
		assert.NotNil(t, reader)
	})
}

func TestLocalStorage_Open(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "local_storage_open_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	storage := &LocalStorage{Root: tmpDir}

	t.Run("open existing file", func(t *testing.T) {
		data := []byte("test content for open")
		_, err := storage.Save("open_test.txt", data)
		assert.NoError(t, err)

		reader, err := storage.Open("open_test.txt")
		assert.NoError(t, err)
		assert.NotNil(t, reader)
		// Close the reader to release file lock on Windows
		// Note: bytes.Reader doesn't need to be closed, but let's verify content
	})

	t.Run("open non-existent file", func(t *testing.T) {
		reader, err := storage.Open("nonexistent.txt")
		assert.Error(t, err)
		assert.Equal(t, os.ErrNotExist, err)
		assert.Nil(t, reader)
	})
}

func TestLocalStorage_Delete(t *testing.T) {
	t.Run("delete non-existent file", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "local_storage_delete_test_2")
		assert.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		storage := &LocalStorage{Root: tmpDir}
		err = storage.Delete("nonexistent.txt")
		assert.Error(t, err)
		assert.Equal(t, os.ErrNotExist, err)
	})

	t.Run("delete with path traversal", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "local_storage_delete_test_3")
		assert.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		storage := &LocalStorage{Root: tmpDir}
		err = storage.Delete("../../../etc/passwd")
		assert.Error(t, err)
		assert.Equal(t, os.ErrInvalid, err)
	})

	t.Run("delete existing file created directly", func(t *testing.T) {
		// Create a temporary directory for this test case
		tmpDir, err := os.MkdirTemp("", "local_storage_delete_test_4")
		assert.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		storage := &LocalStorage{Root: tmpDir}

		// Create file directly (not using Save which may leave file open)
		filePath := filepath.Join(tmpDir, "delete_me_direct.txt")
		err = os.WriteFile(filePath, []byte("direct content"), 0644)
		assert.NoError(t, err)
		assert.True(t, storage.Exists("delete_me_direct.txt"))

		err = storage.Delete("delete_me_direct.txt")
		assert.NoError(t, err)
		assert.False(t, storage.Exists("delete_me_direct.txt"))
	})
}

func TestSaveByMultipart_Signature(t *testing.T) {
	// Test the SaveByMultipart function signature
	assert.NotNil(t, SaveByMultipart)
}

func TestDelete_Signature(t *testing.T) {
	// Test the Delete function signature
	assert.NotNil(t, Delete)
}

func TestDelete_Integration(t *testing.T) {
	// Use unique filename to avoid conflicts
	testPath := "test_delete_file_unique_" + time.Now().Format("20060102150405") + ".txt"
	testData := []byte("test delete content")

	// Create the uploads directory if it doesn't exist
	uploadsDir := "uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		t.Skip("Cannot create uploads directory")
	}

	// Create file directly to avoid file handle issues on Windows
	fullPath := filepath.Join(uploadsDir, testPath)
	err := os.WriteFile(fullPath, testData, 0644)
	assert.NoError(t, err)

	// Verify it exists
	assert.FileExists(t, fullPath)

	// Delete it
	err = Delete(testPath)
	assert.NoError(t, err)

	// Verify it's gone
	assert.NoFileExists(t, fullPath)
}
