package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kasuganosora/community/config"
)

// StorageProvider is the legacy provider interface, kept for callers that
// predate Backend. CloudStorage implements it on top of Default().
type StorageProvider interface {
	Upload(filePath string, objectKey string) (string, error)
	UploadReader(reader io.Reader, objectKey string, size int64) (string, error)
	Download(objectKey string) (io.ReadCloser, error)
	Delete(objectKey string) error
	GetURL(objectKey string) string
	IsEnabled() bool
}

// uploadsRoot returns the cleaned root for uploads and its separator.
func uploadsRoot() (cleanRoot, sep string) {
	return filepath.Clean("uploads"), string(filepath.Separator)
}

// isPathInsideRoot 验证 path 是否仍在 root 目录内（防止路径遍历）.
// path 必须是经过 filepath.Join + filepath.Clean 处理后的路径.
func isPathInsideRoot(path, root, sep string) bool {
	if path == root {
		return true
	}

	return strings.HasPrefix(path, root+sep)
}

const opTimeout = 5 * time.Minute

// Save stores data under key in the default backend and returns "/"+key
// (the historical return format).
func Save(key string, data []byte) (string, error) {
	k, err := CleanKey(key)
	if err != nil {
		return "", fmt.Errorf("%w: potential path traversal detected", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	if err := Default().Put(ctx, k, bytes.NewReader(data), int64(len(data)), sniffContentType(k, data)); err != nil {
		return "", err
	}

	return "/" + k, nil
}

// SaveByMultipart stores an uploaded multipart file under key in the default
// backend and returns the key.
func SaveByMultipart(key string, fileHeader *multipart.FileHeader) (string, error) {
	k, err := CleanKey(key)
	if err != nil {
		return "", fmt.Errorf("%w: potential path traversal detected", err)
	}

	file, err := fileHeader.Open()
	if err != nil {
		return "", err
	}

	defer func() { _ = file.Close() }()

	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	r := io.MultiReader(bytes.NewReader(head), file)
	if err := Default().Put(ctx, k, r, fileHeader.Size, sniffContentType(k, head)); err != nil {
		return "", err
	}

	return k, nil
}

// Delete removes key from the default backend.
func Delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return Default().Delete(ctx, key)
}

// Open reads key from the default backend.
func Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	return Default().Open(ctx, key)
}

// PutFile uploads the file at localPath to key in the default backend.
func PutFile(ctx context.Context, localPath, key, contentType string) error {
	f, err := os.Open(localPath) //nolint:gosec // G304: caller-controlled temp/upload file
	if err != nil {
		return err
	}

	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return err
	}

	if contentType == "" {
		contentType = getContentType(localPath)
	}

	return Default().Put(ctx, key, f, st.Size(), contentType)
}

func sniffContentType(key string, head []byte) string {
	if ct := getContentType(key); ct != "application/octet-stream" {
		return ct
	}

	if len(head) > 0 {
		return http.DetectContentType(head)
	}

	return "application/octet-stream"
}

// CloudStorage adapts Default() to the legacy StorageProvider API.
type CloudStorage struct{}

// GetStorage returns the legacy adapter.
func GetStorage() *CloudStorage { return &CloudStorage{} }

func isCloudStorageEnabled() bool {
	storageType := getStorageType()

	return storageType == "s3" || storageType == "minio"
}

func getStorageType() string {
	// STORAGE_TYPE is a standard Config field, so it lives in
	// config.C.StorageType rather than config.C.Other.
	storageType := config.C.StorageType
	if storageType == "" {
		return "local"
	}

	return strings.ToLower(storageType)
}

// IsEnabled reports whether object storage (S3) is the active backend.
func (s *CloudStorage) IsEnabled() bool { return IsRemote() }

// Upload uploads a local file to objectKey and returns the key.
func (s *CloudStorage) Upload(filePath string, objectKey string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	if err := PutFile(ctx, filePath, objectKey, ""); err != nil {
		return "", err
	}

	return objectKey, nil
}

// UploadReader uploads reader to objectKey and returns the key.
func (s *CloudStorage) UploadReader(reader io.Reader, objectKey string, size int64) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	if err := Default().Put(ctx, objectKey, reader, size, getContentType(objectKey)); err != nil {
		return "", err
	}

	return objectKey, nil
}

// Download opens objectKey (legacy references are normalised first).
func (s *CloudStorage) Download(objectKey string) (io.ReadCloser, error) {
	key, _ := NormalizeKey(objectKey)
	rc, _, err := Default().Open(context.Background(), key)

	return rc, err
}

// Delete removes objectKey (legacy references are normalised first).
func (s *CloudStorage) Delete(objectKey string) error {
	key, _ := NormalizeKey(objectKey)

	return Delete(key)
}

// GetURL returns a URL for objectKey: the public object URL when one is
// configured, otherwise APP_BASE_URL/<key>.
func (s *CloudStorage) GetURL(objectKey string) string {
	if u := Default().PublicURL(objectKey); u != "" {
		return u
	}

	return config.C.AppBaseURL + "/" + strings.TrimPrefix(objectKey, "/")
}

// GeneratePresignedURL returns a temporary URL (S3 only).
func (s *CloudStorage) GeneratePresignedURL(objectKey string, expiry time.Duration) (string, error) {
	b, ok := Default().(*S3Backend)
	if !ok {
		return "", errors.New("storage not enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return b.PresignedURL(ctx, objectKey, expiry)
}

// HealthCheck reports whether the default backend passes its probe.
func (s *CloudStorage) HealthCheck() bool {
	return CheckHealth(context.Background()).Healthy
}

// getContentType maps a file extension to a MIME type.
func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg" //nolint:goconst
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	default:
		return "application/octet-stream" //nolint:goconst
	}
}
