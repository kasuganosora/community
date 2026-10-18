package datatransfer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/twofactor"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// A secret in the format Misskey stores in user_profile.twoFactorSecret
// (otpauth Secret().base32: 32 base32 chars) and its plain backup list.
const (
	misskeySecret = "N5XGS3TPMVZGKZ3UGQZDGNBVGY3TQOJQ"
	misskeyBackup = "Xk3pQ9vL2m"
)

func misskeyTwoFactorBundle(t *testing.T, ext map[string]any, twoFactor bool) *Bundle {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	public, _ := x509.MarshalPKIXPublicKey(&private.PublicKey)
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)}))
	password, err := bcrypt.GenerateFromPassword([]byte("fixture passphrase"), bcrypt.MinCost)
	require.NoError(t, err)
	raw, _ := json.Marshal(ext)
	b := &Bundle{Schema: Schema, Scope: "site", Source: Source{Platform: "misskey", BaseURL: "https://site.example"}}
	b.Accounts = []Account{{ID: "qa", Username: "qa", Local: true, ActorURI: "https://site.example/users/qa"}}
	b.Credentials = []Credential{{AccountID: "qa", PasswordHash: string(password), PasswordScheme: "bcrypt", TwoFactor: twoFactor, Extensions: raw}}
	b.Keys = []FederationKey{{AccountID: "qa", KeyID: "https://site.example/users/qa#main-key", Primary: true, PublicKey: publicPEM, PrivateKey: privatePEM}}
	profile, _ := json.Marshal(map[string]any{"userId": "qa", "twoFactorEnabled": true, "twoFactorSecret": misskeySecret, "twoFactorBackupSecret": []string{misskeyBackup}})
	b.Records = []PreservedRecord{{Kind: "misskey:user_profile", ID: "qa", OwnerID: "qa", Data: profile}}
	return b
}

func withEncryptedKey(t *testing.T) {
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "transfer-test-key-0123456789abcd"
	t.Cleanup(func() { config.C.EncryptedKey = old })
}

func TestImportMisskeyTOTPAndBackupCodes(t *testing.T) {
	withEncryptedKey(t)
	conn := importerDB(t)
	b := misskeyTwoFactorBundle(t, map[string]any{"twoFactorEnabled": true, "twoFactorSecret": misskeySecret,
		"twoFactorBackupSecret": []string{misskeyBackup, "second0001"}, "securityKeysAvailable": true}, true)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}

	preview, err := Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	require.True(t, preview.CanExecute, "TOTP accounts no longer raise unsupported_auth: %+v", preview.Issues)
	codes := map[string]bool{}
	for _, is := range preview.Issues {
		codes[is.Code] = true
	}
	require.True(t, codes["security_keys_separate"])

	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	require.Equal(t, 1, report.Created["two_factor"])

	var user models.User
	require.NoError(t, conn.First(&user).Error)
	row, err := twofactor.Load(conn, user.Id)
	require.NoError(t, err)
	require.True(t, row.Enabled())
	require.NotContains(t, row.Secret, misskeySecret, "secret must be encrypted at rest")

	// a code generated from the Misskey secret verifies against the imported state
	code, err := twofactor.Generate(misskeySecret, time.Now())
	require.NoError(t, err)
	require.NoError(t, twofactor.Verify(conn, user.Id, code))
	require.NoError(t, twofactor.Verify(conn, user.Id, misskeyBackup))
	require.ErrorIs(t, twofactor.Verify(conn, user.Id, misskeyBackup), twofactor.ErrInvalidCode)

	// nothing secret in the report, receipts or preserved raw records
	for _, v := range []any{preview, report} {
		out, _ := json.Marshal(v)
		require.NotContains(t, string(out), misskeySecret)
		require.NotContains(t, string(out), misskeyBackup)
	}
	var records []StoredPreservedRecord
	require.NoError(t, conn.Find(&records).Error)
	require.NotEmpty(t, records)
	for _, r := range records {
		require.NotContains(t, string(r.Data), misskeySecret)
		require.NotContains(t, string(r.Data), misskeyBackup)
	}
	var hashes []models.UserBackupCode
	require.NoError(t, conn.Find(&hashes).Error)
	require.Len(t, hashes, 2)
	for _, h := range hashes {
		require.NotContains(t, strings.ToLower(h.CodeHash), strings.ToLower(misskeyBackup))
	}

	// a retry with the same bundle is a no-op (receipt digest stable)
	again, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err, "%+v", again.Issues)
	require.Equal(t, 1, again.Skipped["account"])
}

