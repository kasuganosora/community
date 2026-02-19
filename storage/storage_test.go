package storage

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockStorageProvider implements StorageProvider for testing
type MockStorageProvider struct{}

func (m *MockStorageProvider) Upload(filePath string, objectKey string) (string, error) {
	return objectKey, nil
}

func (m *MockStorageProvider) UploadReader(reader io.Reader, objectKey string, size int64) (string, error) {
	return objectKey, nil
}

func (m *MockStorageProvider) Download(objectKey string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *MockStorageProvider) Delete(objectKey string) error {
	return nil
}

func (m *MockStorageProvider) GetURL(objectKey string) string {
	return "http://example.com/" + objectKey
}

func (m *MockStorageProvider) IsEnabled() bool {
	return true
}

func TestStorageProviderInterface(t *testing.T) {
	// Create a mock implementation
	mockProvider := &MockStorageProvider{}

	var _ StorageProvider = mockProvider
	assert.NotNil(t, mockProvider)
}

func TestGetStorage(t *testing.T) {
	storage := GetStorage()
	assert.NotNil(t, storage)
}

func TestCloudStorage_IsEnabled(t *testing.T) {
	storage := GetStorage()
	// In test environment, cloud storage is typically disabled
	enabled := storage.IsEnabled()
	assert.False(t, enabled) // Should be false in test environment
}

func TestCloudStorage_GetURL(t *testing.T) {
	storage := GetStorage()
	url := storage.GetURL("test/file.txt")
	assert.NotEmpty(t, url)
}

func TestCloudStorage_Upload_Disabled(t *testing.T) {
	storage := GetStorage()
	// When cloud storage is disabled, it returns the original path
	result, err := storage.Upload("/local/path", "remote/key")
	assert.NoError(t, err)
	assert.Equal(t, "/local/path", result)
}

func TestS3Provider_IsEnabled(t *testing.T) {
	provider := &S3Provider{}
	// Without client, should be disabled
	assert.False(t, provider.IsEnabled())
}

func TestS3Provider_GetURL(t *testing.T) {
	tests := []struct {
		name        string
		provider    *S3Provider
		objectKey   string
		expectedURL string
	}{
		{
			name: "with public base URL",
			provider: &S3Provider{
				publicBaseURL: "https://cdn.example.com",
				bucket:        "test-bucket",
			},
			objectKey:   "file.txt",
			expectedURL: "https://cdn.example.com/test-bucket/file.txt",
		},
		{
			name: "without public base URL HTTP",
			provider: &S3Provider{
				endpoint: "localhost:9000",
				bucket:   "test-bucket",
				useSSL:   false,
			},
			objectKey:   "file.txt",
			expectedURL: "http://localhost:9000/test-bucket/file.txt",
		},
		{
			name: "without public base URL HTTPS",
			provider: &S3Provider{
				endpoint: "s3.amazonaws.com",
				bucket:   "test-bucket",
				useSSL:   true,
			},
			objectKey:   "file.txt",
			expectedURL: "https://s3.amazonaws.com/test-bucket/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.provider.GetURL(tt.objectKey)
			assert.Equal(t, tt.expectedURL, url)
		})
	}
}

func TestGetContentType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"image.jpg", "image/jpeg"},
		{"image.jpeg", "image/jpeg"},
		{"image.png", "image/png"},
		{"image.gif", "image/gif"},
		{"image.webp", "image/webp"},
		{"video.mp4", "video/mp4"},
		{"video.webm", "video/webm"},
		{"audio.mp3", "audio/mpeg"},
		{"audio.ogg", "audio/ogg"},
		{"audio.wav", "audio/wav"},
		{"file.txt", "application/octet-stream"},
		{"file.unknown", "application/octet-stream"},
		{"IMAGE.JPG", "image/jpeg"}, // case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := getContentType(tt.filename)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCloudStorageStruct(t *testing.T) {
	storage := &CloudStorage{
		enabled:  false,
		provider: nil,
	}

	assert.False(t, storage.IsEnabled())
}

func TestS3Provider_Struct(t *testing.T) {
	provider := &S3Provider{
		bucket:        "test-bucket",
		endpoint:      "localhost:9000",
		region:        "us-east-1",
		useSSL:        false,
		publicBaseURL: "https://cdn.example.com",
	}

	assert.Equal(t, "test-bucket", provider.bucket)
	assert.Equal(t, "localhost:9000", provider.endpoint)
	assert.Equal(t, "us-east-1", provider.region)
	assert.False(t, provider.useSSL)
	assert.Equal(t, "https://cdn.example.com", provider.publicBaseURL)
}

func TestDelete(t *testing.T) {
	// Test the Delete function signature
	assert.NotNil(t, Delete)
}

func TestSaveByMultipart(t *testing.T) {
	// Test the SaveByMultipart function signature
	assert.NotNil(t, SaveByMultipart)
}
