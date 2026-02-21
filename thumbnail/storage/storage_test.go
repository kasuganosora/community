package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.RemoveAll("./test_storage")
	os.Exit(code)
}

func TestNewLocalBackend(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "thumbnails/",
		LocalPath:  "./test_storage",
		PublicURL:  "",
	}

	backend, err := NewBackend(cfg)
	if err != nil {
		t.Fatalf("Failed to create local backend: %v", err)
	}

	if backend == nil {
		t.Fatal("Backend should not be nil")
	}

	if backend.Type() != "local" {
		t.Errorf("Type = %s, want local", backend.Type())
	}

	// Verify directory was created
	if _, err := os.Stat("./test_storage"); os.IsNotExist(err) {
		t.Error("Storage directory should be created")
	}
}

func TestLocalBackendPutAndGet(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "test/",
		LocalPath:  "./test_storage",
	}

	backend, _ := NewBackend(cfg)
	ctx := context.Background()

	key := "test_file.txt"
	content := []byte("Hello, World!")

	// Test Put
	err := backend.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("Failed to put file: %v", err)
	}

	// Verify file exists
	fullPath := filepath.Join("./test_storage", "test", key)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Error("File should exist after put")
	}

	// Test GetURL
	url := backend.GetURL(key)
	if url == "" {
		t.Error("URL should not be empty")
	}
	if !strings.Contains(url, key) {
		t.Error("URL should contain the key")
	}
}

func TestLocalBackendExists(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "exists_test/",
		LocalPath:  "./test_storage",
	}

	backend, _ := NewBackend(cfg)
	ctx := context.Background()

	// Test non-existent file
	exists, err := backend.Exists(ctx, "non_existent.txt")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("Non-existent file should return false")
	}

	// Create a file
	key := "exists_test.txt"
	content := []byte("test content")
	backend.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain")

	// Test existing file
	exists, err = backend.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("Existing file should return true")
	}
}

func TestLocalBackendDelete(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "delete_test/",
		LocalPath:  "./test_storage",
	}

	backend, _ := NewBackend(cfg)
	ctx := context.Background()

	key := "delete_me.txt"
	content := []byte("delete me")

	// Create file
	backend.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain")

	// Verify file exists
	exists, _ := backend.Exists(ctx, key)
	if !exists {
		t.Fatal("File should exist before delete")
	}

	// Delete file
	err := backend.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify file is deleted
	exists, _ = backend.Exists(ctx, key)
	if exists {
		t.Error("File should not exist after delete")
	}
}

func TestLocalBackendNestedPath(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "nested/",
		LocalPath:  "./test_storage",
	}

	backend, _ := NewBackend(cfg)
	ctx := context.Background()

	key := "path/to/nested/file.txt"
	content := []byte("nested content")

	err := backend.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("Failed to put nested file: %v", err)
	}

	// Verify directory structure was created
	fullPath := filepath.Join("./test_storage", "nested", key)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Error("Nested file should exist")
	}

	// Verify we can get URL for nested file
	url := backend.GetURL(key)
	if url == "" {
		t.Error("URL should not be empty for nested file")
	}
}

func TestLocalBackendWithPublicURL(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "public/",
		LocalPath:  "./test_storage",
		PublicURL:  "https://cdn.example.com",
	}

	backend, _ := NewBackend(cfg)

	key := "public_file.txt"
	url := backend.GetURL(key)

	if !strings.HasPrefix(url, "https://cdn.example.com") {
		t.Errorf("URL should start with public URL: %s", url)
	}
}

func TestUnsupportedStorageType(t *testing.T) {
	cfg := Config{
		Type: "unsupported",
	}

	_, err := NewBackend(cfg)
	if err == nil {
		t.Error("Should return error for unsupported storage type")
	}
}

func TestLocalBackendLargeFile(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "large/",
		LocalPath:  "./test_storage",
	}

	backend, _ := NewBackend(cfg)
	ctx := context.Background()

	// Create 1MB of data
	content := make([]byte, 1024*1024)
	for i := range content {
		content[i] = byte(i % 256)
	}

	key := "large_file.bin"
	err := backend.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "application/octet-stream")
	if err != nil {
		t.Fatalf("Failed to put large file: %v", err)
	}

	// Verify file exists and has correct size
	fullPath := filepath.Join("./test_storage", "large", key)
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("Failed to stat large file: %v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Errorf("File size = %d, want %d", info.Size(), len(content))
	}
}

func TestLocalBackendSpecialCharacters(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "special/",
		LocalPath:  "./test_storage",
	}

	backend, _ := NewBackend(cfg)
	ctx := context.Background()

	// Test file with special characters in name (but safe for filesystem)
	key := "file-with_special.chars.txt"
	content := []byte("special content")

	err := backend.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("Failed to put file with special chars: %v", err)
	}

	exists, _ := backend.Exists(ctx, key)
	if !exists {
		t.Error("File with special chars should exist")
	}
}

func TestLocalBackendMultipleOperations(t *testing.T) {
	cfg := Config{
		Type:       "local",
		PathPrefix: "multi/",
		LocalPath:  "./test_storage",
	}

	backend, _ := NewBackend(cfg)
	ctx := context.Background()

	// Create multiple files
	files := map[string]string{
		"file1.txt": "content1",
		"file2.txt": "content2",
		"file3.txt": "content3",
	}

	for key, content := range files {
		data := []byte(content)
		err := backend.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain")
		if err != nil {
			t.Fatalf("Failed to put %s: %v", key, err)
		}
	}

	// Verify all files exist
	for key := range files {
		exists, err := backend.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed for %s: %v", key, err)
		}
		if !exists {
			t.Errorf("File %s should exist", key)
		}
	}

	// Delete all files
	for key := range files {
		err := backend.Delete(ctx, key)
		if err != nil {
			t.Fatalf("Failed to delete %s: %v", key, err)
		}
	}

	// Verify all files are deleted
	for key := range files {
		exists, _ := backend.Exists(ctx, key)
		if exists {
			t.Errorf("File %s should not exist after delete", key)
		}
	}
}

// Test S3Backend creation (without actual S3 connection)
func TestS3BackendType(t *testing.T) {
	// This test just verifies the S3Backend type exists
	// We can't test actual S3 operations without credentials
	cfg := Config{
		Type:     "s3",
		Bucket:   "test-bucket",
		Region:   "us-east-1",
		Endpoint: "",
	}

	// This will fail because no credentials, but verifies the code compiles
	_, err := NewBackend(cfg)
	// We expect an error due to missing credentials
	if err == nil {
		t.Skip("S3 backend created without credentials - unexpected in CI environment")
	}
}

// Test MinIOBackend creation (without actual MinIO connection)
func TestMinIOBackendType(t *testing.T) {
	// This test just verifies the MinIOBackend type exists
	cfg := Config{
		Type:     "minio",
		Bucket:   "test-bucket",
		Region:   "us-east-1",
		Endpoint: "http://localhost:9000",
	}

	// This will fail because no server, but verifies the code compiles
	_, err := NewBackend(cfg)
	// We expect an error due to connection failure
	if err == nil {
		t.Skip("MinIO backend created without server - unexpected in CI environment")
	}
}
