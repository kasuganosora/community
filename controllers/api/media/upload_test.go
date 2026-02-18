package media

import (
	"bytes"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// createTestMultipartRequest 创建测试用的 multipart 请求
func createTestMultipartRequest(t *testing.T, fieldName, fileName string, fileContent []byte) *http.Request {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile(fieldName, fileName)
	assert.NoError(t, err)

	_, err = part.Write(fileContent)
	assert.NoError(t, err)

	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req
}

// createTestJPEGContent 创建测试用的 JPEG 内容
func createTestJPEGContent(t *testing.T, width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, nil)
	assert.NoError(t, err)
	return buf.Bytes()
}

func TestIsAllowedContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		expected    bool
	}{
		{"JPEG image", "image/jpeg", true},
		{"PNG image", "image/png", true},
		{"GIF image", "image/gif", true},
		{"WebP image", "image/webp", true},
		{"MP4 video", "video/mp4", true},
		{"WebM video", "video/webm", true},
		{"MPEG audio", "audio/mpeg", true},
		{"OGG audio", "audio/ogg", true},
		{"WAV audio", "audio/wav", true},
		{"PDF document", "application/pdf", false},
		{"Text file", "text/plain", false},
		{"Empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAllowedContentType(tt.contentType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidFileSize_Image(t *testing.T) {
	tests := []struct {
		name        string
		size        int64
		contentType string
		expected    bool
	}{
		{"Small image", 100 * 1024, "image/jpeg", true},
		{"Medium image", 5 * 1024 * 1024, "image/png", true},
		{"Max image size", MaxImageSize, "image/jpeg", true},
		{"Over max image", MaxImageSize + 1, "image/jpeg", false},
		{"Small video", 10 * 1024 * 1024, "video/mp4", true},
		{"Max video size", MaxVideoSize, "video/mp4", true},
		{"Over max video", MaxVideoSize + 1, "video/webm", false},
		{"Small audio", 5 * 1024 * 1024, "audio/mpeg", true},
		{"Max audio size", MaxVideoSize, "audio/ogg", true},
		{"Over max audio", MaxVideoSize + 1, "audio/wav", false},
		{"Unknown type", 100, "application/pdf", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidFileSize(tt.size, tt.contentType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUpload_NoFile(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// 创建空的 multipart 请求
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	// 需要模拟 authenticated 和 account
	c.Set("authenticated", true)
	c.Set("account", &mockAccount{})

	Upload(c)

	// 应该返回错误，因为没有文件
	assert.NotEqual(t, http.StatusOK, w.Code)
}

func TestUpload_UnsupportedMediaType(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// 创建一个 PDF 文件内容
	pdfContent := []byte("%PDF-1.4 fake pdf content")

	req := createTestMultipartRequest(t, "file", "test.pdf", pdfContent)
	c.Request = req
	c.Set("authenticated", true)
	c.Set("account", &mockAccount{})

	Upload(c)

	// 应该返回 415 Unsupported Media Type
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

func TestUploadAsync_NoFile(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/v2", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	c.Set("authenticated", true)
	c.Set("account", &mockAccount{})

	UploadAsync(c)

	assert.NotEqual(t, http.StatusOK, w.Code)
}

// mockAccount 模拟 Account 对象
type mockAccount struct{}

func (m *mockAccount) GetId() uint64 { return 1 }
