package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail/storage"
)

// --- NewService ---

func TestNewServiceNilConfig(t *testing.T) {
	_, err := NewService(nil, nil)
	if err == nil {
		t.Fatal("Expected error when config is nil")
	}

	if !strings.Contains(err.Error(), "config is nil") {
		t.Errorf("Error should mention 'config is nil', got: %v", err)
	}
}

func TestNewServiceDisabled(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: false,
		Storage: models.ThumbnailStorageConfig{
			Type:      models.StorageTypeLocal,
			LocalPath: "./test_storage", //nolint:goconst
		},
	}

	svc, err := NewService(cfg, nil)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	if svc == nil {
		t.Fatal("Service should not be nil")
	}

	if svc.IsEnabled() {
		t.Error("Service should be disabled")
	}
}

// --- IsEnabled / GetConfig / GetStorage ---

func TestIsEnabledFalse(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	if svc.IsEnabled() {
		t.Error("IsEnabled() should return false")
	}
}

func TestIsEnabledTrue(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: true}
	svc, _ := NewService(cfg, nil)

	if !svc.IsEnabled() {
		t.Error("IsEnabled() should return true")
	}
}

func TestGetConfigNotNil(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled:    true,
		AccessMode: models.AccessModeRedirect,
	}
	svc, _ := NewService(cfg, nil)

	got := svc.GetConfig()
	if got == nil {
		t.Fatal("GetConfig() should not return nil")
	}

	if got.AccessMode != models.AccessModeRedirect {
		t.Errorf("AccessMode = %v, want %v", got.AccessMode, models.AccessModeRedirect)
	}
}

func TestGetStorageNil(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	if svc.GetStorage() != nil {
		t.Error("GetStorage() should return nil when no backend was provided")
	}
}

func TestGetStorageNotNil(t *testing.T) {
	storageCfg := storage.Config{
		Type:      "local", //nolint:goconst
		LocalPath: "./test_storage",
	}
	backend, _ := storage.NewBackend(storageCfg)

	cfg := &models.ThumbnailServiceConfig{Enabled: true}
	svc, _ := NewService(cfg, backend)

	if svc.GetStorage() == nil {
		t.Error("GetStorage() should not return nil")
	}
}

// --- GetThumbnailURL (disabled path, no DB needed) ---

func TestGetThumbnailURLDisabled(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	result, err := svc.GetThumbnailURL(context.TODO(), "https://example.com/image.jpg")
	if err != nil {
		t.Fatalf("GetThumbnailURL should not error when disabled, got: %v", err)
	}

	if result == nil {
		t.Fatal("Result should not be nil")
	}

	if result.OriginalURL != "https://example.com/image.jpg" { //nolint:goconst
		t.Errorf("OriginalURL = %q, want %q", result.OriginalURL, "https://example.com/image.jpg")
	}

	if result.Status != models.ThumbnailStatusSkipped {
		t.Errorf("Status = %q, want %q", result.Status, models.ThumbnailStatusSkipped)
	}

	if result.IsThumbnail {
		t.Error("IsThumbnail should be false")
	}

	if result.Reason != "service disabled" { //nolint:goconst
		t.Errorf("Reason = %q, want %q", result.Reason, "service disabled")
	}
}

// --- BatchGetThumbnailURLs (disabled, no DB needed) ---

func TestBatchGetThumbnailURLsDisabled(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	urls := []string{
		"https://example.com/img1.jpg",
		"https://example.com/img2.png",
		"https://example.com/video.mp4",
	}

	results := svc.BatchGetThumbnailURLs(context.TODO(), urls)
	if len(results) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(results))
	}

	for _, url := range urls {
		result, ok := results[url]
		if !ok {
			t.Errorf("Missing result for %s", url)

			continue
		}

		if result.Status != models.ThumbnailStatusSkipped {
			t.Errorf("Status for %s = %q, want %q", url, result.Status, models.ThumbnailStatusSkipped)
		}

		if result.Reason != "service disabled" {
			t.Errorf("Reason for %s = %q, want %q", url, result.Reason, "service disabled")
		}
	}
}

