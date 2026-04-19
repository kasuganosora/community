package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// S3Backend tests
// ---------------------------------------------------------------------------

func TestS3Backend_Type(t *testing.T) {
	s := &S3Backend{}
	assert.Equal(t, "s3", s.Type())
}

func TestS3Backend_GetURL_WithPublicURL(t *testing.T) {
	s := &S3Backend{
		bucket:     "my-bucket",
		pathPrefix: "thumbs/",
		publicURL:  "https://cdn.example.com",
		region:     "us-east-1",
	}

	url := s.GetURL("img/photo.png")
	assert.Equal(t, "https://cdn.example.com/thumbs/img/photo.png", url)
}

func TestS3Backend_GetURL_WithoutPublicURL(t *testing.T) {
	s := &S3Backend{
		bucket:     "my-bucket",
		pathPrefix: "pre/",
		publicURL:  "",
		region:     "eu-west-1",
	}

	url := s.GetURL("a/b.png")
	assert.Equal(t, "https://my-bucket.s3.eu-west-1.amazonaws.com/pre/a/b.png", url)
}

func TestS3Backend_GetURL_EmptyPrefix(t *testing.T) {
	s := &S3Backend{
		bucket:     "assets",
		pathPrefix: "",
		publicURL:  "",
		region:     "ap-southeast-1",
	}

	url := s.GetURL("hello.jpg")
	assert.Equal(t, "https://assets.s3.ap-southeast-1.amazonaws.com/hello.jpg", url)
}

func TestS3Backend_GetURL_WithPublicURLAndEmptyPrefix(t *testing.T) {
	s := &S3Backend{
		bucket:     "b",
		pathPrefix: "",
		publicURL:  "https://static.example.org",
		region:     "us-east-1",
	}

	url := s.GetURL("file.txt")
	assert.Equal(t, "https://static.example.org/file.txt", url)
}

// ---------------------------------------------------------------------------
// MinIOBackend tests
// ---------------------------------------------------------------------------

func TestMinIOBackend_Type(t *testing.T) {
	m := &MinIOBackend{}
	assert.Equal(t, "minio", m.Type())
}

func TestMinIOBackend_GetURL_WithPublicURL(t *testing.T) {
	m := &MinIOBackend{
		bucket:     "my-bucket",
		pathPrefix: "thumbs/",
		publicURL:  "https://minio-cdn.example.com",
		endpoint:   "http://localhost:9000",
	}

	url := m.GetURL("img/photo.png")
	assert.Equal(t, "https://minio-cdn.example.com/thumbs/img/photo.png", url)
}

func TestMinIOBackend_GetURL_WithoutPublicURL(t *testing.T) {
	m := &MinIOBackend{
		bucket:     "my-bucket",
		pathPrefix: "pre/",
		publicURL:  "",
		endpoint:   "http://localhost:9000",
	}

	url := m.GetURL("a/b.png")
	assert.Equal(t, "http://localhost:9000/my-bucket/pre/a/b.png", url)
}

func TestMinIOBackend_GetURL_EmptyPrefix(t *testing.T) {
	m := &MinIOBackend{
		bucket:     "assets",
		pathPrefix: "",
		publicURL:  "",
		endpoint:   "http://192.168.1.10:9000",
	}

	url := m.GetURL("hello.jpg")
	assert.Equal(t, "http://192.168.1.10:9000/assets/hello.jpg", url)
}

func TestMinIOBackend_GetURL_WithPublicURLAndEmptyPrefix(t *testing.T) {
	m := &MinIOBackend{
		bucket:     "b",
		pathPrefix: "",
		publicURL:  "https://static.minio.org",
		endpoint:   "http://localhost:9000",
	}

	url := m.GetURL("file.txt")
	assert.Equal(t, "https://static.minio.org/file.txt", url)
}

// ---------------------------------------------------------------------------
// NewBackend factory tests
// ---------------------------------------------------------------------------

func TestNewBackend_UnsupportedType(t *testing.T) {
	_, err := NewBackend(Config{Type: "ftp"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported storage type: ftp")
}

func TestNewBackend_UnsupportedType_Empty(t *testing.T) {
	_, err := NewBackend(Config{Type: ""})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported storage type:")
}

// ---------------------------------------------------------------------------
// StorageType constants
// ---------------------------------------------------------------------------

func TestStorageTypeConstants(t *testing.T) {
	assert.Equal(t, "local", StorageTypeLocal)
	assert.Equal(t, "s3", StorageTypeS3)
	assert.Equal(t, "minio", StorageTypeMinIO)
}

// ---------------------------------------------------------------------------
// S3Backend GetURL — verify URL contains expected components
// ---------------------------------------------------------------------------

func TestS3Backend_GetURL_ContainsBucketAndRegion(t *testing.T) {
	s := &S3Backend{
		bucket:     "test-bucket",
		pathPrefix: "",
		publicURL:  "",
		region:     "sa-east-1",
	}

	url := s.GetURL("object.key")
	assert.True(t, strings.Contains(url, "test-bucket"), "URL should contain bucket name")
	assert.True(t, strings.Contains(url, "sa-east-1"), "URL should contain region")
	assert.True(t, strings.Contains(url, "s3"), "URL should contain s3")
	assert.True(t, strings.Contains(url, "amazonaws.com"), "URL should contain amazonaws.com")
}

func TestMinIOBackend_GetURL_ContainsEndpointAndBucket(t *testing.T) {
	m := &MinIOBackend{
		bucket:     "data",
		pathPrefix: "",
		publicURL:  "",
		endpoint:   "http://minio.example.com:9000",
	}

	url := m.GetURL("obj")
	assert.True(t, strings.Contains(url, "minio.example.com:9000"), "URL should contain endpoint")
	assert.True(t, strings.Contains(url, "data"), "URL should contain bucket name")
}
