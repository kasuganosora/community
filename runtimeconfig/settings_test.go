package runtimeconfig

import (
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEffectiveConfigStoredMappingDoesNotMutateRunningClients(t *testing.T) {
	previous := config.C
	t.Cleanup(func() { config.C = previous })
	config.C = config.Config{SmtpServer: "running.example", Other: map[string]string{"MEILISEARCH_HOST": "http://running.example:7700", "S3_BUCKET": "running-bucket", "unrelated": "keep"}}
	values := map[string]any{"config.email.server": "saved.example", "config.email.tls": false, "config.storage.bucket": "saved-bucket", "config.search.enabled": false, "config.search.host": "https://saved.example", "config.search.api_key": "saved-key", "config.search.index_prefix": "custom"}
	for key, value := range values {
		old := systemsettings.Get(key, nil)
		systemsettings.Set(key, value)
		t.Cleanup(func() { systemsettings.Set(key, old) })
	}
	for _, env := range []string{"SMTP_SERVER", "SMTP_TLS", "S3_BUCKET", "MEILISEARCH_HOST", "MEILISEARCH_ENABLED", "MEILISEARCH_API_KEY", "MEILISEARCH_INDEX_PREFIX"} {
		t.Setenv(env, "")
	}
	effective := EffectiveConfig()
	require.Equal(t, "saved.example", effective.SmtpServer)
	require.Equal(t, "false", effective.SmtpTLS)
	require.Equal(t, "saved-bucket", effective.Other["S3_BUCKET"])
	for key, value := range map[string]string{"MEILISEARCH_HOST": "https://saved.example", "MEILISEARCH_ENABLED": "false", "MEILISEARCH_API_KEY": "saved-key", "MEILISEARCH_INDEX_PREFIX": "custom"} {
		require.Equal(t, value, effective.Other[key])
	}
	require.Equal(t, "running.example", config.C.SmtpServer)
	require.Equal(t, "http://running.example:7700", config.C.Other["MEILISEARCH_HOST"])
	effective.Other["unrelated"] = "changed"
	require.Equal(t, "keep", config.C.Other["unrelated"])
}
func TestEffectiveConfigDeploymentOverridesSavedSettings(t *testing.T) {
	previous := config.C
	t.Cleanup(func() { config.C = previous })
	config.C = config.Config{SmtpServer: "deployed.example", Other: map[string]string{"MEILISEARCH_HOST": "https://deployed.example", "MEILISEARCH_ENABLED": "auto", "MEILISEARCH_API_KEY": "deployed-key", "MEILISEARCH_INDEX_PREFIX": "deployment", "S3_BUCKET": "deployment"}}
	for key, value := range map[string]any{"config.email.server": "saved.example", "config.search.host": "https://saved.example", "config.search.enabled": false, "config.search.api_key": "saved-key", "config.search.index_prefix": "saved", "config.storage.bucket": "saved"} {
		old := systemsettings.Get(key, nil)
		systemsettings.Set(key, value)
		t.Cleanup(func() { systemsettings.Set(key, old) })
	}
	for _, env := range []string{"SMTP_SERVER", "S3_BUCKET", "MEILISEARCH_HOST", "MEILISEARCH_ENABLED", "MEILISEARCH_API_KEY", "MEILISEARCH_INDEX_PREFIX"} {
		t.Setenv(env, "explicit")
	}
	effective := EffectiveConfig()
	require.Equal(t, config.C.SmtpServer, effective.SmtpServer)
	require.Equal(t, config.C.Other, effective.Other)
}
