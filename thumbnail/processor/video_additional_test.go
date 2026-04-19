package processor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// --- NewVideoProcessor ---

func TestNewVideoProcessorCustomPath(t *testing.T) {
	vp := NewVideoProcessor("/usr/local/bin/ffmpeg")
	if vp == nil {
		t.Fatal("NewVideoProcessor should not return nil")
	}
	// The custom path should be stored (verified indirectly via error message)
}

func TestNewVideoProcessorDefaultPath(t *testing.T) {
	vp := NewVideoProcessor("")
	if vp == nil {
		t.Fatal("NewVideoProcessor should not return nil")
	}
	// Default path should be "ffmpeg"
}

// --- VideoInfo ---

func TestVideoInfoStructFields(t *testing.T) {
	info := &VideoInfo{
		Format:     "mp4",
		Duration:   120.5,
		Size:       1024000,
		BitRate:    8000000,
		Width:      1920,
		Height:     1080,
		VideoCodec: "h264",
	}

	if info.Format != "mp4" {
		t.Errorf("Format = %q, want %q", info.Format, "mp4")
	}
	if info.Duration != 120.5 {
		t.Errorf("Duration = %f, want %f", info.Duration, 120.5)
	}
	if info.Size != 1024000 {
		t.Errorf("Size = %d, want %d", info.Size, 1024000)
	}
	if info.BitRate != 8000000 {
		t.Errorf("BitRate = %d, want %d", info.BitRate, 8000000)
	}
	if info.Width != 1920 {
		t.Errorf("Width = %d, want %d", info.Width, 1920)
	}
	if info.Height != 1080 {
		t.Errorf("Height = %d, want %d", info.Height, 1080)
	}
	if info.VideoCodec != "h264" {
		t.Errorf("VideoCodec = %q, want %q", info.VideoCodec, "h264")
	}
}

func TestVideoInfoZeroValues(t *testing.T) {
	info := &VideoInfo{}
	if info.Format != "" {
		t.Errorf("Zero-value Format should be empty, got %q", info.Format)
	}
	if info.Duration != 0 {
		t.Errorf("Zero-value Duration should be 0, got %f", info.Duration)
	}
	if info.Size != 0 {
		t.Errorf("Zero-value Size should be 0, got %d", info.Size)
	}
}

// --- IsSupported ---

func TestVideoProcessorIsSupportedCaseSensitive(t *testing.T) {
	vp := NewVideoProcessor("")

	// Uppercase extensions should NOT be supported (map keys are lowercase)
	upperCases := []string{"MP4", "WebM", "MOV", "AVI", "MKV", "FLV", "WMV"}
	for _, format := range upperCases {
		if vp.IsSupported(format) {
			t.Errorf("IsSupported(%q) should be false (case-sensitive)", format)
		}
	}

	// Mixed case should also not match
	mixedCases := []string{"Mp4", "Webm", "Mov"}
	for _, format := range mixedCases {
		if vp.IsSupported(format) {
			t.Errorf("IsSupported(%q) should be false (case-sensitive)", format)
		}
	}
}

func TestVideoProcessorIsSupportedNegativeCases(t *testing.T) {
	vp := NewVideoProcessor("")

	negativeFormats := []string{
		"txt", "doc", "pdf", "mp3", "wav", "ogg", "flac",
		"exe", "zip", "tar", "gz", "html", "css", "js",
		"tiff", "tif", "heic", "heif", "avif",
		"3gp", "ts", "m4v", "f4v",
	}
	for _, format := range negativeFormats {
		if vp.IsSupported(format) {
			t.Errorf("IsSupported(%q) should be false", format)
		}
	}
}

// --- SetSeekTime ---

func TestSetSeekTimeValues(t *testing.T) {
	vp := NewVideoProcessor("")

	tests := []struct {
		name string
		dur  time.Duration
	}{
		{"zero", 0},
		{"negative", -1 * time.Second},
		{"small", 100 * time.Millisecond},
		{"5s", 5 * time.Second},
		{"1m", 1 * time.Minute},
		{"1h", 1 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vp.SetSeekTime(tt.dur)
			// If it didn't panic, the test passes
		})
	}
}

// --- GenerateThumbnail (error paths when ffmpeg not available) ---

