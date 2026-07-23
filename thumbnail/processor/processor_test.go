package processor

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestNewDefaultProcessor(t *testing.T) {
	p := NewDefaultProcessor()
	if p == nil {
		t.Fatal("Processor should not be nil")
	}
}

func TestIsSupported(t *testing.T) {
	p := NewDefaultProcessor()

	tests := []struct {
		format   string
		expected bool
	}{
		{"jpeg", true}, //nolint:goconst
		{"jpg", true},  //nolint:goconst
		{"png", true},  //nolint:goconst
		{"gif", true},  //nolint:goconst
		{"webp", true}, //nolint:goconst
		{"bmp", true},
		{"tiff", true}, //nolint:goconst
		{"svg", false},
		{"", false},
	}

	for _, tt := range tests {
		result := p.IsSupported(tt.format)
		if result != tt.expected {
			t.Errorf("IsSupported(%s) = %v, want %v", tt.format, result, tt.expected)
		}
	}
}

func TestGetDimensions(t *testing.T) {
	p := NewDefaultProcessor()

	// Create a test image
	img := image.NewRGBA(image.Rect(0, 0, 100, 200))

	var buf bytes.Buffer

	err := jpeg.Encode(&buf, img, nil)
	if err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}

	width, height, format, err := p.GetDimensions(&buf)
	if err != nil {
		t.Fatalf("GetDimensions failed: %v", err)
	}

	if width != 100 {
		t.Errorf("Width = %d, want 100", width)
	}

	if height != 200 {
		t.Errorf("Height = %d, want 200", height)
	}

	if format != "jpeg" {
		t.Errorf("Format = %s, want jpeg", format)
	}
}

func TestGenerateThumbnail(t *testing.T) {
	p := NewDefaultProcessor()

	// Create a test image
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))

	var buf bytes.Buffer

	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	if err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}

	opts := ThumbnailOptions{
		MaxWidth:  800,
		MaxHeight: 600,
		Quality:   85,
		Format:    "jpeg",
	}

	reader, width, height, format, err := p.GenerateThumbnail(&buf, opts)
	if err != nil {
		t.Fatalf("GenerateThumbnail failed: %v", err)
	}

	if reader == nil {
		t.Fatal("Reader should not be nil")
	}

	if width > 800 {
		t.Errorf("Width %d exceeds max 800", width)
	}

	if height > 600 {
		t.Errorf("Height %d exceeds max 600", height)
	}

	if format != "jpeg" {
		t.Errorf("Format = %s, want jpeg", format)
	}

	// Read the output to verify it's valid
	output, err := bytes.NewBuffer(nil).ReadFrom(reader)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	if output == 0 {
		t.Error("Output should not be empty")
	}
}

func TestCalculateDimensions(t *testing.T) {
	tests := []struct {
		origWidth  int
		origHeight int
		maxWidth   int
		maxHeight  int
		wantWidth  int
		wantHeight int
	}{
		// No scaling needed
		{100, 100, 200, 200, 100, 100},
		// Scale down width
		{400, 200, 200, 200, 200, 100},
		// Scale down height
		{200, 400, 200, 200, 100, 200},
		// Scale down both
		{400, 400, 200, 200, 200, 200},
		// Large image
		{1920, 1080, 800, 600, 800, 450},
		// Very small max dimensions
		{1000, 1000, 1, 1, 1, 1},
	}

	for _, tt := range tests {
		gotWidth, gotHeight := calculateDimensions(tt.origWidth, tt.origHeight, tt.maxWidth, tt.maxHeight)
		if gotWidth != tt.wantWidth || gotHeight != tt.wantHeight {
			t.Errorf("calculateDimensions(%d, %d, %d, %d) = (%d, %d), want (%d, %d)",
				tt.origWidth, tt.origHeight, tt.maxWidth, tt.maxHeight,
				gotWidth, gotHeight, tt.wantWidth, tt.wantHeight)
		}
	}
}

func TestVideoProcessorIsSupported(t *testing.T) {
	vp := NewVideoProcessor("")

	tests := []struct {
		format   string
		expected bool
	}{
		{"mp4", true},  //nolint:goconst
		{"webm", true}, //nolint:goconst
		{"mov", true},  //nolint:goconst
		{"avi", true},  //nolint:goconst
		{"mkv", true},  //nolint:goconst
		{"flv", true},  //nolint:goconst
		{"wmv", true},  //nolint:goconst
		{"jpg", false},
		{"png", false},
		{"", false},
	}

	for _, tt := range tests {
		result := vp.IsSupported(tt.format)
		if result != tt.expected {
			t.Errorf("IsSupported(%s) = %v, want %v", tt.format, result, tt.expected)
		}
	}
}

func TestVideoProcessorSetSeekTime(t *testing.T) {
	vp := NewVideoProcessor("")
	vp.SetSeekTime(5000000000) // 5 seconds in nanoseconds
	// The seek time is set, we can't easily verify it but at least it shouldn't panic
}

func TestEncodeWebP(t *testing.T) {
	// Create a test image
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))

	var buf bytes.Buffer

	// Test with high quality (should use PNG)
	err := encodeWebP(&buf, img, 95)
	if err != nil {
		t.Fatalf("encodeWebP failed: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("Output should not be empty")
	}

	// Test with low quality (should use JPEG)
	buf.Reset()

	err = encodeWebP(&buf, img, 50)
	if err != nil {
		t.Fatalf("encodeWebP failed: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("Output should not be empty")
	}
}

func TestGenerateThumbnailPNG(t *testing.T) {
	p := NewDefaultProcessor()

	// Create a PNG test image
	img := image.NewRGBA(image.Rect(0, 0, 500, 500))

	var buf bytes.Buffer

	err := png.Encode(&buf, img)
	if err != nil {
		t.Fatalf("Failed to encode PNG: %v", err)
	}

	opts := ThumbnailOptions{
		MaxWidth:  200,
		MaxHeight: 200,
		Quality:   85,
		Format:    "png",
	}

	reader, width, height, format, err := p.GenerateThumbnail(&buf, opts)
	if err != nil {
		t.Fatalf("GenerateThumbnail failed: %v", err)
	}

	if reader == nil {
		t.Fatal("Reader should not be nil")
	}

	if width > 200 {
		t.Errorf("Width %d exceeds max 200", width)
	}

	if height > 200 {
		t.Errorf("Height %d exceeds max 200", height)
	}

	if format != "png" {
		t.Errorf("Format = %s, want png", format)
	}
}

func TestGenerateThumbnailNoScaling(t *testing.T) {
	p := NewDefaultProcessor()

	// Create a small test image (smaller than max dimensions)
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))

	var buf bytes.Buffer

	err := jpeg.Encode(&buf, img, nil)
	if err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}

	opts := ThumbnailOptions{
		MaxWidth:  200,
		MaxHeight: 200,
		Quality:   85,
		Format:    "jpeg",
	}

	reader, width, height, _, err := p.GenerateThumbnail(&buf, opts)
	if err != nil {
		t.Fatalf("GenerateThumbnail failed: %v", err)
	}

	if reader == nil {
		t.Fatal("Reader should not be nil")
	}

	// Should keep original dimensions
	if width != 50 || height != 50 {
		t.Errorf("Dimensions = (%d, %d), want (50, 50)", width, height)
	}
}
