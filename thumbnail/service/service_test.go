package service

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail/storage"
)

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()
	// Cleanup
	os.RemoveAll("./test_storage")
	os.Exit(code)
}

func TestNewService(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled:        true,
		AccessMode:     models.AccessModeProxy,
		RewriteEnabled: true,
		Storage: models.ThumbnailStorageConfig{
			Type:       models.StorageTypeLocal,
			Bucket:     "test-bucket",
			PathPrefix: "thumbnails/",
			LocalPath:  "./test_storage",
			PublicURL:  "",
		},
		Rules: []models.ThumbnailRule{
			{
				ContentTypes:    []string{"image/jpeg", "image/png"},
				Extensions:      []string{"jpg", "jpeg", "png"},
				MaxOriginalSize: 524288,
				MaxWidth:        1920,
				MaxHeight:       1080,
				Quality:         85,
				Format:          "jpeg",
				Enabled:         true,
				MediaType:       "image",
			},
		},
	}

	storageCfg := storage.Config{
		Type:       "local",
		Bucket:     "test-bucket",
		PathPrefix: "thumbnails/",
		LocalPath:  "./test_storage",
	}

	backend, err := storage.NewBackend(storageCfg)
	if err != nil {
		t.Fatalf("Failed to create storage backend: %v", err)
	}

	svc, err := NewService(cfg, backend)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	if svc == nil {
		t.Fatal("Service is nil")
	}

	if !svc.IsEnabled() {
		t.Error("Service should be enabled")
	}

	if svc.GetConfig() == nil {
		t.Error("Config should not be nil")
	}

	if svc.GetStorage() == nil {
		t.Error("Storage should not be nil")
	}
}

func TestMatchRule(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				ContentTypes:    []string{"image/jpeg"},
				Extensions:      []string{"jpg", "jpeg"},
				MaxOriginalSize: 100000,
				Enabled:         true,
				MediaType:       "image",
			},
			{
				ContentTypes:    []string{"video/mp4"},
				Extensions:      []string{"mp4"},
				MaxOriginalSize: 10485760,
				Enabled:         true,
				MediaType:       "video",
			},
		},
	}

	storageCfg := storage.Config{
		Type:      "local",
		LocalPath: "./test_storage",
	}

	backend, _ := storage.NewBackend(storageCfg)
	svc, _ := NewService(cfg, backend)

	tests := []struct {
		url               string
		expectedMatch     bool
		expectedMediaType string
	}{
		{"https://example.com/image.jpg", true, "image"},
		{"https://example.com/image.jpeg", true, "image"},
		{"https://example.com/image.png", false, ""},
		{"https://example.com/image", false, ""},
		{"https://example.com/video.mp4", true, "video"},
		{"https://example.com/video.avi", false, ""},
	}

	for _, tt := range tests {
		match, rule, mediaType := svc.matchRule(tt.url)
		if match != tt.expectedMatch {
			t.Errorf("matchRule(%s) match = %v, want %v", tt.url, match, tt.expectedMatch)
		}
		if match && rule == nil {
			t.Errorf("matchRule(%s) returned nil rule", tt.url)
		}
		if match && mediaType != tt.expectedMediaType {
			t.Errorf("matchRule(%s) mediaType = %v, want %v", tt.url, mediaType, tt.expectedMediaType)
		}
	}
}

func TestGenerateCacheKey(t *testing.T) {
	url := "https://example.com/image.jpg"
	format := "webp"

	key1 := GenerateCacheKey(url, format)
	key2 := GenerateCacheKey(url, format)

	if key1 == "" {
		t.Error("Cache key should not be empty")
	}

	if key1 != key2 {
		t.Error("Same URL and format should generate same key")
	}

	// Different URL should generate different key
	key3 := GenerateCacheKey("https://example.com/other.jpg", format)
	if key1 == key3 {
		t.Error("Different URLs should generate different keys")
	}

	// Different format should generate different key
	key4 := GenerateCacheKey(url, "png")
	if key1 == key4 {
		t.Error("Different formats should generate different keys")
	}
}

func TestStorageOperations(t *testing.T) {
	storageCfg := storage.Config{
		Type:       "local",
		PathPrefix: "test/",
		LocalPath:  "./test_storage",
	}

	backend, err := storage.NewBackend(storageCfg)
	if err != nil {
		t.Fatalf("Failed to create storage backend: %v", err)
	}

	ctx := context.Background()
	key := "test_file.txt"
	content := []byte("Hello, World!")

	// Test Put
	err = backend.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("Failed to put file: %v", err)
	}

	// Test Exists
	exists, err := backend.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Failed to check file existence: %v", err)
	}
	if !exists {
		t.Error("File should exist")
	}

	// Test GetURL
	url := backend.GetURL(key)
	if url == "" {
		t.Error("URL should not be empty")
	}

	// Test Delete
	err = backend.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Failed to delete file: %v", err)
	}

	// Check existence after delete
	exists, err = backend.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Failed to check file existence after delete: %v", err)
	}
	if exists {
		t.Error("File should not exist after delete")
	}
}

func TestThumbnailStats(t *testing.T) {
	// Skip this test if database is not available
	// In a real environment, this would need database initialization
	t.Skip("Skipping test that requires database connection")

	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Storage: models.ThumbnailStorageConfig{
			Type:      models.StorageTypeLocal,
			LocalPath: "./test_storage",
		},
	}

	storageCfg := storage.Config{
		Type:      "local",
		LocalPath: "./test_storage",
	}

	backend, _ := storage.NewBackend(storageCfg)
	svc, _ := NewService(cfg, backend)

	stats, err := svc.GetThumbnailStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	// Stats may be empty but should not error
	if stats == nil {
		t.Error("Stats should not be nil")
	}
}

func TestWorkerLifecycle(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Storage: models.ThumbnailStorageConfig{
			Type:      models.StorageTypeLocal,
			LocalPath: "./test_storage",
		},
	}

	storageCfg := storage.Config{
		Type:      "local",
		LocalPath: "./test_storage",
	}

	backend, _ := storage.NewBackend(storageCfg)
	worker := NewWorker(cfg, backend)

	// Start worker
	worker.Start(2)

	// Wait a bit for workers to start
	time.Sleep(100 * time.Millisecond)

	// Stop worker
	worker.Stop()
}

func TestConfigAccessMode(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled:    true,
		AccessMode: models.AccessModeProxy,
		Storage: models.ThumbnailStorageConfig{
			Type:      models.StorageTypeLocal,
			LocalPath: "./test_storage",
		},
	}

	storageCfg := storage.Config{
		Type:      "local",
		LocalPath: "./test_storage",
	}

	backend, _ := storage.NewBackend(storageCfg)
	svc, _ := NewService(cfg, backend)

	if svc.GetConfig().AccessMode != models.AccessModeProxy {
		t.Error("AccessMode should be Proxy")
	}
}

func TestConfigRewriteEnabled(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled:        true,
		RewriteEnabled: true,
		Storage: models.ThumbnailStorageConfig{
			Type:      models.StorageTypeLocal,
			LocalPath: "./test_storage",
		},
	}

	storageCfg := storage.Config{
		Type:      "local",
		LocalPath: "./test_storage",
	}

	backend, _ := storage.NewBackend(storageCfg)
	svc, _ := NewService(cfg, backend)

	if !svc.GetConfig().RewriteEnabled {
		t.Error("RewriteEnabled should be true")
	}
}
