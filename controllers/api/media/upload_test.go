package media

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// createTestMultipartRequest 创建测试用的 multipart 请求.
func createTestMultipartRequest(t *testing.T, fieldName, fileName string, fileContent []byte) *http.Request {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile(fieldName, fileName)
	require.NoError(t, err)

	_, err = part.Write(fileContent)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body) //nolint:noctx
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req
}

// createTestAccount creates a test account for testing.
func createTestAccount() *models.Account {
	return &models.Account{
		Id:       1,
		Username: "testuser",
		Acct:     "testuser@test.com",
	}
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
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body) //nolint:noctx
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	// 需要模拟 authenticated 和 account
	c.Set("authenticated", true)
	c.Set("account", createTestAccount())

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
	c.Set("account", createTestAccount())

	Upload(c)

	// 应该返回 400 Bad Request（magic bytes 验证不通过）
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUploadAsync_NoFile(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/v2", body) //nolint:noctx
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	c.Set("authenticated", true)
	c.Set("account", createTestAccount())

	UploadAsync(c)

	assert.NotEqual(t, http.StatusOK, w.Code)
}
