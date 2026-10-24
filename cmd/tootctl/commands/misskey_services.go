package commands

import (
	"strconv"
	"strings"

	"github.com/kasuganosora/community/systemsettings"
)

// misskeyServiceColumns are optional Misskey meta columns copied into
// system_settings. Columns missing on an older source are skipped.
func misskeyServiceColumns() []string {
	return []string{
		"enableTurnstile", "turnstileSiteKey", "turnstileSecretKey",
		"enableHcaptcha", "hcaptchaSiteKey", "hcaptchaSecretKey",
		"enableRecaptcha", "recaptchaSiteKey", "recaptchaSecretKey",
		"useObjectStorage", "objectStorageBucket", "objectStorageEndpoint", "objectStorageRegion",
		"objectStorageAccessKey", "objectStorageSecretKey", "objectStorageBaseUrl",
		"objectStorageUseSSL", "objectStoragePort", "objectStorageS3ForcePathStyle",
		"smtpHost", "smtpPort", "smtpUser", "smtpPass", "smtpSecure", "smtpFrom",
	}
}

// MisskeyServiceSettings maps a Misskey meta row (column name → text) onto
// the system_settings keys the admin UI already edits. Empty columns are
// omitted. The bucket is never invented: an unset object store stays unset.
func MisskeyServiceSettings(scalars map[string]string) map[string]any {
	out := map[string]any{}
	if provider, site, secret, enabled, ok := misskeyCaptcha(scalars); ok {
		out[systemsettings.KeyCaptchaProvider] = provider
		out[systemsettings.KeyCaptchaEnabled] = enabled
		if site != "" {
			out[systemsettings.KeyCaptchaSiteKey] = site
		}
		if secret != "" {
			out[systemsettings.KeyCaptchaSecretKey] = secret
		}
	}
	for key, value := range misskeyObjectStorage(scalars) {
		out[key] = value
	}
	for key, value := range misskeySMTP(scalars) {
		out[key] = value
	}
	return out
}

type captchaSource struct {
	provider, siteCol, secretCol, enableCol string
}

func misskeyCaptcha(scalars map[string]string) (provider, site, secret string, enabled, ok bool) {
	sources := []captchaSource{
		{"cloudflare", "turnstileSiteKey", "turnstileSecretKey", "enableTurnstile"},
		{"hcaptcha", "hcaptchaSiteKey", "hcaptchaSecretKey", "enableHcaptcha"},
		{"recaptcha", "recaptchaSiteKey", "recaptchaSecretKey", "enableRecaptcha"},
	}
	chosen := -1
	for i, src := range sources {
		if on, present := misskeyBool(scalars, src.enableCol); present && on {
			chosen = i
			break
		}
	}
	if chosen < 0 {
		for i, src := range sources {
			if strings.TrimSpace(scalars[src.siteCol]) != "" || strings.TrimSpace(scalars[src.secretCol]) != "" {
				chosen = i
				break
			}
		}
	}
	if chosen < 0 {
		return "", "", "", false, false
	}
	src := sources[chosen]
	site = strings.TrimSpace(scalars[src.siteCol])
	secret = strings.TrimSpace(scalars[src.secretCol])
	if on, present := misskeyBool(scalars, src.enableCol); present {
		enabled = on
	} else {
		enabled = site != "" && secret != ""
	}
	return src.provider, site, secret, enabled, true
}

func misskeyObjectStorage(scalars map[string]string) map[string]any {
	out := map[string]any{}
	endpoint := misskeyObjectEndpoint(scalars)
	bucket := strings.TrimSpace(scalars["objectStorageBucket"])
	region := strings.TrimSpace(scalars["objectStorageRegion"])
	access := strings.TrimSpace(scalars["objectStorageAccessKey"])
	secret := strings.TrimSpace(scalars["objectStorageSecretKey"])
	publicURL := strings.TrimRight(strings.TrimSpace(scalars["objectStorageBaseUrl"]), "/")
	if endpoint != "" {
		out["config.storage.endpoint"] = endpoint
	}
	if bucket != "" {
		out["config.storage.bucket"] = bucket
	}
	if region != "" {
		out["config.storage.region"] = region
	}
	if access != "" {
		out["config.storage.access_key"] = access
	}
	if secret != "" {
		out["config.storage.secret_key"] = secret
	}
	if publicURL != "" {
		out["config.storage.base_url"] = publicURL
	}
	if on, present := misskeyBool(scalars, "objectStorageUseSSL"); present {
		out["config.storage.use_ssl"] = on
	}
	if on, present := misskeyBool(scalars, "objectStorageS3ForcePathStyle"); present {
		if on {
			out["config.storage.path_style"] = "true"
		} else {
			out["config.storage.path_style"] = "false"
		}
	}
	if bucket != "" || endpoint != "" || access != "" || secret != "" {
		out["config.storage.type"] = "s3"
	}
	return out
}

func misskeyObjectEndpoint(scalars map[string]string) string {
	endpoint := strings.TrimSpace(scalars["objectStorageEndpoint"])
	if endpoint == "" {
		return ""
	}
	if strings.Contains(endpoint, "://") {
		return strings.TrimRight(endpoint, "/")
	}
	scheme := "https://"
	if on, present := misskeyBool(scalars, "objectStorageUseSSL"); present && !on {
		scheme = "http://"
	}
	endpoint = scheme + endpoint
	port := strings.TrimSpace(scalars["objectStoragePort"])
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if port != "" && port != "0" && !strings.Contains(host, ":") {
		endpoint += ":" + port
	}
	return endpoint
}

