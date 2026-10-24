package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudStorageImplementsStorageProvider(t *testing.T) {
	var _ StorageProvider = &CloudStorage{}
}

func TestDefaultIsLocalInTests(t *testing.T) {
	assert.Equal(t, KindLocal, Default().Kind())
	assert.False(t, GetStorage().IsEnabled())
}

func TestCloudStorage_GetURL_Local(t *testing.T) {
	assert.NotEmpty(t, GetStorage().GetURL("test/file.txt"))
}

func TestCloudKeyRoundTripLocal(t *testing.T) {
	root := t.TempDir()
	defer SetDefault(&LocalBackend{Root: root})()

	src := filepath.Join(t.TempDir(), "a.png")
	require.NoError(t, os.WriteFile(src, []byte("png-bytes"), 0o600))

	key, err := GetStorage().Upload(src, "media/2026/01/01/a.png")
	require.NoError(t, err)
	assert.Equal(t, "media/2026/01/01/a.png", key)
	assert.FileExists(t, filepath.Join(root, "media/2026/01/01/a.png"))

	rc, info, err := Open(context.Background(), key)
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "png-bytes", string(data))
	assert.Equal(t, int64(9), info.Size)

	require.NoError(t, Delete(key))
	_, _, err = Open(context.Background(), key)
	require.ErrorIs(t, err, ErrNotFound)
	// Deleting twice is fine.
	require.NoError(t, Delete(key))
}

func TestCleanKey(t *testing.T) {
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "a\\b", "..", "a/..", "x\x00"} {
		_, err := CleanKey(bad)
		assert.Error(t, err, bad)
	}

	k, err := CleanKey("media//2026/./a.jpg")
	require.NoError(t, err)
	assert.Equal(t, "media/2026/a.jpg", k)
}

func TestNormalizeKey(t *testing.T) {
	cases := map[string]string{
		"media/2026/a.jpg":                                  "media/2026/a.jpg",
		"uploads/matrix/2026/a.jpg":                         "matrix/2026/a.jpg",
		"/uploads/media/a.jpg":                              "media/a.jpg",
		"https://acct.r2.cloudflarestorage.com/media/a.jpg": "media/a.jpg",
	}
	for in, want := range cases {
		got, _ := NormalizeKey(in)
		assert.Equal(t, want, got, in)
	}

	got, _ := NormalizeKey("https://acct.r2.cloudflarestorage.com/some-bucket/media/2026/a.jpg")
	assert.Equal(t, "media/2026/a.jpg", got, "unknown bucket segment is dropped")

	_, isURL := NormalizeKey("https://x/y")
	assert.True(t, isURL)
}

func TestNormalizeKey_BucketURL(t *testing.T) {
	defer SetDefault(NewS3Backend(S3Config{Endpoint: "acct.r2.cloudflarestorage.com", Bucket: "community-file", AccessKey: "AKIAEXAMPLE", SecretKey: "secretexample", UseSSL: true, PublicURL: "https://media.example.com"}))()

	got, isURL := NormalizeKey("https://acct.r2.cloudflarestorage.com/community-file/media/2026/10/09/x.jpg")
	assert.True(t, isURL)
	assert.Equal(t, "media/2026/10/09/x.jpg", got)

	got, _ = NormalizeKey("https://media.example.com/community-file/media/a%20b.jpg")
	assert.Equal(t, "media/a b.jpg", got, "legacy <public>/<bucket>/<key> form")

	got, _ = NormalizeKey("https://media.example.com/media/a.jpg")
	assert.Equal(t, "media/a.jpg", got)
}

func TestS3Backend_PublicURL(t *testing.T) {
	b := NewS3Backend(S3Config{Endpoint: "https://acct.r2.cloudflarestorage.com", Bucket: "b", AccessKey: "a", SecretKey: "s", PublicURL: "https://pub.r2.dev/"})
	assert.True(t, b.cfg.UseSSL)
	assert.Equal(t, "acct.r2.cloudflarestorage.com", b.cfg.Endpoint)
	assert.Equal(t, "https://pub.r2.dev/media/a%20b.jpg", b.PublicURL("media/a b.jpg"))
	assert.Empty(t, b.PublicURL("../x"))

	b2 := NewS3Backend(S3Config{Endpoint: "x", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	assert.Empty(t, b2.PublicURL("media/a.jpg"))
}

func TestS3Backend_MisconfiguredNeverFallsBackToLocal(t *testing.T) {
	b := NewS3Backend(S3Config{Endpoint: "localhost:1", Bucket: "b"})
	assert.Equal(t, KindS3, b.Kind())
	err := b.Put(context.Background(), "media/a.jpg", bytes.NewReader([]byte("x")), 1, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "S3_ACCESS_KEY")
	require.Error(t, b.Check(context.Background()))
}

func TestLoadS3Config(t *testing.T) {
	old := config.C.Other
	defer func() { config.C.Other = old }()
	config.C.Other = map[string]string{
		"S3_ENDPOINT": "https://acct.r2.cloudflarestorage.com", "S3_REGION": "auto", "S3_BUCKET": "community-file",
		"S3_ACCESS_KEY": "k", "S3_SECRET_KEY": "s", "S3_FORCE_PATH_STYLE": "true", "S3_PUBLIC_URL": "https://media.example.com/",
	}
	cfg := LoadS3Config()
	assert.Equal(t, "auto", cfg.Region)
	assert.Equal(t, "true", cfg.PathStyle)
	assert.Equal(t, "https://media.example.com", cfg.PublicURL)
	assert.Equal(t, "uploads", cfg.LegacyLocalRoot)
}

func TestLoadS3ConfigUnsetHasNoPlaceholderBucket(t *testing.T) {
	old := config.C.Other
	defer func() { config.C.Other = old }()
	config.C.Other = map[string]string{}
	cfg := LoadS3Config()
	assert.Empty(t, cfg.Bucket)
	assert.Empty(t, cfg.Endpoint)
	assert.NotEqual(t, "community", cfg.Bucket)
	assert.NotEqual(t, "localhost:9000", cfg.Endpoint)
}

func TestGetContentType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"image.jpg", "image/jpeg"}, //nolint:goconst
		{"image.jpeg", "image/jpeg"},
		{"image.png", "image/png"},
		{"image.gif", "image/gif"},
		{"image.webp", "image/webp"},
		{"video.mp4", "video/mp4"},
		{"video.webm", "video/webm"},
		{"audio.mp3", "audio/mpeg"},
		{"audio.ogg", "audio/ogg"},
		{"audio.wav", "audio/wav"},
		{"file.txt", "application/octet-stream"}, //nolint:goconst
		{"file.unknown", "application/octet-stream"},
		{"IMAGE.JPG", "image/jpeg"}, // case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			assert.Equal(t, tt.expected, getContentType(tt.filename))
		})
	}
}

func TestHealthCacheAndLocalCheck(t *testing.T) {
	root := t.TempDir()
	restore := SetDefault(&LocalBackend{Root: root})
	defer restore()

	r := CheckHealth(context.Background())
	assert.True(t, r.Healthy)
	assert.False(t, r.Cached)
	r = CheckHealth(context.Background())
	assert.True(t, r.Cached)

	SetDefault(&LocalBackend{Root: filepath.Join(root, "missing")})
	r = CheckHealth(context.Background())
	assert.False(t, r.Healthy)
	assert.NotEmpty(t, r.Error)
}