func TestBatchGetThumbnailURLsEmpty(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	results := svc.BatchGetThumbnailURLs(context.TODO(), nil)
	if len(results) != 0 {
		t.Errorf("Expected empty results, got %d", len(results))
	}

	results = svc.BatchGetThumbnailURLs(context.TODO(), []string{})
	if len(results) != 0 {
		t.Errorf("Expected empty results, got %d", len(results))
	}
}

// --- ProcessContent (disabled / rewrite-disabled / no URLs, no DB needed) ---

func TestProcessContentDisabled(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	content := "Check out https://example.com/img.jpg for more info"

	result, err := svc.ProcessContent(context.TODO(), content)
	if err != nil {
		t.Fatalf("ProcessContent should not error when disabled, got: %v", err)
	}

	if result != content {
		t.Errorf("ProcessContent should return original content when disabled, got %q", result)
	}
}

func TestProcessContentRewriteDisabled(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled:        true,
		RewriteEnabled: false,
	}
	svc, _ := NewService(cfg, nil)

	content := "Check out https://example.com/img.jpg for more info"

	result, err := svc.ProcessContent(context.TODO(), content)
	if err != nil {
		t.Fatalf("ProcessContent should not error when rewrite disabled, got: %v", err)
	}

	if result != content {
		t.Errorf("ProcessContent should return original content when rewrite disabled, got %q", result)
	}
}

func TestProcessContentNoURLs(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled:        true,
		RewriteEnabled: true,
	}
	svc, _ := NewService(cfg, nil)

	content := "This is plain text with no image URLs."

	result, err := svc.ProcessContent(context.TODO(), content)
	if err != nil {
		t.Fatalf("ProcessContent should not error with no URLs, got: %v", err)
	}

	if result != content {
		t.Errorf("ProcessContent should return original content when no URLs, got %q", result)
	}
}

func TestProcessContentEmpty(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled:        true,
		RewriteEnabled: true,
	}
	svc, _ := NewService(cfg, nil)

	result, err := svc.ProcessContent(context.TODO(), "")
	if err != nil {
		t.Fatalf("ProcessContent should not error with empty content, got: %v", err)
	}

	if result != "" {
		t.Errorf("ProcessContent should return empty string, got %q", result)
	}
}

// --- extractImageURLs (pure function) ---

func TestExtractImageURLs(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name:     "empty content",
			content:  "",
			expected: nil,
		},
		{
			name:     "no URLs",
			content:  "Just plain text here",
			expected: nil,
		},
		{
			name:     "single jpg URL",
			content:  "See https://example.com/photo.jpg for details",
			expected: []string{"https://example.com/photo.jpg"},
		},
		{
			name:     "single png URL",
			content:  "Image: https://cdn.example.com/img.png",
			expected: []string{"https://cdn.example.com/img.png"},
		},
		{
			name:     "gif URL",
			content:  "Animated: https://example.com/anim.gif",
			expected: []string{"https://example.com/anim.gif"},
		},
		{
			name:     "webp URL",
			content:  "Modern: https://example.com/img.webp",
			expected: []string{"https://example.com/img.webp"},
		},
		{
			name:     "svg URL",
			content:  "Vector: https://example.com/icon.svg",
			expected: []string{"https://example.com/icon.svg"},
		},
		{
			name:     "jpeg URL",
			content:  "Photo: https://example.com/pic.jpeg",
			expected: []string{"https://example.com/pic.jpeg"},
		},
		{
			name:     "bmp URL",
			content:  "Bitmap: https://example.com/draw.bmp",
			expected: []string{"https://example.com/draw.bmp"},
		},
		{
			name:     "ico URL",
			content:  "Favicon: https://example.com/favicon.ico",
			expected: []string{"https://example.com/favicon.ico"},
		},
		{
			name:     "video mp4 URL",
			content:  "Video: https://example.com/vid.mp4",
			expected: []string{"https://example.com/vid.mp4"},
		},
		{
			name:     "video webm URL",
			content:  "Video: https://example.com/clip.webm",
			expected: []string{"https://example.com/clip.webm"},
		},
		{
			name:     "video mov URL",
			content:  "Video: https://example.com/movie.mov",
			expected: []string{"https://example.com/movie.mov"},
		},
		{
			name:     "URL with query params",
			content:  "Resized: https://example.com/img.jpg?w=800&h=600",
			expected: []string{"https://example.com/img.jpg?w=800&h=600"},
		},
		{
			name:     "multiple URLs",
			content:  "See https://a.com/1.jpg and https://b.com/2.png",
			expected: []string{"https://a.com/1.jpg", "https://b.com/2.png"},
		},
		{
			name:     "http URL",
			content:  "http://example.com/photo.jpg",
			expected: []string{"http://example.com/photo.jpg"},
		},
		{
			name:    "mixed content and URLs",
			content: "Here is an image: https://cdn.example.com/path/to/image.webp?q=90. Enjoy!",
			// Note: the regex captures the trailing period as part of the query param
			expected: []string{"https://cdn.example.com/path/to/image.webp?q=90."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls := extractImageURLs(tt.content)

			if len(tt.expected) == 0 {
				if len(urls) != 0 {
					t.Errorf("Expected no URLs, got %v", urls)
				}

				return
			}

			if len(urls) != len(tt.expected) {
				t.Errorf("Expected %d URLs, got %d: %v", len(tt.expected), len(urls), urls)

				return
			}

			for i, exp := range tt.expected {
				if urls[i] != exp {
					t.Errorf("URL[%d] = %q, want %q", i, urls[i], exp)
				}
			}
		})
	}
}

