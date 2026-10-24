package commands

import (
	"testing"

	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestMisskeyServiceSettingsTurnstileSMTPAndObjectStorage(t *testing.T) {
	got := MisskeyServiceSettings(map[string]string{
		"enableTurnstile":               "true",
		"turnstileSiteKey":              "site-public",
		"turnstileSecretKey":            "site-private",
		"enableHcaptcha":                "true",
		"hcaptchaSiteKey":               "ignored-site",
		"useObjectStorage":              "true",
		"objectStorageEndpoint":         "acct.r2.example",
		"objectStorageUseSSL":           "true",
		"objectStoragePort":             "443",
		"objectStorageBucket":           "maid-file",
		"objectStorageRegion":           "auto",
		"objectStorageAccessKey":        "ak",
		"objectStorageSecretKey":        "sk",
		"objectStorageBaseUrl":          "https://media.example/",
		"objectStorageS3ForcePathStyle": "true",
		"smtpHost":                      "smtp.example.test",
		"smtpPort":                      "2525",
		"smtpUser":                      "mailer",
		"smtpPass":                      "mail-secret",
		"smtpFrom":                      "noreply@example.test",
		"smtpSecure":                    "false",
	})
	require.Equal(t, "cloudflare", got[systemsettings.KeyCaptchaProvider])
	require.Equal(t, true, got[systemsettings.KeyCaptchaEnabled])
	require.Equal(t, "site-public", got[systemsettings.KeyCaptchaSiteKey])
	require.Equal(t, "site-private", got[systemsettings.KeyCaptchaSecretKey])
	require.NotEqual(t, "ignored-site", got[systemsettings.KeyCaptchaSiteKey])
	require.Equal(t, "s3", got["config.storage.type"])
	require.Equal(t, "https://acct.r2.example:443", got["config.storage.endpoint"])
	require.Equal(t, "maid-file", got["config.storage.bucket"])
	require.Equal(t, "auto", got["config.storage.region"])
	require.Equal(t, true, got["config.storage.use_ssl"])
	require.Equal(t, "true", got["config.storage.path_style"])
	require.Equal(t, "https://media.example", got["config.storage.base_url"])
	require.Equal(t, "smtp", got["config.email.mode"])
	require.Equal(t, "smtp.example.test", got["config.email.server"])
	require.Equal(t, float64(2525), got["config.email.port"])
	require.Equal(t, false, got["config.email.tls"])
	require.Equal(t, "mail-secret", got["config.email.password"])
}

func TestMisskeyServiceSettingsUnsetStorageHasNoBucket(t *testing.T) {
	got := MisskeyServiceSettings(map[string]string{"name": "Example"})
	require.Empty(t, got["config.storage.bucket"])
	require.NotContains(t, got, "config.storage.type")
	require.NotEqual(t, "community", got["config.storage.bucket"])
	cols := misskeyServiceColumns()
	require.Contains(t, cols, "enableTurnstile")
	require.Contains(t, cols, "objectStorageBucket")
	require.Contains(t, cols, "smtpHost")
}

func TestMisskeyServiceSettingsPlainHTTPEndpoint(t *testing.T) {
	got := MisskeyServiceSettings(map[string]string{
		"objectStorageEndpoint": "minio.internal",
		"objectStorageUseSSL":   "false",
		"objectStoragePort":     "9000",
		"objectStorageBucket":   "media",
	})
	require.Equal(t, "http://minio.internal:9000", got["config.storage.endpoint"])
	require.Equal(t, false, got["config.storage.use_ssl"])
}

func TestServiceSettingsFromEnvDoesNotInventBucket(t *testing.T) {
	got := ServiceSettingsFromEnv(map[string]string{
		"SMTP_SERVER":                     "smtp.example.test",
		"SMTP_PORT":                       "2525",
		"SMTP_PASSWORD":                   "mail-secret",
		"S3_ENDPOINT":                     "https://acct.r2.example",
		"S3_BUCKET":                       "maid-file",
		"S3_ACCESS_KEY":                   "ak",
		"S3_SECRET_KEY":                   "sk",
		"S3_USE_SSL":                      "true",
		"CLOUDFLARE_TURNSTILE_SITE_KEY":   "site-public",
		"CLOUDFLARE_TURNSTILE_SECRET_KEY": "site-private",
		"UNRELATED":                       "ignore",
	})
	require.Equal(t, "smtp.example.test", got["config.email.server"])
	require.Equal(t, float64(2525), got["config.email.port"])
	require.Equal(t, "smtp", got["config.email.mode"])
	require.Equal(t, "s3", got["config.storage.type"])
	require.Equal(t, "maid-file", got["config.storage.bucket"])
	require.Equal(t, "cloudflare", got[systemsettings.KeyCaptchaProvider])
	require.Equal(t, true, got[systemsettings.KeyCaptchaEnabled])
	require.Equal(t, "site-public", got[systemsettings.KeyCaptchaSiteKey])
	require.NotContains(t, got, "UNRELATED")

	empty := ServiceSettingsFromEnv(map[string]string{"S3_REGION": "us-east-1"})
	require.Empty(t, empty["config.storage.bucket"])
	require.NotContains(t, empty, "config.storage.type")
	require.NotEqual(t, "community", empty["config.storage.bucket"])
}

func TestWithoutStoredKeepsOnlyMissingKeys(t *testing.T) {
	systemsettings.Set("config.storage.bucket", "already")
	t.Cleanup(func() { systemsettings.Set("config.storage.bucket", nil) })
	got := WithoutStored(map[string]any{
		"config.storage.bucket": "maid-file",
		"config.email.server":   "smtp.example.test",
	})
	require.NotContains(t, got, "config.storage.bucket")
	require.Equal(t, "smtp.example.test", got["config.email.server"])
}
