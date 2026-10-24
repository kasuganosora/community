package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlanEnvImportSkipsCoreKeysAndConvertsTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "DB_PASSWORD=x\nAPP_BASE_URL=https://a.example\nENCRYPTED_KEY=k\nSMTP_SERVER=smtp.example.org\nSMTP_PORT=2525\nS3_USE_SSL=false\nSMTP_TIMEOUT=abc\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := PlanEnvImport(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]EnvImportItem{}
	for _, item := range items {
		got[item.Env] = item
	}
	for _, core := range []string{"DB_PASSWORD", "APP_BASE_URL", "ENCRYPTED_KEY"} {
		if _, ok := got[core]; ok {
			t.Fatalf("core key %s must stay in .env", core)
		}
	}
	if got["SMTP_PORT"].Value != float64(2525) || got["SMTP_PORT"].Err != nil || got["S3_USE_SSL"].Value != false || got["SMTP_SERVER"].Err != nil {
		t.Fatalf("conversion failed: %+v", got)
	}
	if got["SMTP_TIMEOUT"].Err == nil {
		t.Fatal("invalid number must be reported")
	}
}

func TestApplyEnvImportHotReloadsEmailAndStorage(t *testing.T) {
	newAdminTestEnv(t)
	prev := config.C
	t.Cleanup(func() {
		config.C = prev
		storage.ResetDefault()
		for _, key := range []string{
			"config.email.server", "config.email.port", "config.email.password",
			"config.storage.type", "config.storage.endpoint", "config.storage.bucket", "config.storage.access_key",
		} {
			systemsettings.Set(key, nil)
		}
	})
	config.C = config.Config{EmailMode: "smtp", SmtpServer: "boot.example", SmtpPort: "587", StorageType: "local", Other: map[string]string{"S3_BUCKET": "boot-bucket"}}
	for _, env := range []string{"SMTP_SERVER", "SMTP_PORT", "SMTP_PASSWORD", "STORAGE_TYPE", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "CAPTCHA_PROVIDER", "CAPTCHA_SECRET_KEY"} {
		t.Setenv(env, "")
	}
	storage.ResetDefault()
	require.Equal(t, storage.KindLocal, storage.Default().Kind())

	path := filepath.Join(t.TempDir(), ".env")
	body := "SMTP_SERVER=smtp.example.org\nSMTP_PORT=2525\nSMTP_PASSWORD=super-secret\nSTORAGE_TYPE=s3\nS3_ENDPOINT=https://acct.r2.example\nS3_BUCKET=maid-file\nS3_ACCESS_KEY=AKIAEXAMPLE\nCAPTCHA_PROVIDER=hcaptcha\nCAPTCHA_SECRET_KEY=captcha-secret\nDB_PASSWORD=leave-me\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	items, err := PlanEnvImport(path)
	require.NoError(t, err)
	for _, item := range items {
		require.NotContains(t, []string{"CAPTCHA_PROVIDER", "CAPTCHA_SECRET_KEY", "DB_PASSWORD"}, item.Env)
	}
	var ok []EnvImportItem
	for _, item := range items {
		if item.Err == nil {
			ok = append(ok, item)
		}
	}
	require.NoError(t, ApplyEnvImport(ok))

	require.Equal(t, "smtp.example.org", config.C.SmtpServer)
	require.Equal(t, "2525", config.C.SmtpPort)
	require.Equal(t, "super-secret", config.C.SmtpPassword)
	require.Equal(t, "s3", config.C.StorageType)
	require.Equal(t, "maid-file", config.C.Other["S3_BUCKET"])
	require.Equal(t, "https://acct.r2.example", config.C.Other["S3_ENDPOINT"])
	require.Equal(t, "AKIAEXAMPLE", config.C.Other["S3_ACCESS_KEY"])
	require.Equal(t, storage.KindS3, storage.Default().Kind())
}

func TestApplyEnvImportDoesNotApplyWhenSaveFails(t *testing.T) {
	env := newAdminTestEnv(t)
	prev := config.C
	t.Cleanup(func() {
		config.C = prev
		storage.ResetDefault()
	})
	config.C = config.Config{EmailMode: "smtp", SmtpServer: "boot.example", StorageType: "local", Other: map[string]string{}}
	storage.ResetDefault()
	require.NoError(t, env.gdb.Callback().Create().Before("gorm:create").Register("qa_import_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_settings" {
			tx.AddError(gorm.ErrInvalidTransaction)
		}
	}))
	t.Cleanup(func() { _ = env.gdb.Callback().Create().Remove("qa_import_failure") })

	err := ApplyEnvImport([]EnvImportItem{{Env: "SMTP_SERVER", Key: "config.email.server", Value: "smtp.example.org"}, {Env: "STORAGE_TYPE", Key: "config.storage.type", Value: "s3"}})
	require.Error(t, err)
	require.Equal(t, "boot.example", config.C.SmtpServer)
	require.Equal(t, "local", config.C.StorageType)
	require.Equal(t, storage.KindLocal, storage.Default().Kind())
}

func TestApplyEnvImportLeavesUnrelatedRuntimeAlone(t *testing.T) {
	newAdminTestEnv(t)
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = config.Config{EmailMode: "smtp", SmtpServer: "boot.example", StorageType: "local", Other: map[string]string{"MEILISEARCH_HOST": "http://boot.example:7700"}}
	require.NoError(t, ApplyEnvImport([]EnvImportItem{
		{Env: "SITE_TITLE", Key: "title", Value: "Imported"},
		{Env: "MEILISEARCH_HOST", Key: "config.search.host", Value: "https://saved.example"},
	}))
	require.Equal(t, "boot.example", config.C.SmtpServer)
	require.Equal(t, "local", config.C.StorageType)
	require.Equal(t, "http://boot.example:7700", config.C.Other["MEILISEARCH_HOST"])
}

func TestShadowedByProcessEnvNeedsStoredValueAndEnv(t *testing.T) {
	env := map[string]string{"SMTP_SERVER": "smtp.example.org", "SMTP_PORT": " "}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	got := ShadowedByProcessEnv(lookup, func(string) bool { return true })
	if len(got) != 1 || got[0] != "SMTP_SERVER" {
		t.Fatalf("got %v", got)
	}
	if got := ShadowedByProcessEnv(lookup, func(string) bool { return false }); len(got) != 0 {
		t.Fatalf("unstored keys must not warn: %v", got)
	}
}
