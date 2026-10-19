package datatransfer

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Spool is an on-disk staging area for a site transfer. Records are streamed
// in (from bundle.json or an exporter's NDJSON stream) and imported in chunks,
// so memory does not grow with the size of the source instance.
type Spool struct {
	db     *sql.DB
	gdb    *gorm.DB
	path   string
	Header Bundle // header fields only (schema, scope, source, owner)
	tx     *sql.Tx
	ins    *sql.Stmt
	n      int
}

// spoolSections are the bundle array sections, in bundle.json order.
var spoolSections = []string{"accounts", "credentials", "keys", "posts", "media", "relations", "lists", "reactions", "bookmarks",
	"polls", "poll_votes", "emojis", "records", "issues"}

func knownSection(s string) bool {
	for _, v := range spoolSections {
		if v == s {
			return true
		}
	}
	return false
}

// OpenSpool creates or reopens a spool file. A reopened spool keeps its
// records and header so an interrupted import can resume.
func OpenSpool(path string) (*Spool, error) {
	if err := os.MkdirAll(filepath.Dir(path), PrivateDirMode); err != nil {
		return nil, err
	}
	// Create the file 0600 before SQLite opens it; SQLite copies the
	// database file's mode to its -wal/-shm files.
	if err := ensurePrivateFile(path); err != nil {
		return nil, err
	}
	gdb, err := gorm.Open(sqlite.Open(path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, err
	}
	db, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS rec (seq INTEGER PRIMARY KEY, section TEXT NOT NULL, id TEXT NOT NULL, acct TEXT NOT NULL, raw BLOB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS rec_id ON rec(section, id)`,
		`CREATE INDEX IF NOT EXISTS rec_acct ON rec(section, acct)`,
		`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`,
	} {
		if _, err = db.Exec(q); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(p, PrivateFileMode)
	}
	s := &Spool{db: db, gdb: gdb, path: path}
	if h := s.Meta("header"); h != "" {
		_ = json.Unmarshal([]byte(h), &s.Header)
	}
	if err := s.sealLegacySecrets(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// sealLegacySecrets seals records of secret-bearing sections that a spool
// written by an older version still holds in plaintext (e.g. a failed
// rehearsal's work directory: credentials, actor private keys, raw source
// rows). secure_delete zeroes the freed pages, VACUUM rewrites the file and
// the WAL is truncated, so the old plaintext does not linger on disk.
func (s *Spool) sealLegacySecrets() error {
	if !SpoolSealingAvailable() {
		return nil
	}
	rows, err := s.db.Query(`SELECT seq, raw FROM rec WHERE section IN ('credentials', 'keys', 'records') AND substr(raw, 1, ?) <> ?`, len(sealedSpoolPrefix), sealedSpoolPrefix)
	if err != nil {
		return err
	}
	type plain struct {
		seq int64
		raw []byte
	}
	var todo []plain
	for rows.Next() {
		var p plain
		if err := rows.Scan(&p.seq, &p.raw); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if len(todo) == 0 {
		return nil
	}
	if _, err := s.db.Exec(`PRAGMA secure_delete = ON`); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, p := range todo {
		sealed, err := sealSpoolRecord(p.raw)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`UPDATE rec SET raw = ? WHERE seq = ?`, sealed, p.seq); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return err
	}
	_, err = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (s *Spool) Close() error { return s.db.Close() }

func (s *Spool) Meta(k string) string {
	var v string
	_ = s.db.QueryRow(`SELECT v FROM meta WHERE k = ?`, k).Scan(&v)
	return v
}

func (s *Spool) SetMeta(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// Reset drops spooled records (a new snapshot replaces the old one).
func (s *Spool) Reset() error {
	_, err := s.db.Exec(`DELETE FROM rec; DELETE FROM meta;`)
	return err
}

// Count returns the number of spooled records in a section.
func (s *Spool) Count(section string) int {
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM rec WHERE section = ?`, section).Scan(&n)
	return n
}

type spoolKeys struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	OwnerID   string `json:"owner_id"`
	AuthorID  string `json:"author_id"`
	PostID    string `json:"post_id"`
	PollID    string `json:"poll_id"`
	KeyID     string `json:"key_id"`
	Kind      string `json:"kind"`
}

func (s *Spool) setHeader(raw []byte) error {
	if err := s.flush(); err != nil {
		return err
	}
	var h Bundle
	if err := json.Unmarshal(raw, &h); err != nil {
		return fmt.Errorf("invalid bundle header: %w", err)
	}
	h.Accounts, h.Posts, h.Media = nil, nil, nil
	s.Header = h
	b, _ := json.Marshal(h)
	return s.SetMeta("header", string(b))
}

func (s *Spool) add(section string, raw []byte) error {
	if !knownSection(section) {
		return fmt.Errorf("unknown bundle section %q", section)
	}
	var k spoolKeys
	if err := json.Unmarshal(raw, &k); err != nil {
		return fmt.Errorf("invalid %s record: %w", section, err)
	}
	id, acct := k.ID, k.AccountID
	switch section {
	case "posts":
		acct = k.AuthorID
	case "media", "lists":
		acct = k.OwnerID
	case "polls":
		acct = k.PostID
	case "poll_votes":
		id = k.PollID + "\n" + k.AccountID
	case "keys":
		id = k.AccountID + "\n" + k.KeyID
	case "credentials":
		id = k.AccountID
	case "records":
		id, acct = k.Kind+"\n"+k.ID, k.OwnerID
	}
	if s.tx == nil {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		s.tx = tx
		if s.ins, err = tx.Prepare(`INSERT INTO rec(section, id, acct, raw) VALUES(?, ?, ?, ?)`); err != nil {
			return err
		}
	}
	if section == "records" {
		raw = redactPreservedRecord(raw)
	}
	if sealedSection(section) {
		sealed, err := sealSpoolRecord(raw)
		if err != nil {
			return err
		}
		raw = sealed
	}
	if _, err := s.ins.Exec(section, id, acct, raw); err != nil {
		return err
	}
	s.n++
	if s.n%5000 == 0 {
		return s.flush()
	}
	return nil
}

