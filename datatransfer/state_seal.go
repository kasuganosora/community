package datatransfer

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha3"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Field-level sealing for the exporter's fetch state (state/staging.sqlite3,
// raw source rows). Whole rows cannot be sealed there: the exporter queries
// id/reference columns with json_extract. Secret-named fields (password
// hashes, TOTP secrets, private keys, OAuth/app secrets, tokens, webhook and
// instance API secrets) are replaced by "enc1:<base64url>" values instead.
//
// Format v1 (mirrored by scripts/migration/export_misskey.py, stdlib only):
//
//	master  = StateKey (32 bytes, HMAC-SHA256(ENCRYPTED_KEY, label))
//	enc,mac = HMAC-SHA256(master, "enc"), HMAC-SHA256(master, "mac")
//	ct      = json(value) XOR SHAKE256(enc || nonce16)
//	tag     = HMAC-SHA256(mac, "v1" || nonce || ct)[:16]
//	token   = "enc1:" + base64url_nopad(nonce || ct || tag)

// StateKeyEnv passes the derived state key to the exporter process.
const StateKeyEnv = "COMMUNITY_TRANSFER_STATE_KEY"

const sealedFieldPrefix = "enc1:"

// secretFieldName matches source column names that hold secrets. Only
// non-empty string, array and object values are sealed (flags such as
// usePasswordLessLogin stay readable).
var secretFieldName = regexp.MustCompile(`(?i)(secret|password|privatekey|private_key|authkey|apikey|accesskey|smtppass|^token$|^hash$|^auth$|^code$)`)

// secretFieldHint is a cheap whole-row prefilter for secretFieldName.
var secretFieldHint = regexp.MustCompile(`(?i)(secret|password|private_?key|authkey|apikey|accesskey|smtppass|"token"|"hash"|"auth"|"code")`)

// StateKey returns the exporter state key derived from ENCRYPTED_KEY, or nil
// when no key is configured.
func StateKey() []byte {
	if config.C.EncryptedKey == "" {
		return nil
	}
	m := hmac.New(sha256.New, []byte(config.C.EncryptedKey))
	m.Write([]byte("community-transfer exporter state v1"))
	return m.Sum(nil)
}

// StateKeyHex is StateKey for the exporter environment ("" without a key).
func StateKeyHex() string {
	if k := StateKey(); k != nil {
		return hex.EncodeToString(k)
	}
	return ""
}

func subKey(master []byte, label string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte(label))
	return m.Sum(nil)
}

func sealField(master, plain []byte) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ks := sha3.SumSHAKE256(append(subKey(master, "enc"), nonce...), len(plain))
	ct := make([]byte, len(plain))
	for i := range plain {
		ct[i] = plain[i] ^ ks[i]
	}
	m := hmac.New(sha256.New, subKey(master, "mac"))
	m.Write([]byte("v1"))
	m.Write(nonce)
	m.Write(ct)
	out := append(append(nonce, ct...), m.Sum(nil)[:16]...)
	return sealedFieldPrefix + base64.RawURLEncoding.EncodeToString(out), nil
}

var errStateKey = errors.New("exporter state was sealed with a different ENCRYPTED_KEY")

func openField(master []byte, token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, sealedFieldPrefix))
	if err != nil || len(raw) < 32 {
		return nil, errStateKey
	}
	nonce, ct, tag := raw[:16], raw[16:len(raw)-16], raw[len(raw)-16:]
	m := hmac.New(sha256.New, subKey(master, "mac"))
	m.Write([]byte("v1"))
	m.Write(nonce)
	m.Write(ct)
	if !hmac.Equal(m.Sum(nil)[:16], tag) {
		return nil, errStateKey
	}
	ks := sha3.SumSHAKE256(append(subKey(master, "enc"), nonce...), len(ct))
	plain := make([]byte, len(ct))
	for i := range ct {
		plain[i] = ct[i] ^ ks[i]
	}
	return plain, nil
}

