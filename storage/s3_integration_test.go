package storage_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests run against a real S3 compatible server, e.g.
//
//	TEST_S3_ENDPOINT=127.0.0.1:9000 TEST_S3_ACCESS_KEY=... TEST_S3_SECRET_KEY=... go test ./storage/
func TestS3Backend_RoundTrip(t *testing.T) {
	env := storagetest.S3FromEnv(t)
	bucket := env.NewBucket(t)
	b := storage.NewS3Backend(env.Config(bucket))
	ctx := context.Background()

	require.NoError(t, b.Check(ctx))

	payload := []byte("\x89PNG\r\n\x1a\nfake")
	require.NoError(t, b.Put(ctx, "media/2026/10/09/a.png", bytes.NewReader(payload), int64(len(payload)), "image/png"))

	info, err := b.Stat(ctx, "media/2026/10/09/a.png")
	require.NoError(t, err)
	assert.Equal(t, int64(len(payload)), info.Size)
	assert.Equal(t, "image/png", info.ContentType)

	rc, info, err := b.Open(ctx, "media/2026/10/09/a.png")
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, payload, got)
	assert.Equal(t, "image/png", info.ContentType)

	// Streaming upload with unknown size.
	require.NoError(t, b.Put(ctx, "media/stream.bin", io.LimitReader(bytes.NewReader(make([]byte, 1<<20)), 1<<20), -1, ""))
	info, err = b.Stat(ctx, "media/stream.bin")
	require.NoError(t, err)
	assert.Equal(t, int64(1<<20), info.Size)

	require.NoError(t, b.Delete(ctx, "media/2026/10/09/a.png"))
	_, _, err = b.Open(ctx, "media/2026/10/09/a.png")
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, b.Delete(ctx, "media/2026/10/09/a.png"), "deleting a missing object is fine")

	url, err := b.PresignedURL(ctx, "media/stream.bin", time.Minute)
	require.NoError(t, err)
	assert.Contains(t, url, bucket)
}

func TestS3Backend_LegacyLocalFallback(t *testing.T) {
	env := storagetest.S3FromEnv(t)
	cfg := env.Config(env.NewBucket(t))
	cfg.LegacyLocalRoot = t.TempDir()
	b := storage.NewS3Backend(cfg)
	ctx := context.Background()

	require.NoError(t, os.MkdirAll(filepath.Join(cfg.LegacyLocalRoot, "media/2025/01/01"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.LegacyLocalRoot, "media/2025/01/01/old.jpg"), []byte("old"), 0o600))

	rc, _, err := b.Open(ctx, "media/2025/01/01/old.jpg")
	require.NoError(t, err, "files written before switching to S3 stay readable")
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "old", string(got))

	require.NoError(t, b.Delete(ctx, "media/2025/01/01/old.jpg"))
	_, err = os.Stat(filepath.Join(cfg.LegacyLocalRoot, "media/2025/01/01/old.jpg"))
	assert.True(t, os.IsNotExist(err))
}

func TestS3Backend_HealthDetectsBadCredentialsAndBucket(t *testing.T) {
	env := storagetest.S3FromEnv(t)
	bucket := env.NewBucket(t)
	ctx := context.Background()

	bad := env.Config(bucket)
	bad.SecretKey = "definitely-wrong-secret-value"
	err := storage.NewS3Backend(bad).Check(ctx)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), bad.SecretKey)
	assert.NotContains(t, err.Error(), env.SecretKey)

	badKey := env.Config(bucket)
	badKey.AccessKey = "UNKNOWNACCESSKEYID"
	require.Error(t, storage.NewS3Backend(badKey).Check(ctx))

	missing := env.Config(bucket + "-missing")
	err = storage.NewS3Backend(missing).Check(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")

	// Cached report through the default backend.
	defer storage.SetDefault(storage.NewS3Backend(bad))()
	r := storage.CheckHealth(ctx)
	assert.False(t, r.Healthy)
	assert.Equal(t, storage.KindS3, r.Backend)
	assert.NotContains(t, r.Error, env.Endpoint)
	assert.NotContains(t, r.Error, bad.SecretKey)
	assert.True(t, storage.CheckHealth(ctx).Cached)

	storage.SetDefault(storage.NewS3Backend(env.Config(bucket)))
	assert.True(t, storage.CheckHealth(ctx).Healthy)

	// Unreachable endpoint times out quickly instead of hanging.
	old := storage.HealthTimeout
	storage.HealthTimeout = 500 * time.Millisecond
	defer func() { storage.HealthTimeout = old }()
	dead := env.Config(bucket)
	dead.Endpoint = "10.255.255.1:9"
	storage.SetDefault(storage.NewS3Backend(dead))
	start := time.Now()
	r = storage.CheckHealth(ctx)
	assert.False(t, r.Healthy)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.NotContains(t, r.Error, "10.255.255.1")
}