func misskeySMTP(scalars map[string]string) map[string]any {
	out := map[string]any{}
	put := func(column, key string) {
		if v := strings.TrimSpace(scalars[column]); v != "" {
			out[key] = v
		}
	}
	put("smtpHost", "config.email.server")
	put("smtpUser", "config.email.username")
	put("smtpPass", "config.email.password")
	put("smtpFrom", "config.email.from")
	if port := strings.TrimSpace(scalars["smtpPort"]); port != "" {
		if n, err := strconv.Atoi(port); err == nil && n > 0 {
			out["config.email.port"] = float64(n)
		}
	}
	if on, present := misskeyBool(scalars, "smtpSecure"); present {
		out["config.email.tls"] = on
	}
	if _, ok := out["config.email.server"]; ok {
		out["config.email.mode"] = "smtp"
	}
	return out
}

func misskeyBool(scalars map[string]string, key string) (bool, bool) {
	raw, exists := scalars[key]
	if !exists {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "t", "1", "yes":
		return true, true
	case "false", "f", "0", "no":
		return false, true
	default:
		return false, false
	}
}

// ServiceSettingsFromEnv maps SMTP_*, S3_* and captcha variables (as in
// secrets/smtp.env and secrets/s3.env) onto the same system_settings keys.
// Unknown keys are ignored. An empty bucket is not replaced with a name.
func ServiceSettingsFromEnv(env map[string]string) map[string]any {
	out := map[string]any{}
	assign := func(envKey, setting, kind string) {
		raw := strings.TrimSpace(env[envKey])
		if raw == "" {
			return
		}
		switch kind {
		case "bool":
			if on, ok := misskeyBool(map[string]string{envKey: raw}, envKey); ok {
				out[setting] = on
			}
		case "port":
			n, err := strconv.Atoi(raw)
			if err == nil && n > 0 && n < 65536 {
				out[setting] = float64(n)
			}
		case "provider":
			switch strings.ToLower(raw) {
			case "turnstile":
				out[setting] = "cloudflare"
			default:
				out[setting] = strings.ToLower(raw)
			}
		default:
			out[setting] = raw
		}
	}
	assign("EMAIL_MODE", "config.email.mode", "")
	assign("SMTP_SERVER", "config.email.server", "")
	assign("SMTP_PORT", "config.email.port", "port")
	assign("SMTP_USERNAME", "config.email.username", "")
	assign("SMTP_PASSWORD", "config.email.password", "")
	assign("SMTP_FROM", "config.email.from", "")
	assign("SMTP_AUTH_METHOD", "config.email.auth_method", "")
	assign("SMTP_TIMEOUT", "config.email.timeout", "port")
	assign("SMTP_TLS", "config.email.tls", "bool")
	assign("STORAGE_TYPE", "config.storage.type", "")
	assign("S3_ENDPOINT", "config.storage.endpoint", "")
	assign("S3_REGION", "config.storage.region", "")
	assign("S3_BUCKET", "config.storage.bucket", "")
	assign("S3_ACCESS_KEY", "config.storage.access_key", "")
	assign("S3_SECRET_KEY", "config.storage.secret_key", "")
	assign("S3_USE_SSL", "config.storage.use_ssl", "bool")
	assign("S3_PUBLIC_URL", "config.storage.base_url", "")
	assign("S3_FORCE_PATH_STYLE", "config.storage.path_style", "")
	assign("CAPTCHA_PROVIDER", systemsettings.KeyCaptchaProvider, "provider")
	assign("CAPTCHA_SITE_KEY", systemsettings.KeyCaptchaSiteKey, "")
	assign("CAPTCHA_SECRET_KEY", systemsettings.KeyCaptchaSecretKey, "")
	assign("CAPTCHA_ENABLED", systemsettings.KeyCaptchaEnabled, "bool")
	if _, ok := out[systemsettings.KeyCaptchaSiteKey]; !ok {
		assign("CLOUDFLARE_TURNSTILE_SITE_KEY", systemsettings.KeyCaptchaSiteKey, "")
	}
	if _, ok := out[systemsettings.KeyCaptchaSecretKey]; !ok {
		assign("CLOUDFLARE_TURNSTILE_SECRET_KEY", systemsettings.KeyCaptchaSecretKey, "")
	}
	if _, ok := out[systemsettings.KeyCaptchaProvider]; !ok {
		if strings.TrimSpace(env["CLOUDFLARE_TURNSTILE_SITE_KEY"]) != "" || strings.TrimSpace(env["CLOUDFLARE_TURNSTILE_SECRET_KEY"]) != "" {
			out[systemsettings.KeyCaptchaProvider] = "cloudflare"
		}
	}
	if _, ok := out["config.email.mode"]; !ok {
		if _, hasServer := out["config.email.server"]; hasServer {
			out["config.email.mode"] = "smtp"
		}
	}
	if _, ok := out["config.storage.type"]; !ok {
		if out["config.storage.bucket"] != nil || out["config.storage.endpoint"] != nil || out["config.storage.access_key"] != nil {
			out["config.storage.type"] = "s3"
		}
	}
	if _, ok := out[systemsettings.KeyCaptchaEnabled]; !ok && out[systemsettings.KeyCaptchaProvider] != nil {
		out[systemsettings.KeyCaptchaEnabled] = true
	}
	return out
}

// WithoutStored drops keys that already have a saved value so a cutover
// re-run fills gaps and does not overwrite later admin edits.
func WithoutStored(values map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		if value == nil {
			continue
		}
		if v, ok := systemsettings.Stored(key); ok && v != nil {
			continue
		}
		out[key] = value
	}
	return out
}
