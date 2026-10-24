package setup

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kasuganosora/community/cmd/tootctl/commands"
	"github.com/kasuganosora/community/systemsettings"
)

// importRuntimeServiceSettings writes captcha, SMTP and object storage into
// system_settings. Misskey meta is imported earlier by stageMisskeyMeta.
// Values already saved are left alone. Nothing here is written to .env.
func importRuntimeServiceSettings(r *runner) error {
	job := r.t.snapshot()
	storage, secret := jobObjectStorage(job, r)
	values, err := runtimeServiceValues(serviceSecretDirs(), storage, secret)
	if err != nil {
		return fail("config_write_failed", "could not read service settings")
	}
	values = commands.WithoutStored(values)
	if len(values) == 0 {
		return nil
	}
	if err := systemsettings.SaveChanges(values); err != nil {
		return fail("config_write_failed", "could not save email or storage settings")
	}
	r.t.logf("info", "saved %s to the database", serviceSettingGroups(values))
	return nil
}

func jobObjectStorage(job *Job, r *runner) (*ObjectStorageParams, string) {
	secret := ""
	if r != nil && r.secrets != nil {
		secret = r.secrets.S3SecretKey
	}
	if job == nil {
		return nil, secret
	}
	switch job.Mode {
	case "misskey":
		if job.Misskey == nil {
			return nil, secret
		}
		o := job.Misskey.ObjectStorage
		return &o, secret
	case "mastodon":
		if job.Mastodon == nil {
			return nil, secret
		}
		o := job.Mastodon.ObjectStorage
		return &o, secret
	default:
		return nil, secret
	}
}

// serviceSecretDirs is SETUP_SECRETS_DIR when set, otherwise a secrets
// directory next to the config directory and ./secrets.
func serviceSecretDirs() []string {
	if dir := strings.TrimSpace(os.Getenv("SETUP_SECRETS_DIR")); dir != "" {
		return []string{dir}
	}
	dirs := []string{}
	if configDir := ConfigDir(); configDir != "" && configDir != "." {
		dirs = append(dirs, filepath.Join(filepath.Dir(configDir), "secrets"))
	}
	dirs = append(dirs, "secrets")
	return dirs
}

// runtimeServiceValues merges secrets/smtp.env, secrets/s3.env and object
// storage already on the setup job. Empty fields are skipped, so an unset
// bucket is never filled with a placeholder name. Non-empty job fields win
// over the files; a later WithoutStored pass lets saved database values win
// over both.
func runtimeServiceValues(dirs []string, storage *ObjectStorageParams, secret string) (map[string]any, error) {
	merged := map[string]string{}
	for _, dir := range dirs {
		for _, name := range []string{"smtp.env", "s3.env"} {
			vals, err := readSecretEnv(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			for key, value := range vals {
				if _, exists := merged[key]; exists || strings.TrimSpace(value) == "" {
					continue
				}
				merged[key] = value
			}
		}
	}
	out := commands.ServiceSettingsFromEnv(merged)
	if storage != nil {
		overlayObjectStorage(out, *storage, secret)
	}
	return out, nil
}

func readSecretEnv(path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return godotenv.Read(path)
}

func overlayObjectStorage(dst map[string]any, o ObjectStorageParams, secret string) {
	put := func(key, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		dst[key] = value
	}
	put("config.storage.endpoint", o.Endpoint)
	put("config.storage.bucket", o.Bucket)
	put("config.storage.region", o.Region)
	put("config.storage.access_key", o.AccessKey)
	put("config.storage.secret_key", secret)
	put("config.storage.base_url", strings.TrimRight(o.PublicURL, "/"))
	if o.PathStyle {
		dst["config.storage.path_style"] = "true"
	}
	if strings.TrimSpace(o.Bucket) != "" || strings.TrimSpace(o.Endpoint) != "" || strings.TrimSpace(o.AccessKey) != "" || strings.TrimSpace(secret) != "" {
		dst["config.storage.type"] = "s3"
	}
}

func serviceSettingGroups(values map[string]any) string {
	seen := map[string]bool{}
	for key := range values {
		switch {
		case strings.HasPrefix(key, "config.captcha."):
			seen["captcha"] = true
		case strings.HasPrefix(key, "config.email."):
			seen["email"] = true
		case strings.HasPrefix(key, "config.storage."):
			seen["storage"] = true
		}
	}
	groups := make([]string, 0, len(seen))
	for name := range seen {
		groups = append(groups, name)
	}
	sort.Strings(groups)
	if len(groups) == 0 {
		return "service settings"
	}
	return strings.Join(groups, ", ")
}