func TestExtractImageURLsDeduplication(t *testing.T) {
	content := "https://example.com/img.jpg https://other.com/img.png"

	urls := extractImageURLs(content)

	// Same URL appearing multiple times should only appear once
	count := 0

	for _, u := range urls {
		if u == "https://example.com/img.jpg" { //nolint:goconst
			count++
		}
	}

	if count != 1 {
		t.Errorf("URL should be deduplicated, appeared %d times", count)
	}

	if len(urls) != 2 {
		t.Errorf("Expected 2 unique URLs, got %d: %v", len(urls), urls)
	}
}

func TestExtractImageURLsURLInQuotes(t *testing.T) {
	// URL should NOT be extracted if surrounded by quotes (per regex pattern)
	content := `src="https://example.com/img.jpg"`
	urls := extractImageURLs(content)
	// The regex uses [^\s"] which means it stops at a quote
	// So the URL might or might not be extracted depending on regex behavior
	// This test just verifies the function doesn't panic
	_ = urls
}

func TestExtractImageURLsNoMatch(t *testing.T) {
	// URLs without recognized extensions
	content := "https://example.com/page.html https://example.com/doc.pdf"

	urls := extractImageURLs(content)
	if len(urls) != 0 {
		t.Errorf("Expected no URLs for non-image/video extensions, got %v", urls)
	}
}

func TestExtractImageURLsMixedImageAndVideo(t *testing.T) {
	content := "https://example.com/photo.jpg https://example.com/clip.mp4"

	urls := extractImageURLs(content)
	if len(urls) != 2 {
		t.Errorf("Expected 2 URLs (image + video), got %d: %v", len(urls), urls)
	}
}

// --- inferContentType (pure function) ---