func (s *Spool) flush() error {
	if s.tx == nil {
		return nil
	}
	_ = s.ins.Close()
	err := s.tx.Commit()
	s.tx, s.ins = nil, nil
	return err
}

// SpoolStats reports how many records were read.
type SpoolStats struct {
	Records int
}

// ReadBundleJSON streams a bundle.json document into the spool token by token;
// only one record is held in memory at a time.
func (s *Spool) ReadBundleJSON(ctx context.Context, r io.Reader, onRecord func(int)) (SpoolStats, error) {
	if err := s.Reset(); err != nil {
		return SpoolStats{}, err
	}
	d := json.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	d.UseNumber()
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return SpoolStats{}, errors.New("bundle must be a JSON object")
	}
	header := map[string]json.RawMessage{}
	for d.More() {
		if err := ctx.Err(); err != nil {
			return SpoolStats{}, err
		}
		kt, err := d.Token()
		if err != nil {
			return SpoolStats{}, err
		}
		key, _ := kt.(string)
		if knownSection(key) {
			t, err := d.Token()
			if err != nil {
				return SpoolStats{}, err
			}
			if t == nil {
				continue
			}
			if t != json.Delim('[') {
				return SpoolStats{}, fmt.Errorf("section %s must be an array", key)
			}
			for d.More() {
				var raw json.RawMessage
				if err := d.Decode(&raw); err != nil {
					return SpoolStats{}, fmt.Errorf("invalid JSON in %s: %w", key, err)
				}
				if err := s.add(key, raw); err != nil {
					return SpoolStats{}, err
				}
				if onRecord != nil && s.n%10000 == 0 {
					onRecord(s.n)
				}
				if s.n%10000 == 0 {
					if err := ctx.Err(); err != nil {
						return SpoolStats{}, err
					}
				}
			}
			if _, err := d.Token(); err != nil {
				return SpoolStats{}, err
			}
			continue
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return SpoolStats{}, err
		}
		header[key] = raw
	}
	if _, err := d.Token(); err != nil {
		return SpoolStats{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return SpoolStats{}, errors.New("multiple JSON values in backup")
	}
	if err := s.flush(); err != nil {
		return SpoolStats{}, err
	}
	hb, _ := json.Marshal(header)
	if err := s.setHeader(hb); err != nil {
		return SpoolStats{}, err
	}
	return SpoolStats{Records: s.n}, s.SetMeta("complete", "1")
}

// ReadNDJSON spools the direct-mode exporter stream: {"header":{...}} then
// {"s":"<section>","v":{...}} per line.
func (s *Spool) ReadNDJSON(ctx context.Context, r io.Reader, onRecord func(int)) (SpoolStats, error) {
	if err := s.Reset(); err != nil {
		return SpoolStats{}, err
	}
	br := bufio.NewReaderSize(r, 1<<20)
	sawHeader := false
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var env struct {
				Header  json.RawMessage `json:"header"`
				Section string          `json:"s"`
				Value   json.RawMessage `json:"v"`
			}
			if e := json.Unmarshal(line, &env); e != nil {
				return SpoolStats{}, fmt.Errorf("invalid exporter stream line: %w", e)
			}
			switch {
			case len(env.Header) > 0:
				if e := s.setHeader(env.Header); e != nil {
					return SpoolStats{}, e
				}
				sawHeader = true
			case env.Section != "":
				if e := s.add(env.Section, env.Value); e != nil {
					return SpoolStats{}, e
				}
				if onRecord != nil && s.n%10000 == 0 {
					onRecord(s.n)
				}
			default:
				return SpoolStats{}, errors.New("unexpected exporter stream line")
			}
			if s.n%10000 == 0 {
				if e := ctx.Err(); e != nil {
					return SpoolStats{}, e
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return SpoolStats{}, err
		}
	}
	if !sawHeader {
		return SpoolStats{}, errors.New("exporter stream had no header")
	}
	if err := s.flush(); err != nil {
		return SpoolStats{}, err
	}
	return SpoolStats{Records: s.n}, s.SetMeta("complete", "1")
}

// page returns up to limit raw records of a section after seq.
func (s *Spool) page(section string, after int64, limit int) ([]int64, []json.RawMessage, error) {
	rows, err := s.db.Query(`SELECT seq, raw FROM rec WHERE section = ? AND seq > ? ORDER BY seq LIMIT ?`, section, after, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var seqs []int64
	var out []json.RawMessage
	for rows.Next() {
		var seq int64
		var raw []byte
		if err := rows.Scan(&seq, &raw); err != nil {
			return nil, nil, err
		}
		if raw, err = openSpoolRecord(raw); err != nil {
			return nil, nil, err
		}
		seqs = append(seqs, seq)
		out = append(out, raw)
	}
	return seqs, out, rows.Err()
}

func (s *Spool) lookup(section, column string, keys []string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for start := 0; start < len(keys); start += 500 {
		end := min(start+500, len(keys))
		part := keys[start:end]
		args := []any{section}
		for _, k := range part {
			args = append(args, k)
		}
		q := `SELECT raw FROM rec WHERE section = ? AND ` + column + ` IN (?` + strings.Repeat(",?", len(part)-1) + `) ORDER BY seq`
		rows, err := s.db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			if raw, err = openSpoolRecord(raw); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, raw)
		}
		rows.Close()
	}
	return out, nil
}
