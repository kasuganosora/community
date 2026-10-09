package health

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/assert"
)

func TestStorageChecker_S3_RealBucket(t *testing.T) {
	env := storagetest.S3FromEnv(t)
	bucket := env.NewBucket(t)

	good := env.Config(bucket)
	restore := storage.SetDefault(storage.NewS3Backend(good))
	defer restore()
	h := StorageChecker("s3")(context.Background())
	assert.Equal(t, StatusHealthy, h.Status, h.Message)

	bad := env.Config(bucket)
	bad.SecretKey = "wrong-secret-for-health"
	storage.SetDefault(storage.NewS3Backend(bad))
	h = StorageChecker("s3")(context.Background())
	assert.Equal(t, StatusUnhealthy, h.Status)
	assert.Contains(t, h.Message, "check failed")
	assert.NotContains(t, h.Message, bad.SecretKey)
	assert.NotContains(t, h.Message, env.AccessKey)

	storage.SetDefault(storage.NewS3Backend(env.Config(bucket + "-gone")))
	h = StorageChecker("s3")(context.Background())
	assert.Equal(t, StatusUnhealthy, h.Status)
	assert.Contains(t, h.Message, "does not exist")
}