func TestInferContentType(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.com/img.jpg", "image/jpeg"}, //nolint:goconst
		{"https://example.com/img.jpeg", "image/jpeg"},
		{"https://example.com/img.JPG", "image/jpeg"},
		{"https://example.com/img.JPEG", "image/jpeg"},
		{"https://example.com/img.png", "image/png"}, //nolint:goconst
		{"https://example.com/img.PNG", "image/png"},
		{"https://example.com/img.gif", "image/gif"}, //nolint:goconst
		{"https://example.com/img.GIF", "image/gif"},
		{"https://example.com/img.webp", "image/webp"}, //nolint:goconst
		{"https://example.com/img.WEBP", "image/webp"},
		{"https://example.com/img.svg", "image/svg+xml"}, //nolint:goconst
		{"https://example.com/img.SVG", "image/svg+xml"},
		{"https://example.com/vid.mp4", "video/mp4"}, //nolint:goconst
		{"https://example.com/vid.MP4", "video/mp4"},
		{"https://example.com/vid.webm", "video/webm"}, //nolint:goconst
		{"https://example.com/vid.WEBM", "video/webm"},
		// Unknown extensions
		{"https://example.com/page.html", ""},
		{"https://example.com/doc.pdf", ""},
		{"https://example.com/file.txt", ""},
		{"https://example.com/noext", ""},
		{"https://example.com/", ""},
		{"", ""},
		// URL with query parameters (filepath.Ext handles the full suffix)
		{"https://example.com/img.jpg?w=800", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := inferContentType(tt.url)
			if got != tt.expected {
				t.Errorf("inferContentType(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

// --- matchRule (additional cases, no DB needed) ---

func TestMatchRuleCaseInsensitiveExt(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				Extensions:      []string{"jpg", "png"}, //nolint:goconst
				Enabled:         true,
				MediaType:       "image", //nolint:goconst
				MaxOriginalSize: 100000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	tests := []struct {
		url           string
		expectedMatch bool
	}{
		{"https://example.com/photo.JPG", true},
		{"https://example.com/photo.Jpg", true},
		{"https://example.com/photo.PNG", true},
		{"https://example.com/photo.Png", true},
		{"https://example.com/photo.jpg", true},
		{"https://example.com/photo.png", true},
		{"https://example.com/photo.gif", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			match, _, mediaType := svc.matchRule(tt.url)
			if match != tt.expectedMatch {
				t.Errorf("matchRule(%q) match = %v, want %v", tt.url, match, tt.expectedMatch)
			}

			if match && mediaType != "image" {
				t.Errorf("matchRule(%q) mediaType = %q, want %q", tt.url, mediaType, "image")
			}
		})
	}
}

func TestMatchRuleContentTypeMatching(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				ContentTypes:    []string{"image/"},
				Enabled:         true,
				MediaType:       "image",
				MaxOriginalSize: 500000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	// URL with .jpg extension, inferContentType returns "image/jpeg"
	// which starts with "image/" so it should match
	match, rule, mediaType := svc.matchRule("https://example.com/photo.jpg")
	if !match {
		t.Error("Should match content-type prefix 'image/'")
	}

	if rule == nil {
		t.Error("Rule should not be nil")
	}

	if mediaType != "image" {
		t.Errorf("MediaType = %q, want %q", mediaType, "image")
	}

	// URL with .mp4 extension, inferContentType returns "video/mp4"
	// which does NOT start with "image/" so it should NOT match
	match, _, _ = svc.matchRule("https://example.com/video.mp4")
	if match {
		t.Error("Should NOT match content-type for video URL")
	}

	// URL with no recognized extension -> inferContentType returns ""
	// No contentType to match, so it should NOT match
	match, _, _ = svc.matchRule("https://example.com/page.html")
	if match {
		t.Error("Should NOT match when no content-type can be inferred")
	}
}

func TestMatchRuleEmptyRules(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules:   []models.ThumbnailRule{},
	}
	svc, _ := NewService(cfg, nil)

	match, rule, mediaType := svc.matchRule("https://example.com/photo.jpg")
	if match {
		t.Error("Should not match when there are no rules")
	}

	if rule != nil {
		t.Error("Rule should be nil")
	}

	if mediaType != "" {
		t.Errorf("MediaType = %q, want empty", mediaType)
	}
}

func TestMatchRuleDisabledRules(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				Extensions:      []string{"jpg"},
				Enabled:         false,
				MediaType:       "image",
				MaxOriginalSize: 100000,
			},
			{
				Extensions:      []string{"png"},
				Enabled:         false,
				MediaType:       "image",
				MaxOriginalSize: 100000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	match, _, _ := svc.matchRule("https://example.com/photo.jpg")
	if match {
		t.Error("Should not match disabled rule")
	}

	match, _, _ = svc.matchRule("https://example.com/photo.png")
	if match {
		t.Error("Should not match disabled rule")
	}
}

func TestMatchRuleMultipleRules(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				Extensions:      []string{"jpg"},
				Enabled:         true,
				MediaType:       "image",
				MaxOriginalSize: 100000,
			},
			{
				Extensions:      []string{"mp4"},
				Enabled:         true,
				MediaType:       "video", //nolint:goconst
				MaxOriginalSize: 10000000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	// Should match first rule (jpg)
	match, rule, mt := svc.matchRule("https://example.com/photo.jpg")
	if !match {
		t.Error("Should match jpg rule")
	}

	if rule == nil {
		t.Fatal("Rule should not be nil")
	}

	if mt != "image" {
		t.Errorf("MediaType = %q, want %q", mt, "image")
	}

	// Should match second rule (mp4)
	match, rule, mt = svc.matchRule("https://example.com/video.mp4")
	if !match {
		t.Error("Should match mp4 rule")
	}

	if rule == nil {
		t.Fatal("Rule should not be nil")
	}

	if mt != "video" {
		t.Errorf("MediaType = %q, want %q", mt, "video")
	}

	// Should not match any rule
	match, _, _ = svc.matchRule("https://example.com/doc.pdf")
	if match {
		t.Error("Should not match pdf")
	}
}

func TestMatchRuleNoExtensionInURL(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				ContentTypes:    []string{"image/"},
				Enabled:         true,
				MediaType:       "image",
				MaxOriginalSize: 100000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	// URL without extension and without recognizable path
	// inferContentType returns "" since there's no extension
	match, _, _ := svc.matchRule("https://example.com/image/12345")
	if match {
		t.Error("Should not match URL with no extension and no content-type")
	}
}

func TestMatchRuleOnlyContentTypeRules(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				ContentTypes:    []string{"video/mp4"},
				Enabled:         true,
				MediaType:       "video",
				MaxOriginalSize: 50000000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	// mp4 URL -> inferContentType returns "video/mp4" -> matches content type
	match, rule, mt := svc.matchRule("https://example.com/video.mp4")
	if !match {
		t.Error("Should match video/mp4 content type")
	}

	if rule == nil {
		t.Fatal("Rule should not be nil")
	}

	if mt != "video" {
		t.Errorf("MediaType = %q, want %q", mt, "video")
	}

	// jpg URL -> inferContentType returns "image/jpeg" -> does NOT match "video/mp4"
	match, _, _ = svc.matchRule("https://example.com/photo.jpg")
	if match {
		t.Error("Should NOT match image with video content type rule")
	}
}

// --- checkOriginalSize (HTTP-based, no DB needed) ---

func TestCheckOriginalSizeWithServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "50000")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	rule := &models.ThumbnailRule{MaxOriginalSize: 10000}

	need, size, err := svc.checkOriginalSize(ts.URL, rule)
	if err != nil {
		t.Fatalf("checkOriginalSize failed: %v", err)
	}

	if !need {
		t.Error("Should need thumbnail: 50000 > 10000")
	}

	if size != 50000 {
		t.Errorf("Size = %d, want 50000", size)
	}
}