// sealRowFields seals the secret fields of one JSON object row. It returns
// the new row and whether anything changed.
func sealRowFields(master []byte, row []byte) ([]byte, bool, error) {
	if !secretFieldHint.Match(row) {
		return row, false, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(row, &fields) != nil {
		return row, false, nil
	}
	changed := false
	for k, v := range fields {
		if !secretFieldName.MatchString(k) || !sealableValue(v) {
			continue
		}
		token, err := sealField(master, v)
		if err != nil {
			return nil, false, err
		}
		fields[k], _ = json.Marshal(token)
		changed = true
	}
	if !changed {
		return row, false, nil
	}
	out, err := json.Marshal(fields)
	return out, err == nil, err
}

func sealableValue(v json.RawMessage) bool {
	t := bytes.TrimSpace(v)
	if len(t) == 0 {
		return false
	}
	switch t[0] {
	case '[', '{':
		return string(t) != "[]" && string(t) != "{}"
	case '"':
		var s string
		return json.Unmarshal(t, &s) == nil && s != "" && !strings.HasPrefix(s, sealedFieldPrefix)
	}
	return false
}

// SealExporterState seals, in place, every plaintext secret field an older
// exporter run left in stateDir/staging.sqlite3, then zeroes the freed pages
// (secure_delete + VACUUM) and truncates the WAL. It is a no-op without
// ENCRYPTED_KEY or without a state database, and cheap once sealed.
func SealExporterState(stateDir string) (int, error) {
	master := StateKey()
	path := filepath.Join(stateDir, "staging.sqlite3")
	if master == nil {
		return 0, nil
	}
	if _, err := os.Stat(path); err != nil {
		return 0, nil
	}
	gdb, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=secure_delete(ON)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return 0, err
	}
	db, err := gdb.DB()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var done string
	_ = db.QueryRow(`SELECT value FROM meta WHERE key = 'fields_sealed_v1'`).Scan(&done)
	if done == "1" {
		return 0, nil
	}
	n, err := sealStateRows(db, master)
	if err != nil {
		return n, err
	}
	if _, err := db.Exec(`INSERT INTO meta(key, value) VALUES('fields_sealed_v1', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		return n, err
	}
	if n > 0 {
		if _, err := db.Exec(`VACUUM`); err != nil {
			return n, err
		}
	}
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return n, err
}

func sealStateRows(db *sql.DB, master []byte) (int, error) {
	type upd struct {
		tbl, k string
		data   []byte
	}
	sealed := 0
	last := [2]string{"", ""}
	for {
		rows, err := db.Query(`SELECT tbl, k, data FROM rows WHERE (tbl, k) > (?, ?) ORDER BY tbl, k LIMIT 2000`, last[0], last[1])
		if err != nil {
			return sealed, err
		}
		var batch []upd
		n := 0
		for rows.Next() {
			var tbl, k string
			var data []byte
			if err := rows.Scan(&tbl, &k, &data); err != nil {
				rows.Close()
				return sealed, err
			}
			n++
			last = [2]string{tbl, k}
			out, changed, err := sealRowFields(master, data)
			if err != nil {
				rows.Close()
				return sealed, err
			}
			if changed {
				batch = append(batch, upd{tbl, k, out})
			}
		}
		rows.Close()
		if len(batch) > 0 {
			tx, err := db.Begin()
			if err != nil {
				return sealed, err
			}
			for _, u := range batch {
				if _, err := tx.Exec(`UPDATE rows SET data = ? WHERE tbl = ? AND k = ?`, string(u.data), u.tbl, u.k); err != nil {
					_ = tx.Rollback()
					return sealed, err
				}
			}
			if err := tx.Commit(); err != nil {
				return sealed, err
			}
			sealed += len(batch)
		}
		if n == 0 {
			return sealed, nil
		}
	}
}
