// Package runtimeconfig applies saved admin settings. Email and object storage
// take effect in-process; search is still applied when the process starts.
package runtimeconfig

import (
	"fmt"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/email"
	"github.com/kasuganosora/community/storage"
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

// ApplyStored runs once after migrations and copies saved settings onto the
// process configuration. Admin saves of email and object storage call
// ApplyEmail and ApplyStorage so those clients change without a restart.
func EffectiveConfig() config.Config {
	cfg := config.C
	cfg.Other = make(map[string]string, len(config.C.Other))
	for k, v := range config.C.Other {
		cfg.Other[k] = v
	}
	fields := []struct {
		key    string
		target *string
	}{
		{"mode", &cfg.EmailMode},
		{"server", &cfg.SmtpServer},
		{"port", &cfg.SmtpPort},
		{"username", &cfg.SmtpUsername},
		{"password", &cfg.SmtpPassword},
		{"from", &cfg.SmtpFrom},
		{"auth_method", &cfg.SmtpAuthMethod},
		{"timeout", &cfg.SmtpTimeout},
		{"tls", &cfg.SmtpTLS},
	}
	for _, field := range fields {
		*field.target = fmt.Sprint(storedOrLegacy("config.email."+field.key, *field.target))
	}
	cfg.StorageType = fmt.Sprint(storedOrLegacy("config.storage.type", cfg.StorageType))
	if cfg.Other == nil {
		cfg.Other = map[string]string{}
	}
	for key, env := range map[string]string{
		"endpoint": "S3_ENDPOINT", "region": "S3_REGION", "bucket": "S3_BUCKET",
		"access_key": "S3_ACCESS_KEY", "secret_key": "S3_SECRET_KEY", "use_ssl": "S3_USE_SSL", "base_url": "S3_PUBLIC_URL", "path_style": "S3_FORCE_PATH_STYLE",
	} {
		if value, ok := systemsettings.Stored("config.storage." + key); ok && value != nil {
			cfg.Other[env] = fmt.Sprint(value)
			continue
		}
		// Legacy .env / process environment applies only while the database
		// has no saved value. Do not keep a synthetic bucket or endpoint.
		if !config.IsExplicitlyConfigured(env) {
			delete(cfg.Other, env)
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

// storedOrLegacy prefers a system_settings value. legacy (the value already
// loaded from the environment or .env) is used only when that key has not
// been saved. A saved empty string stays empty.
func storedOrLegacy(key string, legacy any) any {
	if value, ok := systemsettings.Stored(key); ok && value != nil {
		return value
	}
	return legacy
}

func ApplyStored() { config.C = EffectiveConfig(); email.Reload(); storage.ResetDefault() }

// ApplyEmail switches the running mailer to the saved SMTP settings without
// touching restart-only settings (storage, search) in config.C.
func ApplyEmail() {
	cfg := EffectiveConfig()
	config.C.EmailMode, config.C.SmtpServer, config.C.SmtpPort = cfg.EmailMode, cfg.SmtpServer, cfg.SmtpPort
	config.C.SmtpUsername, config.C.SmtpPassword, config.C.SmtpFrom = cfg.SmtpUsername, cfg.SmtpPassword, cfg.SmtpFrom
	config.C.SmtpAuthMethod, config.C.SmtpTimeout, config.C.SmtpTLS = cfg.SmtpAuthMethod, cfg.SmtpTimeout, cfg.SmtpTLS
	email.Reload()
}

// ApplyStorage switches the running object-storage client to the saved
// settings. Existing files are not moved.
func ApplyStorage() {
	cfg := EffectiveConfig()
	config.C.StorageType = cfg.StorageType
	if config.C.Other == nil {
		config.C.Other = map[string]string{}
	}
	for _, env := range []string{"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_USE_SSL", "S3_PUBLIC_URL", "S3_FORCE_PATH_STYLE"} {
		if value, ok := cfg.Other[env]; ok {
			config.C.Other[env] = value
			continue
		}
		delete(config.C.Other, env)
	}
	storage.ResetDefault()
}