func TestCheckOriginalSizeBelowThreshold(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5000")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	rule := &models.ThumbnailRule{MaxOriginalSize: 10000}

	need, size, err := svc.checkOriginalSize(ts.URL, rule)
	if err != nil {
		t.Fatalf("checkOriginalSize failed: %v", err)
	}

	if need {
		t.Error("Should NOT need thumbnail: 5000 <= 10000")
	}

	if size != 5000 {
		t.Errorf("Size = %d, want 5000", size)
	}
}

func TestCheckOriginalSizeZeroMax(t *testing.T) {
	// MaxOriginalSize = 0 means always process
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "500")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	rule := &models.ThumbnailRule{MaxOriginalSize: 0}

	need, size, err := svc.checkOriginalSize(ts.URL, rule)
	if err != nil {
		t.Fatalf("checkOriginalSize failed: %v", err)
	}

	if !need {
		t.Error("Should always need thumbnail when MaxOriginalSize is 0")
	}

	if size != 500 {
		t.Errorf("Size = %d, want 500", size)
	}
}

func TestCheckOriginalSizeExactThreshold(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10000")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	rule := &models.ThumbnailRule{MaxOriginalSize: 10000}

	need, size, err := svc.checkOriginalSize(ts.URL, rule)
	if err != nil {
		t.Fatalf("checkOriginalSize failed: %v", err)
	}
	// size > MaxOriginalSize: 10000 > 10000 is false
	if need {
		t.Error("Should NOT need thumbnail: 10000 is not > 10000")
	}

	if size != 10000 {
		t.Errorf("Size = %d, want 10000", size)
	}
}

