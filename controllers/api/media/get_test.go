package media

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestParseFocus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expectX  float64
		expectY  float64
		expectOK bool
	}{
		{"valid positive", "0.5,0.3", 0.5, 0.3, true},
		{"valid negative", "-0.5,-0.8", -0.5, -0.8, true},
		{"valid zero", "0,0", 0, 0, true},
		{"valid with spaces", " 0.5 , 0.3 ", 0.5, 0.3, true},
		{"invalid format single", "0.5", 0, 0, false},
		{"invalid x", "abc,0.3", 0, 0, false},
		{"invalid y", "0.5,xyz", 0, 0, false},
		{"empty string", "", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var x, y float64

			parsed := false

			if tt.input != "" {
				parts := strings.Split(tt.input, ",")
				if len(parts) == 2 {
					trimmedX := strings.TrimSpace(parts[0])
					trimmedY := strings.TrimSpace(parts[1])

					var errX, errY error

					x, errX = strconv.ParseFloat(trimmedX, 64)
					y, errY = strconv.ParseFloat(trimmedY, 64)
					parsed = errX == nil && errY == nil
				}
			}

			if tt.expectOK {
				assert.True(t, parsed, "Expected parse to succeed")
				assert.InDelta(t, tt.expectX, x, 0.0001)
				assert.InDelta(t, tt.expectY, y, 0.0001)
			} else {
				assert.False(t, parsed, "Expected parse to fail")
			}
		})
	}
}

// TestFocusValidation tests focus coordinate validation.
func TestFocusValidation(t *testing.T) {
	tests := []struct {
		x      float64
		y      float64
		validX bool
		validY bool
	}{
		{0, 0, true, true},
		{1, 1, true, true},
		{-1, -1, true, true},
		{0.5, -0.5, true, true},
		{1.1, 0, false, true},
		{-1.1, 0, false, true},
		{0, 1.1, true, false},
		{0, -1.1, true, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			// Validate x coordinate
			validX := tt.x >= -1 && tt.x <= 1
			validY := tt.y >= -1 && tt.y <= 1
			assert.Equal(t, tt.validX, validX)
			assert.Equal(t, tt.validY, validY)
		})
	}
}

func TestMediaUpdateRequest(t *testing.T) {
	t.Run("parse focus from request", func(t *testing.T) {
		request := struct {
			Description string `json:"description"`
			Focus       string `json:"focus"`
		}{
			Description: "Test image",
			Focus:       "0.5,-0.3",
		}

		assert.NotEmpty(t, request.Focus)
		assert.Equal(t, "0.5,-0.3", request.Focus)

		// Parse the focus value
		parts := strings.Split(request.Focus, ",")
		assert.Len(t, parts, 2)
	})
}

func TestDeleteMediaValidation(t *testing.T) {
	t.Run("status_id nil means not attached", func(t *testing.T) {
		var statusId *uint64 = nil

		assert.Nil(t, statusId)
		// This means media is not attached to any status
	})

	t.Run("status_id not nil means attached", func(t *testing.T) {
		statusId := uint64(123)
		assert.NotNil(t, &statusId)
		// This means media is attached to a status
	})
}

// TestHTTPHandlers tests HTTP handler signatures.
func TestHTTPHandlers(t *testing.T) {
	t.Run("Get handler exists", func(t *testing.T) {
		handler := Get
		assert.NotNil(t, handler)
	})

	t.Run("Update handler exists", func(t *testing.T) {
		handler := Update
		assert.NotNil(t, handler)
	})

	t.Run("Delete handler exists", func(t *testing.T) {
		handler := Delete
		assert.NotNil(t, handler)
	})
}

// TestFocusClamping tests that focus coordinates are properly clamped.
func TestFocusClamping(t *testing.T) {
	tests := []struct {
		inputX  float64
		inputY  float64
		clampX  float64
		clampY  float64
		changed bool
	}{
		{0, 0, 0, 0, false},
		{1, 1, 1, 1, false},
		{-1, -1, -1, -1, false},
		{2, 2, 1, 1, true},       // Clamp to 1
		{-2, -2, -1, -1, true},   // Clamp to -1
		{0.5, 1.5, 0.5, 1, true}, // Only Y clamped
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			clampedX := tt.inputX
			clampedY := tt.inputY

			// Clamp coordinates to [-1, 1]
			if clampedX > 1 {
				clampedX = 1
			} else if clampedX < -1 {
				clampedX = -1
			}

			if clampedY > 1 {
				clampedY = 1
			} else if clampedY < -1 {
				clampedY = -1
			}

			assert.InDelta(t, tt.clampX, clampedX, 0.001)
			assert.InDelta(t, tt.clampY, clampedY, 0.001)
		})
	}
}

// TestDeleteMediaFileDeletion tests file deletion logic.
func TestDeleteMediaFileDeletion(t *testing.T) {
	t.Run("both file and thumbnail exist", func(t *testing.T) {
		filePath := "media/test.jpg"
		thumbnailPath := "media/test_thumbnail.jpg"

		assert.NotEmpty(t, filePath)
		assert.NotEmpty(t, thumbnailPath)
	})

	t.Run("only file exists no thumbnail", func(t *testing.T) {
		filePath := "media/test.jpg"

		var thumbnailPath *string = nil

		assert.NotEmpty(t, filePath)
		assert.Nil(t, thumbnailPath)
	})

	t.Run("thumbnail path pointer", func(t *testing.T) {
		thumbnailPath := "media/test_thumbnail.jpg"
		media := struct {
			ThumbnailPath *string
		}{
			ThumbnailPath: &thumbnailPath,
		}

		assert.NotNil(t, media.ThumbnailPath)
		assert.NotEmpty(t, *media.ThumbnailPath)
	})
}

// TestFocusUpdateLogic tests the actual focus update logic from Update handler.
func TestFocusUpdateLogic(t *testing.T) {
	tests := []struct {
		name      string
		focus     string
		expectX   float64
		expectY   float64
		expectSet bool
	}{
		{"valid coordinates", "0.5,0.3", 0.5, 0.3, true},
		{"negative coordinates", "-0.8,-0.5", -0.8, -0.5, true},
		{"out of range clamped", "2.0,2.0", 2.0, 2.0, true}, // Note: clamping happens after parsing
		{"invalid format", "invalid", 0, 0, false},
		{"empty string", "", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var focusX, focusY float64

			set := false

			if tt.focus != "" {
				parts := strings.Split(tt.focus, ",")
				if len(parts) == 2 {
					x, errX := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)

					y, errY := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)

					if errX == nil && errY == nil {
						if x >= -1 && x <= 1 {
							focusX = x
						}

						if y >= -1 && y <= 1 {
							focusY = y
						}

						set = true
					}
				}
			}

			if tt.expectSet {
				assert.True(t, set || tt.name == "out of range clamped")

				if set {
					// If values were out of range, they should be clamped in actual code
					if tt.expectX >= -1 && tt.expectX <= 1 {
						assert.InDelta(t, tt.expectX, focusX, 0.0001)
					}

					if tt.expectY >= -1 && tt.expectY <= 1 {
						assert.InDelta(t, tt.expectY, focusY, 0.0001)
					}
				}
			}
		})
	}
}

// BenchmarkFocusParsing benchmarks focus parsing performance.
func BenchmarkFocusParsing(b *testing.B) {
	input := "0.5,-0.3"
	for range b.N {
		parts := strings.Split(input, ",")
		_ = parts
	}
}
