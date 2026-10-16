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
	if err := runSettingsImport(&out, ".env", items, true, func([]admin.EnvImportItem) error { called = true; return nil }); err != nil {
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
	if err := runSettingsImport(&bytes.Buffer{}, ".env", items, false, func(v []admin.EnvImportItem) error { got = v; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Env != "A" {
		t.Fatalf("applied %v", got)
	}
}