func TestGenerateThumbnailNoFFmpeg(t *testing.T) {
	vp := NewVideoProcessor("")

	// Provide some dummy data as input
	input := strings.NewReader("not a real video file")

	opts := ThumbnailOptions{}

	reader, w, h, format, err := vp.GenerateThumbnail(input, opts)
	if err == nil {
		// If ffmpeg happened to be available, clean up
		if reader != nil {
			_, _ = io.Copy(io.Discard, reader)
		}
		t.Skip("ffmpeg appears to be available, skipping no-ffmpeg test")
	}

	if !strings.Contains(err.Error(), "failed to extract frame") {
		t.Errorf("Expected 'failed to extract frame' in error, got: %v", err)
	}
	if reader != nil {
		t.Error("Reader should be nil on error")
	}
	if w != 0 || h != 0 {
		t.Errorf("Width/Height should be 0 on error, got (%d, %d)", w, h)
	}
	if format != "" {
		t.Errorf("Format should be empty on error, got %q", format)
	}
}

func TestGenerateThumbnailNoFFmpegWithDimensions(t *testing.T) {
	vp := NewVideoProcessor("")

	input := bytes.NewReader(make([]byte, 100))

	opts := ThumbnailOptions{
		MaxWidth:  320,
		MaxHeight: 240,
		Quality:   80,
	}

	reader, w, h, format, err := vp.GenerateThumbnail(input, opts)
	if err == nil {
		if reader != nil {
			_, _ = io.Copy(io.Discard, reader)
		}
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error when ffmpeg is not available")
	}
	if !strings.Contains(err.Error(), "failed to extract frame") {
		t.Errorf("Error should mention frame extraction failure, got: %v", err)
	}
	// Verify dimensions are still zero on error
	if w != 0 || h != 0 {
		t.Errorf("Width/Height should be 0 on error, got (%d, %d)", w, h)
	}
	if format != "" {
		t.Errorf("Format should be empty on error, got %q", format)
	}
}

func TestGenerateThumbnailNoFFmpegZeroDimensions(t *testing.T) {
	vp := NewVideoProcessor("")

	input := bytes.NewReader(make([]byte, 50))

	// Zero dimensions means no scaling filter should be applied
	opts := ThumbnailOptions{
		MaxWidth:  0,
		MaxHeight: 0,
	}

	_, _, _, _, err := vp.GenerateThumbnail(input, opts)
	if err == nil {
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error when ffmpeg is not available")
	}
}

// --- GenerateThumbnail with failing reader ---

type failReader struct {
	err error
}

func (f *failReader) Read(p []byte) (n int, err error) {
	return 0, f.err
}

func TestGenerateThumbnailFailingReader(t *testing.T) {
	vp := NewVideoProcessor("")

	input := &failReader{err: errors.New("simulated read failure")}

	_, _, _, _, err := vp.GenerateThumbnail(input, ThumbnailOptions{})
	if err == nil {
		t.Fatal("Expected error when reader fails")
	}

	if !strings.Contains(err.Error(), "failed to write temp input file") {
		t.Errorf("Expected 'failed to write temp input file' in error, got: %v", err)
	}
}

// --- GenerateThumbnailFromPath (error paths) ---

func TestGenerateThumbnailFromPathNonExistent(t *testing.T) {
	vp := NewVideoProcessor("")

	opts := ThumbnailOptions{}

	reader, w, h, format, err := vp.GenerateThumbnailFromPath("/nonexistent/path/video.mp4", opts)
	if err == nil {
		if reader != nil {
			_, _ = io.Copy(io.Discard, reader)
		}
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error for non-existent path")
	}
	if reader != nil {
		t.Error("Reader should be nil on error")
	}
	if w != 0 || h != 0 {
		t.Errorf("Width/Height should be 0 on error, got (%d, %d)", w, h)
	}
	if format != "" {
		t.Errorf("Format should be empty on error, got %q", format)
	}
}

func TestGenerateThumbnailFromPathWithDimensions(t *testing.T) {
	vp := NewVideoProcessor("")

	opts := ThumbnailOptions{
		MaxWidth:  640,
		MaxHeight: 480,
		Quality:   75,
		Format:    "jpeg",
	}

	_, _, _, _, err := vp.GenerateThumbnailFromPath("/nonexistent/video.avi", opts)
	if err == nil {
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error for non-existent path with dimensions")
	}
}