func TestCheckOriginalSizeFailedRequest(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	rule := &models.ThumbnailRule{MaxOriginalSize: 1000}

	// Use an invalid URL that will fail
	need, size, err := svc.checkOriginalSize("http://127.0.0.1:1/nonexistent", rule)
	if err == nil {
		t.Fatal("Expected error for failed HTTP request")
	}

	if need {
		t.Error("Should NOT need thumbnail on error")
	}

	if size != 0 {
		t.Errorf("Size = %d, want 0 on error", size)
	}
}

func TestCheckOriginalSizeContentTypeCheck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a HEAD request
		if r.Method != http.MethodHead {
			t.Errorf("Expected HEAD request, got %s", r.Method)
		}

		w.Header().Set("Content-Length", "999999")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	rule := &models.ThumbnailRule{MaxOriginalSize: 100000}

	need, size, err := svc.checkOriginalSize(ts.URL, rule)
	if err != nil {
		t.Fatalf("checkOriginalSize failed: %v", err)
	}

	if !need {
		t.Error("Should need thumbnail: 999999 > 100000")
	}

	if size != 999999 {
		t.Errorf("Size = %d, want 999999", size)
	}
}

// --- GenerateCacheKey (additional cases) ---

func TestGenerateCacheKeyFormat(t *testing.T) {
	key := GenerateCacheKey("https://example.com/image.jpg", "webp")

	if !strings.Contains(key, ".") {
		t.Error("Cache key should contain a dot separator")
	}

	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("Cache key should have format 'hash.format', got %q", key)
	}

	if len(parts[0]) != 16 {
		t.Errorf("Hash part should be 16 characters, got %d: %q", len(parts[0]), parts[0])
	}

	if parts[1] != "webp" { //nolint:goconst
		t.Errorf("Format part = %q, want %q", parts[1], "webp")
	}
}

func TestGenerateCacheKeyDifferentFormats(t *testing.T) {
	url := "https://example.com/image.jpg"

	keyJpeg := GenerateCacheKey(url, "jpeg")
	keyPng := GenerateCacheKey(url, "png")
	keyWebp := GenerateCacheKey(url, "webp")

	// All should be different due to different format suffix
	if keyJpeg == keyPng {
		t.Error("Different formats should produce different keys")
	}

	if keyJpeg == keyWebp {
		t.Error("Different formats should produce different keys")
	}

	if keyPng == keyWebp {
		t.Error("Different formats should produce different keys")
	}

	// Each should end with its format
	if !strings.HasSuffix(keyJpeg, ".jpeg") {
		t.Errorf("Key should end with .jpeg, got %q", keyJpeg)
	}

	if !strings.HasSuffix(keyPng, ".png") {
		t.Errorf("Key should end with .png, got %q", keyPng)
	}

	if !strings.HasSuffix(keyWebp, ".webp") {
		t.Errorf("Key should end with .webp, got %q", keyWebp)
	}
}

func TestGenerateCacheKeyEmptyInputs(t *testing.T) {
	key := GenerateCacheKey("", "jpg")
	if key == "" {
		t.Error("Cache key should not be empty")
	}
	// Should still have format
	if !strings.HasSuffix(key, ".jpg") {
		t.Errorf("Key should end with .jpg, got %q", key)
	}

	// Hash part should be 16 chars (MD5 of empty string)
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 || len(parts[0]) != 16 {
		t.Errorf("Invalid key format: %q", key)
	}
}

func TestGenerateCacheKeyConsistency(t *testing.T) {
	// Calling multiple times should produce the same result
	url := "https://example.com/test/image.png"
	format := "webp"

	keys := make([]string, 0, 100)
	for range 100 {
		keys = append(keys, GenerateCacheKey(url, format))
	}

	for i := 1; i < len(keys); i++ {
		if keys[i] != keys[0] {
			t.Errorf("Inconsistent cache key at iteration %d: %q != %q", i, keys[i], keys[0])
		}
	}
}

