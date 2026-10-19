package datatransfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"github.com/stretchr/testify/require"
)

const privatePEM = "-----BEGIN RSA PRIVATE KEY-----\nMIIEsecretkeymaterial\n-----END RSA PRIVATE KEY-----"

// The rehearsal spool held 33 local actor private keys (keys section) and
// complete raw source rows (records: keypairs, app secrets, tokens) in
// plaintext. Both are sealed now, also in an older spool on reopen.
func TestSpoolSealsKeysAndRecordsIncludingLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.sqlite3")
	withSpoolKey(t, "")
	sp, err := OpenSpool(path)
	require.NoError(t, err)
	appRow, _ := json.Marshal(map[string]any{"id": "app1", "secret": "oauth-app-secret-value"})
	b := &Bundle{Schema: Schema, Scope: "site", Source: Source{Platform: "misskey", BaseURL: "https://site.example"},
		Keys:    []FederationKey{{AccountID: "u0", KeyID: "https://site.example/users/u0#main-key", PublicKey: "pub", PrivateKey: privatePEM}},
		Records: []PreservedRecord{{Kind: "misskey:app", ID: "app1", Data: appRow}}}
	raw, _ := json.Marshal(b)
	_, err = sp.ReadBundleJSON(context.Background(), bytes.NewReader(raw), nil)
	require.NoError(t, err)
	require.NoError(t, sp.Close())
	require.Contains(t, string(spoolFileBytes(t, path)), "secretkeymaterial", "legacy (no key) spool is plaintext")

	withSpoolKey(t, "0123456789abcdef0123456789abcdef")
	sp, err = OpenSpool(path)
	require.NoError(t, err)
	_, keys, err := sp.page("keys", 0, 10)
	require.NoError(t, err)
	require.Contains(t, string(keys[0]), "secretkeymaterial")
	byAcct, err := sp.lookup("keys", "acct", []string{"u0"})
	require.NoError(t, err)
	require.Len(t, byAcct, 1)
	_, recs, err := sp.page("records", 0, 10)
	require.NoError(t, err)
	require.Contains(t, string(recs[0]), "oauth-app-secret-value")
	require.NoError(t, sp.Close())
	disk := string(spoolFileBytes(t, path))
	require.NotContains(t, disk, "secretkeymaterial")
	require.NotContains(t, disk, "oauth-app-secret-value")

	// New writes are sealed directly.
	path2 := filepath.Join(t.TempDir(), "spool.sqlite3")
	sp, err = OpenSpool(path2)
	require.NoError(t, err)
	_, err = sp.ReadBundleJSON(context.Background(), bytes.NewReader(raw), nil)
	require.NoError(t, err)
	require.NoError(t, sp.Close())
	require.NotContains(t, string(spoolFileBytes(t, path2)), "secretkeymaterial")
}

func writeLegacyState(t *testing.T, dir string, rows map[string]map[string]any) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "staging.sqlite3")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE rows(tbl TEXT NOT NULL, k TEXT NOT NULL, sk TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY(tbl, k)) WITHOUT ROWID`,
		`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`,
	} {
		_, err = db.Exec(q)
		require.NoError(t, err)
	}
	for key, row := range rows {
		tbl, k, _ := strings.Cut(key, "/")
		data, _ := json.Marshal(row)
		_, err = db.Exec(`INSERT INTO rows VALUES(?, ?, ?, ?)`, tbl, k, k, string(data))
		require.NoError(t, err)
	}
	return path
}

func readStateRow(t *testing.T, path, tbl, k string) map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	var data string
	require.NoError(t, db.QueryRow(`SELECT data FROM rows WHERE tbl = ? AND k = ?`, tbl, k).Scan(&data))
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(data), &out))
	return out
}

func TestSealExporterStateSealsSecretFieldsInPlace(t *testing.T) {
	withSpoolKey(t, "0123456789abcdef0123456789abcdef")
	dir := filepath.Join(t.TempDir(), "state")
	path := writeLegacyState(t, dir, map[string]map[string]any{
		"user_profile/u0": {"userId": "u0", "password": "$2a$10$bcrypthashvalue", "twoFactorSecret": sealTestSecret, "twoFactorBackupSecret": []string{"backup-one"}, "usePasswordLessLogin": false, "email": "a@example.com"},
		"user_keypair/u0": {"userId": "u0", "publicKey": "pub", "privateKey": privatePEM},
		"app/a1":          {"id": "a1", "secret": "oauth-app-secret-value", "name": "Client"},
		"access_token/t1": {"id": "t1", "token": "native-token-value", "hash": "token-hash-value", "userId": "u0"},
		"meta/x":          {"id": "x", "objectStorageSecretKey": "s3-secret-value", "smtpPass": "smtp-pass-value", "deeplAuthKey": "deepl-key-value", "name": "maid"},
		"note/n1":         {"id": "n1", "text": "hello", "userId": "u0"},
	})
	n, err := SealExporterState(dir)
	require.NoError(t, err)
	require.Equal(t, 5, n)
	disk, _ := os.ReadFile(path)
	for _, secret := range []string{"bcrypthashvalue", sealTestSecret, "backup-one", "secretkeymaterial", "oauth-app-secret-value", "native-token-value", "token-hash-value", "s3-secret-value", "smtp-pass-value", "deepl-key-value"} {
		require.NotContains(t, string(disk), secret)
	}
	profile := readStateRow(t, path, "user_profile", "u0")
	require.Equal(t, "u0", profile["userId"], "ids stay queryable")
	require.Equal(t, false, profile["usePasswordLessLogin"], "flags stay readable")
	require.True(t, strings.HasPrefix(profile["twoFactorSecret"].(string), sealedFieldPrefix))
	plain, err := openField(StateKey(), profile["twoFactorSecret"].(string))
	require.NoError(t, err)
	require.Equal(t, `"`+sealTestSecret+`"`, string(plain))
	require.Equal(t, "hello", readStateRow(t, path, "note", "n1")["text"])

	again, err := SealExporterState(dir)
	require.NoError(t, err)
	require.Zero(t, again, "idempotent")

	// The exporter (Python, stdlib only) opens what Go sealed, and Go opens
	// what the exporter sealed: one format on both sides.
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	_, file, _, _ := runtime.Caller(0)
	scripts := filepath.Join(filepath.Dir(file), "..", "scripts", "migration")
	cmd := exec.Command(py, "-c", `
import json, sys, os
sys.path.insert(0, sys.argv[1])
import export_misskey as e
s = e.Staging(sys.argv[2])
p = s.get("user_profile", "u0")
k = s.get("user_keypair", "u0")
print(json.dumps({"secret": p["twoFactorSecret"], "backup": p["twoFactorBackupSecret"], "key": k["privateKey"], "sealed": e.FieldSealer.from_env().seal_value("from-python")}))
`, scripts, path)
	cmd.Env = append(os.Environ(), StateKeyEnv+"="+StateKeyHex())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got), string(out))
	require.Equal(t, sealTestSecret, got["secret"])
	require.Equal(t, []any{"backup-one"}, got["backup"])
	require.Equal(t, privatePEM, got["key"])
	back, err := openField(StateKey(), got["sealed"].(string))
	require.NoError(t, err)
	require.Equal(t, `"from-python"`, string(back))
}