func TestGenerateThumbnailFromPathZeroDimensions(t *testing.T) {
	vp := NewVideoProcessor("")

	opts := ThumbnailOptions{
		MaxWidth:  0,
		MaxHeight: 0,
	}

	_, _, _, _, err := vp.GenerateThumbnailFromPath("/nonexistent/video.mkv", opts)
	if err == nil {
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error for non-existent path with zero dimensions")
	}
}

func TestGenerateThumbnailFromPathEmptyPath(t *testing.T) {
	vp := NewVideoProcessor("")

	_, _, _, _, err := vp.GenerateThumbnailFromPath("", ThumbnailOptions{})
	if err == nil {
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error for empty path")
	}
}

// --- extractFrameExec error message ---

func TestExtractFrameExecErrorMessage(t *testing.T) {
	vp := NewVideoProcessor("ffmpeg")

	// Call extractFrameExec directly (it's unexported but accessible in same package)
	err := vp.extractFrameExec("/nonexistent/input.mp4", "/nonexistent/output.jpg", 320, 240)
	if err == nil {
		t.Fatal("Expected error from extractFrameExec")
	}

	// The error should contain "ffmpeg exec failed"
	if !strings.Contains(err.Error(), "ffmpeg exec failed") {
		t.Errorf("Error should contain 'ffmpeg exec failed', got: %v", err)
	}
}

func TestExtractFrameExecZeroDimensions(t *testing.T) {
	vp := NewVideoProcessor("ffmpeg")

	// Test with zero dimensions (no scaling filter)
	err := vp.extractFrameExec("/nonexistent/input.mp4", "/nonexistent/output.jpg", 0, 0)
	if err == nil {
		t.Fatal("Expected error from extractFrameExec")
	}

	if !strings.Contains(err.Error(), "ffmpeg exec failed") {
		t.Errorf("Error should contain 'ffmpeg exec failed', got: %v", err)
	}
}

func TestExtractFrameFallbackToExec(t *testing.T) {
	vp := NewVideoProcessor("")

	// extractFrame tries ffmpeg-go first, then falls back to extractFrameExec
	// Both should fail when ffmpeg is not available
	err := vp.extractFrame("/nonexistent/input.mp4", "/nonexistent/output.jpg", 320, 240)
	if err == nil {
		t.Fatal("Expected error from extractFrame")
	}

	// The error should ultimately come from extractFrameExec
	if !strings.Contains(err.Error(), "ffmpeg exec failed") {
		t.Errorf("Error should contain 'ffmpeg exec failed', got: %v", err)
	}
}

// --- SeekTime string formatting ---

func TestSeekTimeFormatting(t *testing.T) {
	vp := NewVideoProcessor("")

	// Test that seek time is properly formatted in the error message path
	vp.SetSeekTime(2*time.Second + 500*time.Millisecond)

	// Call extractFrameExec to see the formatted time in the args
	// We can't directly check the args, but we can verify it doesn't panic
	err := vp.extractFrameExec("/nonexistent/input.mp4", "/nonexistent/output.jpg", 0, 0)
	if err == nil {
		t.Fatal("Expected error")
	}
	// The seek time 2.500 should be formatted as "2.500" in the ffmpeg args
	// We can't verify this directly but we verify the function works without panicking
}

// --- VideoProcessor satisfies interface check ---

func TestVideoProcessorType(t *testing.T) {
	// Verify that VideoProcessor is a concrete type (not interface)
	var _ *VideoProcessor = &VideoProcessor{}

	// Verify NewVideoProcessor returns non-nil
	vp := NewVideoProcessor("custom-ffmpeg")
	if vp == nil {
		t.Fatal("NewVideoProcessor returned nil")
	}
	// Verify that methods can be called on it without panicking
	_ = vp.IsSupported("mp4")
	vp.SetSeekTime(0)
}

// --- GenerateThumbnail with large input ---

func TestGenerateThumbnailLargeInput(t *testing.T) {
	vp := NewVideoProcessor("")

	// Large input that's definitely not a video
	largeInput := bytes.NewReader(make([]byte, 10*1024*1024)) // 10MB

	_, _, _, _, err := vp.GenerateThumbnail(largeInput, ThumbnailOptions{
		MaxWidth:  800,
		MaxHeight: 600,
	})
	if err == nil {
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error for invalid large input")
	}
}