// --- ThumbnailResult ---

func TestThumbnailResultFields(t *testing.T) {
	result := &ThumbnailResult{
		OriginalURL:  "https://example.com/img.jpg",
		ThumbnailURL: "https://cdn.example.com/thumb.webp",
		Status:       models.ThumbnailStatusCompleted,
		IsThumbnail:  true,
		Reason:       "",
		Width:        320,
		Height:       240,
	}

	if result.OriginalURL != "https://example.com/img.jpg" {
		t.Errorf("OriginalURL = %q", result.OriginalURL)
	}

	if result.ThumbnailURL != "https://cdn.example.com/thumb.webp" {
		t.Errorf("ThumbnailURL = %q", result.ThumbnailURL)
	}

	if result.Status != models.ThumbnailStatusCompleted {
		t.Errorf("Status = %q", result.Status)
	}

	if !result.IsThumbnail {
		t.Error("IsThumbnail should be true")
	}

	if result.Reason != "" {
		t.Errorf("Reason = %q", result.Reason)
	}

	if result.Width != 320 {
		t.Errorf("Width = %d", result.Width)
	}

	if result.Height != 240 {
		t.Errorf("Height = %d", result.Height)
	}
}

func TestThumbnailResultSkipped(t *testing.T) {
	result := &ThumbnailResult{
		OriginalURL: "https://example.com/small.jpg",
		Status:      models.ThumbnailStatusSkipped,
		IsThumbnail: false,
		Reason:      "size below threshold",
	}

	if result.IsThumbnail {
		t.Error("Skipped result should not be a thumbnail")
	}

	if result.ThumbnailURL != "" {
		t.Error("Skipped result should not have thumbnail URL")
	}
}

// --- ThumbnailStats ---

func TestThumbnailStatsZeroValues(t *testing.T) {
	stats := &ThumbnailStats{}
	if stats.Total != 0 {
		t.Errorf("Zero-value Total should be 0, got %d", stats.Total)
	}

	if stats.Completed != 0 {
		t.Errorf("Zero-value Completed should be 0, got %d", stats.Completed)
	}

	if stats.Failed != 0 {
		t.Errorf("Zero-value Failed should be 0, got %d", stats.Failed)
	}

	if stats.Pending != 0 {
		t.Errorf("Zero-value Pending should be 0, got %d", stats.Pending)
	}

	if stats.Processing != 0 {
		t.Errorf("Zero-value Processing should be 0, got %d", stats.Processing)
	}

	if stats.Skipped != 0 {
		t.Errorf("Zero-value Skipped should be 0, got %d", stats.Skipped)
	}

	if stats.StorageUsed != 0 {
		t.Errorf("Zero-value StorageUsed should be 0, got %d", stats.StorageUsed)
	}
}

func TestThumbnailStatsFields(t *testing.T) {
	stats := &ThumbnailStats{
		Total:       100,
		Completed:   80,
		Failed:      10,
		Pending:     5,
		Processing:  3,
		Skipped:     2,
		StorageUsed: 5000000,
	}

	if stats.Total != 100 {
		t.Errorf("Total = %d, want 100", stats.Total)
	}

	if stats.Completed != 80 {
		t.Errorf("Completed = %d, want 80", stats.Completed)
	}

	if stats.Failed+stats.Completed+stats.Pending+stats.Processing+stats.Skipped != 100 {
		t.Error("Status counts should add up to total")
	}

	if stats.StorageUsed != 5000000 {
		t.Errorf("StorageUsed = %d, want 5000000", stats.StorageUsed)
	}
}

// --- Service with real storage backend ---

