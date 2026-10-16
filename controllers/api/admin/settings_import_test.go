package admin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanEnvImportSkipsCoreKeysAndConvertsTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "DB_PASSWORD=x\nAPP_BASE_URL=https://a.example\nENCRYPTED_KEY=k\nSMTP_SERVER=smtp.example.org\nSMTP_PORT=587\nS3_USE_SSL=false\nSMTP_TIMEOUT=abc\n"
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
	if got["SMTP_PORT"].Value != float64(587) || got["S3_USE_SSL"].Value != false || got["SMTP_SERVER"].Err != nil {
		t.Fatalf("conversion failed: %+v", got)
	}
	if got["SMTP_TIMEOUT"].Err == nil {
		t.Fatal("invalid number must be reported")
	}
}
