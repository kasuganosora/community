package commands

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/kasuganosora/community/controllers/api/admin"
)

func TestSettingsImportDryRunDoesNotSaveAndMasksSecrets(t *testing.T) {
	items := []admin.EnvImportItem{
		{Env: "SMTP_SERVER", Key: "config.email.server", Value: "smtp.example.org"},
		{Env: "SMTP_PASSWORD", Key: "config.email.password", Value: "hunter2", Secret: true},
		{Env: "SMTP_PORT", Key: "config.email.port", Err: errors.New("SMTP_PORT is not a number")},
	}
	var out bytes.Buffer
	called := false
	if err := runSettingsImport(&out, ".env", items, true, func([]admin.EnvImportItem) error { called = true; return nil }, noEnv); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if called || strings.Contains(text, "hunter2") || !strings.Contains(text, "SKIP SMTP_PORT") || !strings.Contains(text, "  SMTP_SERVER\n") {
		t.Fatalf("unexpected dry run: called=%v\n%s", called, text)
	}
}

func TestSettingsImportAppliesOnlyValidItems(t *testing.T) {
	items := []admin.EnvImportItem{{Env: "A", Key: "a", Value: "x"}, {Env: "B", Key: "b", Err: errors.New("bad")}}
	var got []admin.EnvImportItem
	if err := runSettingsImport(&bytes.Buffer{}, ".env", items, false, func(v []admin.EnvImportItem) error { got = v; return nil }, noEnv); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Env != "A" {
		t.Fatalf("applied %v", got)
	}
}

func noEnv(string) (string, bool) { return "", false }

func TestSettingsImportNamesBothEnvFilesAndWarnsOnProcessEnv(t *testing.T) {
	items := []admin.EnvImportItem{{Env: "SMTP_SERVER", Key: "config.email.server", Value: "smtp.example.org"}, {Env: "SITE_TITLE", Key: "x", Value: "y"}}
	var out bytes.Buffer
	lookup := func(name string) (string, bool) { return "smtp.example.org", name == "SMTP_SERVER" }
	if err := runSettingsImport(&out, "/app/config/.env", items, false, func([]admin.EnvImportItem) error { return nil }, lookup); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"  SMTP_SERVER\n", "  SITE_TITLE\n", "BOTH", "env_file", "  - /app/config/.env", "WARNING: still set in this process environment (so they override the database): SMTP_SERVER\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}