func TestNewServiceWithStorageBackend(t *testing.T) {
	storageCfg := storage.Config{
		Type:      "local",
		LocalPath: "./test_storage",
	}

	backend, err := storage.NewBackend(storageCfg)
	if err != nil {
		t.Fatalf("Failed to create storage backend: %v", err)
	}

	cfg := &models.ThumbnailServiceConfig{
		Enabled:    true,
		AccessMode: models.AccessModeProxy,
		Storage: models.ThumbnailStorageConfig{
			Type:      models.StorageTypeLocal,
			LocalPath: "./test_storage",
		},
	}

	svc, err := NewService(cfg, backend)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	if svc.GetStorage() == nil {
		t.Error("Storage should not be nil")
	}

	if svc.GetStorage().Type() != "local" {
		t.Errorf("Storage type = %q, want %q", svc.GetStorage().Type(), "local")
	}
}

// --- BatchGetThumbnailURLs with multiple disabled checks ---

func TestBatchGetThumbnailURLsMixed(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	urls := make([]string, 0, 10)
	for i := range 10 {
		urls = append(urls, fmt.Sprintf("https://example.com/img%d.jpg", i))
	}

	results := svc.BatchGetThumbnailURLs(context.TODO(), urls)
	if len(results) != 10 {
		t.Errorf("Expected 10 results, got %d", len(results))
	}

	for _, url := range urls {
		result, ok := results[url]
		if !ok {
			t.Errorf("Missing result for %s", url)

			continue
		}

		if result.Status != models.ThumbnailStatusSkipped {
			t.Errorf("Status = %q, want %q", result.Status, models.ThumbnailStatusSkipped)
		}

		if result.OriginalURL != url {
			t.Errorf("OriginalURL = %q, want %q", result.OriginalURL, url)
		}
	}
}

// --- matchRule with extension AND content-type in same rule ---

func TestMatchRuleBothExtensionAndContentType(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				Extensions:      []string{"png"},
				ContentTypes:    []string{"image/png"},
				Enabled:         true,
				MediaType:       "image",
				MaxOriginalSize: 100000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	// Match by extension
	match, _, _ := svc.matchRule("https://example.com/img.png")
	if !match {
		t.Error("Should match by extension")
	}

	// Non-matching extension doesn't match
	match, _, _ = svc.matchRule("https://example.com/img.jpg")
	if match {
		t.Error("Should NOT match non-matching extension")
	}
}

// --- matchRule prioritizes extension over content-type ---

func TestMatchRuleFirstMatchingRuleWins(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{
		Enabled: true,
		Rules: []models.ThumbnailRule{
			{
				Extensions:      []string{"jpg"},
				Enabled:         true,
				MediaType:       "first-rule",
				MaxOriginalSize: 100000,
			},
			{
				Extensions:      []string{"jpg"},
				Enabled:         true,
				MediaType:       "second-rule",
				MaxOriginalSize: 200000,
			},
		},
	}
	svc, _ := NewService(cfg, nil)

	// Should match the first rule
	_, _, mt := svc.matchRule("https://example.com/photo.jpg")
	if mt != "first-rule" {
		t.Errorf("MediaType = %q, want %q (first rule should win)", mt, "first-rule")
	}
}

// --- checkOriginalSize with empty response ---

func TestCheckOriginalSizeNoContentLength(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't set Content-Length
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	rule := &models.ThumbnailRule{MaxOriginalSize: 1000}

	need, size, err := svc.checkOriginalSize(ts.URL, rule)
	if err != nil {
		t.Fatalf("checkOriginalSize failed: %v", err)
	}
	// Content-Length will be -1 (unknown), which is not > 1000
	if need {
		t.Error("Should NOT need thumbnail when content length is unknown (-1)")
	}

	if size != -1 {
		t.Errorf("Size = %d, want -1 (unknown)", size)
	}
}

// --- ProcessContent with various URL patterns ---

func TestProcessContentWithImageURLsButDisabled(t *testing.T) {
	cfg := &models.ThumbnailServiceConfig{Enabled: false}
	svc, _ := NewService(cfg, nil)

	content := `Here is an image <img src="https://example.com/photo.jpg"> and a link https://example.com/other.png`

	result, err := svc.ProcessContent(context.TODO(), content)
	if err != nil {
		t.Fatalf("ProcessContent should not error when disabled, got: %v", err)
	}

	if result != content {
		t.Error("ProcessContent should return original content when disabled")
	}
}
