package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeServiceValuesFromSecretFilesAndJob(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "smtp.env"), []byte("SMTP_SERVER=smtp.example.test\nSMTP_PORT=2525\nSMTP_PASSWORD=mail-secret\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s3.env"), []byte("S3_ENDPOINT=https://acct.r2.example\nS3_BUCKET=maid-file\nS3_ACCESS_KEY=ak\nS3_SECRET_KEY=sk\nS3_USE_SSL=true\n"), 0o600))

	got, err := runtimeServiceValues([]string{dir}, &ObjectStorageParams{PublicURL: "https://media.example/"}, "")
	require.NoError(t, err)
	require.Equal(t, "smtp.example.test", got["config.email.server"])
	require.Equal(t, float64(2525), got["config.email.port"])
	require.Equal(t, "maid-file", got["config.storage.bucket"])
	require.Equal(t, "s3", got["config.storage.type"])
	require.Equal(t, "https://media.example", got["config.storage.base_url"])
	require.NotEqual(t, "community", got["config.storage.bucket"])

	referenceOnly, err := runtimeServiceValues(nil, &ObjectStorageParams{PublicURL: "https://cdn.example/files"}, "")
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example/files", referenceOnly["config.storage.base_url"])
	require.NotContains(t, referenceOnly, "config.storage.bucket")
	require.NotContains(t, referenceOnly, "config.storage.type")

	unset, err := runtimeServiceValues([]string{filepath.Join(dir, "missing")}, &ObjectStorageParams{}, "")
	require.NoError(t, err)
	require.Empty(t, unset["config.storage.bucket"])
	require.NotEqual(t, "community", unset["config.storage.bucket"])
}

func TestRuntimeServiceValuesJobOverridesEmptySecretFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s3.env"), []byte("S3_ENDPOINT=https://old.example\n"), 0o600))
	got, err := runtimeServiceValues([]string{dir}, &ObjectStorageParams{Endpoint: "https://acct.r2.example", Bucket: "maid-file"}, "sk")
	require.NoError(t, err)
	require.Equal(t, "https://acct.r2.example", got["config.storage.endpoint"])
	require.Equal(t, "maid-file", got["config.storage.bucket"])
	require.Equal(t, "sk", got["config.storage.secret_key"])
	require.Equal(t, "s3", got["config.storage.type"])
}
