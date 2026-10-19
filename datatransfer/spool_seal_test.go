package datatransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

const sealTestSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

func withSpoolKey(t *testing.T, key string) {
	t.Helper()
	old := config.C.EncryptedKey
	config.C.EncryptedKey = key
	t.Cleanup(func() { config.C.EncryptedKey = old })
}

func spoolFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	var all []byte
	for _, p := range []string{path, path + "-wal"} {
		b, err := os.ReadFile(p)
		if err == nil {
			all = append(all, b...)
		}
	}
	return all
}

func TestSpoolSealsCredentialsAndRedactsRecordsAtRest(t *testing.T) {
	withSpoolKey(t, "0123456789abcdef0123456789abcdef")
	path := filepath.Join(t.TempDir(), "spool.sqlite3")
	sp, err := OpenSpool(path)
	require.NoError(t, err)
	ext, _ := json.Marshal(map[string]any{"twoFactorEnabled": true, "twoFactorSecret": sealTestSecret, "twoFactorBackupSecret": []string{"backup-code-one"}})
	data, _ := json.Marshal(map[string]any{"userId": "u0", "twoFactorSecret": sealTestSecret})
	b := &Bundle{Schema: Schema, Scope: "site", Source: Source{Platform: "misskey", BaseURL: "https://site.example"},
		Credentials: []Credential{{AccountID: "u0", PasswordHash: "$2a$10$hash", Extensions: ext}},
		Records:     []PreservedRecord{{Kind: "user_profile", ID: "u0", Data: data}}}
	raw, _ := json.Marshal(b)
	_, err = sp.ReadBundleJSON(context.Background(), bytes.NewReader(raw), nil)
	require.NoError(t, err)

	_, creds, err := sp.page("credentials", 0, 10)
	require.NoError(t, err)
	require.Len(t, creds, 1)
	require.Contains(t, string(creds[0]), sealTestSecret, "readers get the plaintext back")
	_, recs, err := sp.page("records", 0, 10)
	require.NoError(t, err)
	require.NotContains(t, string(recs[0]), sealTestSecret)
	require.NoError(t, sp.Close())

	disk := spoolFileBytes(t, path)
	require.NotContains(t, string(disk), sealTestSecret)
	require.NotContains(t, string(disk), "backup-code-one")
	require.NotContains(t, string(disk), "$2a$10$hash")

	// A different key cannot open the sealed records (and says so).
	withSpoolKey(t, "another-key-another-key-another-k")
	sp, err = OpenSpool(path)
	require.NoError(t, err)
	_, _, err = sp.page("credentials", 0, 10)
	require.ErrorIs(t, err, ErrSpoolKey)
	require.NoError(t, sp.Close())
}

// A spool written before sealing (the failed maid.lat rehearsal) is sealed in
// place on the next open, so re-running from the same staged data does not
// leave the plaintext behind.
func TestOpenSpoolSealsLegacyPlaintextCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.sqlite3")
	withSpoolKey(t, "")
	sp, err := OpenSpool(path)
	require.NoError(t, err)
	ext, _ := json.Marshal(map[string]any{"twoFactorEnabled": true, "twoFactorSecret": sealTestSecret})
	b := &Bundle{Schema: Schema, Scope: "site", Source: Source{Platform: "misskey", BaseURL: "https://site.example"},
		Credentials: []Credential{{AccountID: "u0", PasswordHash: "x", Extensions: ext}}}
	raw, _ := json.Marshal(b)
	_, err = sp.ReadBundleJSON(context.Background(), bytes.NewReader(raw), nil)
	require.NoError(t, err)
	require.NoError(t, sp.Close())
	require.Contains(t, string(spoolFileBytes(t, path)), sealTestSecret, "no key: legacy plaintext")

	withSpoolKey(t, "0123456789abcdef0123456789abcdef")
	sp, err = OpenSpool(path)
	require.NoError(t, err)
	_, creds, err := sp.page("credentials", 0, 10)
	require.NoError(t, err)
	require.Contains(t, string(creds[0]), sealTestSecret)
	require.NoError(t, sp.Close())
	require.NotContains(t, string(spoolFileBytes(t, path)), sealTestSecret)
}

func TestWidthTallyCountsEachRecordOnce(t *testing.T) {
	var tally widthTally
	a := widthIssue("emojis", "shortcode", 36, 2, 113, []string{"e1", "e2"})
	a.violators = []string{"e1", "e2"}
	b := widthIssue("emojis", "shortcode", 36, 2, 90, []string{"e2", "e3"})
	b.violators = []string{"e2", "e3"} // e2 repeats as a closure copy
	tally.add(a)
	tally.add(b)
	got := tally.list()
	require.Len(t, got, 1)
	require.Equal(t, 3, got[0].Count)
	require.Equal(t, 113, got[0].MaxLength)
	require.Equal(t, []string{"e1", "e2", "e3"}, got[0].SampleIDs)
	require.Contains(t, got[0].Message, "3 staged value(s) exceed emojis.shortcode (width 36, longest 113)")
}
