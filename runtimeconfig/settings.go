// Package runtimeconfig applies restart-only admin settings before workers start.
package runtimeconfig

import (
	"fmt"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/email"
	"github.com/kasuganosora/community/systemsettings"
)

// Effective reads saved settings without overriding explicit deployment values.
// It does not mutate the running clients and is also used by diagnostics.
func Effective(key, env string, fallback any) any {
	if config.IsExplicitlyConfigured(env) {
		return fallback
	}
	if value := systemsettings.Get(key, fallback); value != nil {
		return value
	}
	return fallback
}

// ApplyStored runs once after migrations. Mutations on the running server only
// change persistent settings; SMTP/storage clients adopt them on next startup.
func EffectiveConfig() config.Config {
	cfg := config.C
	cfg.Other = make(map[string]string, len(config.C.Other))
	for k, v := range config.C.Other {
		cfg.Other[k] = v
	}
	fields := []struct {
		key, env string
		target   *string
	}{
		{"mode", "EMAIL_MODE", &cfg.EmailMode},
		{"server", "SMTP_SERVER", &cfg.SmtpServer},
		{"port", "SMTP_PORT", &cfg.SmtpPort},
		{"username", "SMTP_USERNAME", &cfg.SmtpUsername},
		{"password", "SMTP_PASSWORD", &cfg.SmtpPassword},
		{"from", "SMTP_FROM", &cfg.SmtpFrom},
		{"auth_method", "SMTP_AUTH_METHOD", &cfg.SmtpAuthMethod},
		{"timeout", "SMTP_TIMEOUT", &cfg.SmtpTimeout},
		{"tls", "SMTP_TLS", &cfg.SmtpTLS},
	}
	for _, field := range fields {
		*field.target = fmt.Sprint(Effective("config.email."+field.key, field.env, *field.target))
	}
	cfg.StorageType = fmt.Sprint(Effective("config.storage.type", "STORAGE_TYPE", cfg.StorageType))
	if cfg.Other == nil {
		cfg.Other = map[string]string{}
	}
	for key, env := range map[string]string{
		"endpoint": "S3_ENDPOINT", "region": "S3_REGION", "bucket": "S3_BUCKET",
		"access_key": "S3_ACCESS_KEY", "secret_key": "S3_SECRET_KEY", "use_ssl": "S3_USE_SSL", "base_url": "S3_PUBLIC_URL", "path_style": "S3_FORCE_PATH_STYLE",
	} {
		if !config.IsExplicitlyConfigured(env) {
			if value := systemsettings.Get("config.storage."+key, nil); value != nil {
				cfg.Other[env] = fmt.Sprint(value)
			}
		}
	}
	for key, env := range map[string]string{"enabled": "MEILISEARCH_ENABLED", "host": "MEILISEARCH_HOST", "api_key": "MEILISEARCH_API_KEY", "index_prefix": "MEILISEARCH_INDEX_PREFIX"} {
		if !config.IsExplicitlyConfigured(env) {
			if value := systemsettings.Get("config.search."+key, nil); value != nil {
				cfg.Other[env] = fmt.Sprint(value)
			}
		}
	}

	return cfg
}

func ApplyStored() { config.C = EffectiveConfig(); email.Reload() }