func TestPreflightUnsupportedAuthReasons(t *testing.T) {
	withEncryptedKey(t)
	conn := importerDB(t)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	cases := []struct {
		name   string
		ext    map[string]any
		tf     bool
		reason string
	}{
		{"mastodon-style flag only", nil, true, authReasonNoAdapter},
		{"bad secret", map[string]any{"twoFactorEnabled": true, "twoFactorSecret": "not base32 !!" + misskeySecret[:4]}, true, authReasonInvalidSecret},
		{"passwordless without totp", map[string]any{"usePasswordLessLogin": true, "securityKeysAvailable": true}, true, authReasonPasswordlessNoTOP},
		{"unknown extension", map[string]any{"twoFactorEnabled": true, "twoFactorSecret": misskeySecret, "webauthnDevice": "x"}, true, authReasonUnknownExtension},
		{"bad backup list", map[string]any{"twoFactorEnabled": true, "twoFactorSecret": misskeySecret, "twoFactorBackupSecret": misskeyBackup}, true, authReasonInvalidBackup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := misskeyTwoFactorBundle(t, tc.ext, tc.tf)
			if tc.ext == nil {
				b.Credentials[0].Extensions = nil
			}
			r, err := Preview(context.Background(), conn, b, o)
			require.NoError(t, err)
			require.False(t, r.CanExecute)
			found := false
			for _, is := range r.Issues {
				if is.Code == "unsupported_auth" {
					found = true
					require.True(t, strings.HasPrefix(is.Message, tc.reason+":"), is.Message)
				}
			}
			require.True(t, found)
			out, _ := json.Marshal(r)
			require.NotContains(t, string(out), misskeySecret)
			require.NotContains(t, string(out), misskeyBackup)
			_, err = Import(context.Background(), conn, b, o, nil)
			require.Error(t, err)
			require.NotContains(t, err.Error(), misskeySecret)
		})
	}
	// disabled 2FA with a leftover secret imports without TOTP
	b := misskeyTwoFactorBundle(t, map[string]any{"twoFactorSecret": misskeySecret}, false)
	r, err := Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	require.True(t, r.CanExecute, "%+v", r.Issues)
}

func TestCredentialTwoFactorNeverFormatsSecret(t *testing.T) {
	tf, reason := parseCredentialTwoFactor(Credential{Extensions: json.RawMessage(fmt.Sprintf(`{"twoFactorEnabled":true,"twoFactorSecret":%q,"twoFactorBackupSecret":[%q]}`, misskeySecret, misskeyBackup))})
	require.Empty(t, reason)
	for _, s := range []string{fmt.Sprint(tf), fmt.Sprintf("%v %+v %#v", *tf, *tf, *tf)} {
		require.NotContains(t, s, misskeySecret)
		require.NotContains(t, s, misskeyBackup)
	}
	red := redactTwoFactor(Credential{Extensions: json.RawMessage(fmt.Sprintf(`{"twoFactorEnabled":true,"twoFactorSecret":%q}`, misskeySecret))})
	require.NotContains(t, string(red.Extensions), misskeySecret)
}

func TestSpoolAndWorkDirModesAndWipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	old := syscallUmask(0o022)
	t.Cleanup(func() { syscallUmask(old) })
	work := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(work, 0o755))
	require.NoError(t, EnsurePrivateDir(work))
	st, _ := os.Stat(work)
	require.Equal(t, PrivateDirMode, st.Mode().Perm())

	sp, err := OpenSpool(filepath.Join(work, "spool.sqlite3"))
	require.NoError(t, err)
	require.NoError(t, sp.SetMeta("k", misskeySecret))
	for _, name := range []string{"spool.sqlite3", "spool.sqlite3-wal", "spool.sqlite3-shm"} {
		if st, err := os.Stat(filepath.Join(work, name)); err == nil {
			require.Equal(t, PrivateFileMode, st.Mode().Perm(), name)
		}
	}
	require.NoError(t, sp.Close())

	state := filepath.Join(work, "state")
	require.NoError(t, os.MkdirAll(state, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(state, "staging.sqlite3"), []byte(misskeySecret), 0o644))
	out := filepath.Join(work, "out-20260101T000000")
	require.NoError(t, os.MkdirAll(filepath.Join(out, ".spool"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(out, ".spool", "credentials"), []byte(misskeySecret), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(out, "media"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(out, "media", "a.png"), []byte("png"), 0o644))

	require.NoError(t, TightenTree(work))
	for _, p := range []string{state, out, filepath.Join(out, ".spool")} {
		st, _ := os.Stat(p)
		require.Equal(t, PrivateDirMode, st.Mode().Perm(), p)
	}
	for _, p := range []string{filepath.Join(state, "staging.sqlite3"), filepath.Join(out, "media", "a.png")} {
		st, _ := os.Stat(p)
		require.Equal(t, PrivateFileMode, st.Mode().Perm(), p)
	}

	wiped, err := WipeSecretSpool(work)
	require.NoError(t, err)
	require.NotEmpty(t, wiped)
	for _, p := range []string{filepath.Join(work, "spool.sqlite3"), state, filepath.Join(out, ".spool")} {
		_, err := os.Lstat(p)
		require.True(t, os.IsNotExist(err), p)
	}
	_, err = os.Stat(filepath.Join(out, "media", "a.png"))
	require.NoError(t, err, "copied media is kept")
}

func TestRedactSecretFields(t *testing.T) {
	in := json.RawMessage(fmt.Sprintf(`{"userId":"1","twoFactorSecret":%q,"twoFactorBackupSecret":[%q],"twoFactorTempSecret":null}`, misskeySecret, misskeyBackup))
	out, changed := RedactSecretFields(in)
	require.True(t, changed)
	require.NotContains(t, string(out), misskeySecret)
	require.NotContains(t, string(out), misskeyBackup)
	require.Contains(t, string(out), `"userId":"1"`)
	_, changed = RedactSecretFields(json.RawMessage(`{"a":1}`))
	require.False(t, changed)
}
