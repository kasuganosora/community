package commands

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

func runPasskeys(args ...string) cliResult {
	var out, errOut bytes.Buffer
	app := &cli.App{
		Name: "tootctl", Commands: []*cli.Command{PasskeysCommand()},
		Writer: &out, ErrWriter: &errOut,
		ExitErrHandler: func(*cli.Context, error) {},
	}
	err := app.Run(append([]string{"tootctl", "passkeys", "import-misskey"}, args...))
	return cliResult{out: out.String(), errOut: errOut.String(), err: err}
}

func TestPasskeysImportMisskeyRequiresDSN(t *testing.T) {
	t.Setenv("MISSKEY_DSN", "")
	res := runPasskeys()
	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "--dsn")
}

// TestPasskeysImportMisskeyCLI runs the command against Misskey-shaped tables;
// the Misskey query is PostgreSQL-only, so this needs TEST_DATABASE_URL.
func TestPasskeysImportMisskeyCLI(t *testing.T) {
	if !pgtest.Enabled() {
		t.Skip("needs PostgreSQL (TEST_DATABASE_URL)")
	}
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.PasskeyCredential{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	old := misskeySource
	misskeySource = func(string) (*gorm.DB, func(), error) { return conn, func() {}, nil }
	t.Cleanup(func() { misskeySource = old })

	require.NoError(t, conn.Create(&models.User{Id: 7, UUID: "u7", UserName: "alice", EmailHash: "h7"}).Error)
	require.NoError(t, conn.Create(&models.Account{UserId: 7, Username: "alice", DisplayName: "Alice"}).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE "user" (id varchar(32) PRIMARY KEY, username varchar(128) NOT NULL, host varchar(512))`).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE user_security_key (id varchar PRIMARY KEY, "userId" varchar(32) NOT NULL, "publicKey" varchar NOT NULL,
		"lastUsed" timestamptz NOT NULL DEFAULT now(), name varchar(30) NOT NULL, counter bigint NOT NULL DEFAULT 0,
		"credentialDeviceType" varchar(32), "credentialBackedUp" boolean, transports varchar(32)[])`).Error)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	x, y := make([]byte, 32), make([]byte, 32)
	priv.X.FillBytes(x)
	priv.Y.FillBytes(y)
	raw, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: 2, Algorithm: -7}, Curve: 1, XCoord: x, YCoord: y,
	})
	require.NoError(t, err)
	cose := base64.RawURLEncoding.EncodeToString(raw)
	require.True(t, strings.HasPrefix(cose, "pQECAyYg"))
	require.NoError(t, conn.Exec(`INSERT INTO "user" VALUES ('9a', 'ALICE', NULL), ('9c', 'carol', NULL)`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO user_security_key (id, "userId", "publicKey", name, "credentialDeviceType", "credentialBackedUp", transports)
		VALUES ('AQID', '9a', ?, 'phone', 'multiDevice', true, '{internal,hybrid}'), ('BAUG', '9c', ?, 'key', 'singleDevice', false, NULL)`, cose, cose).Error)

	res := runPasskeys("--dsn", "postgres://ignored", "--dry-run")
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "[dry run] misskey keys: 2, imported: 1, skipped (already present): 0, unmatched: 1")
	assert.NotContains(t, res.out, cose[:12])

	res = runPasskeys("--dsn", "postgres://ignored", "-v")
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "imported: 1, skipped (already present): 0, unmatched: 1")
	assert.NotContains(t, res.out, cose[:12])
	res = runPasskeys("--dsn", "postgres://ignored")
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "imported: 0, skipped (already present): 1, unmatched: 1")
}