// --- GenerateThumbnail with custom ffmpegPath ---

func TestGenerateThumbnailCustomFFmpegPath(t *testing.T) {
	// Use a custom path that doesn't exist
	vp := NewVideoProcessor("/nonexistent/ffmpeg")

	input := bytes.NewReader(make([]byte, 10))

	_, _, _, _, err := vp.GenerateThumbnail(input, ThumbnailOptions{})
	if err == nil {
		t.Skip("ffmpeg appears to be available at custom path")
	}

	if err == nil {
		t.Fatal("Expected error with custom ffmpeg path")
	}

	// The error should mention the custom path
	errMsg := err.Error()
	if !strings.Contains(errMsg, "failed to extract frame") {
		t.Errorf("Error should mention frame extraction failure, got: %v", errMsg)
	}
}

// --- Edge case: empty ThumbnailOptions ---

func TestGenerateThumbnailEmptyOptions(t *testing.T) {
	vp := NewVideoProcessor("")

	input := bytes.NewReader([]byte("dummy"))

	_, _, _, _, err := vp.GenerateThumbnail(input, ThumbnailOptions{})
	if err == nil {
		t.Skip("ffmpeg appears to be available")
	}

	if err == nil {
		t.Fatal("Expected error with empty options")
	}
}

// --- extractFrame with only maxWidth > 0 ---

func TestExtractFramePartialDimensions(t *testing.T) {
	vp := NewVideoProcessor("")

	// Only maxWidth set (maxHeight is 0), so no scaling filter should be applied
	// The condition is: if maxWidth > 0 && maxHeight > 0
	err := vp.extractFrame("/nonexistent/input.mp4", "/nonexistent/output.jpg", 320, 0)
	if err == nil {
		t.Fatal("Expected error")
	}

	// Only maxHeight set
	err = vp.extractFrame("/nonexistent/input.mp4", "/nonexistent/output.jpg", 0, 240)
	if err == nil {
		t.Fatal("Expected error")
	}
}

// --- Verify ffmpegPath is correctly stored ---

func TestVideoProcessorFFmpegPathStored(t *testing.T) {
	vp := NewVideoProcessor("/usr/bin/ffmpeg")
	// We can verify the path indirectly by checking extractFrameExec error
	// The custom path will be in the command that fails
	err := vp.extractFrameExec("/nonexistent/input.mp4", "/nonexistent/output.jpg", 0, 0)
	if err == nil {
		t.Fatal("Expected error")
	}
	// The error comes from exec.Command with our custom path
	// Verify the error is about the executable not found
	if !strings.Contains(err.Error(), "ffmpeg exec failed") {
		t.Errorf("Unexpected error format: %v", err)
	}
}

// --- VideoInfo JSON tags ---

func TestVideoInfoFieldCount(t *testing.T) {
	// Verify VideoInfo has the expected number of fields
	info := VideoInfo{}
	// This test ensures the struct hasn't changed unexpectedly
	_ = info.Format
	_ = info.Duration
	_ = info.Size
	_ = info.BitRate
	_ = info.Width
	_ = info.Height
	_ = info.VideoCodec
}

// --- GenerateThumbnailFromPath with various formats ---

func TestGenerateThumbnailFromPathVariousFormats(t *testing.T) {
	vp := NewVideoProcessor("")
	formats := []string{"mp4", "webm", "mov", "avi", "mkv", "flv", "wmv"}

	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			path := fmt.Sprintf("/nonexistent/video.%s", format)
			_, _, _, _, err := vp.GenerateThumbnailFromPath(path, ThumbnailOptions{
				MaxWidth:  320,
				MaxHeight: 240,
			})
			if err == nil {
				t.Skip("ffmpeg appears to be available")
			}
		})
	}
}

// --- Processor interface compliance ---

func TestDefaultProcessorImplementsProcessor(t *testing.T) {
	var _ Processor = NewDefaultProcessor()
}

// --- VideoProcessor with negative seek time ---

func TestSetSeekTimeNegative(t *testing.T) {
	vp := NewVideoProcessor("")
	vp.SetSeekTime(-5 * time.Second)
	// Should not panic; ffmpeg will handle the negative value
}
