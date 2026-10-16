package setup

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

// MastodonParams is the source Mastodon PostgreSQL database and media.
// Mastodon keeps its domain and storage settings in its environment, not in
// the database; the domain is detected from local status URIs.
type MastodonParams struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
	// MediaSource is "local" (FilesPath is Mastodon's public/system
	// directory) or "s3" (see ObjectStorage and MediaMode).
	MediaSource   string              `json:"media_source"`
	FilesPath     string              `json:"files_path"`
	MediaMode     string              `json:"media_mode"`
	ObjectStorage ObjectStorageParams `json:"object_storage"`
}

func mastodonStages() map[string]stageFunc {
	s := commonStages()
	s["mastodon_media"] = stageMastodonMedia
	s["mastodon_export"] = stageMastodonExport
	s["mastodon_preflight"] = func(ctx context.Context, r *runner) error {
		return preflight(ctx, r, "mastodon_preflight", "mastodon_export")
	}
	s["mastodon_import"] = func(ctx context.Context, r *runner) error { return importBundle(ctx, r, "mastodon_import") }
	return s
}

func (m *MastodonParams) normalizeConnection() *Error {
	m.Host, m.Name, m.User = strings.TrimSpace(m.Host), strings.TrimSpace(m.Name), strings.TrimSpace(m.User)
	if m.Host == "" || m.Name == "" || m.User == "" {
		return fail("mastodon_fields_required")
	}
	if strings.ContainsAny(m.Host, "/@?# \t\r\n") {
		return fail("invalid_database_host")
	}
	if m.Port == 0 {
		m.Port = 5432
	}
	if m.Port < 1 || m.Port > 65535 {
		return fail("invalid_database_port")
	}
	switch m.SSLMode {
	case "":
		m.SSLMode = "disable"
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return fail("invalid_database_sslmode")
	}
	return nil
}

func (m *MastodonParams) normalize() *Error {
	if err := m.normalizeConnection(); err != nil {
		return err
	}
	switch m.MediaSource {
	case "local":
		m.FilesPath = strings.TrimSpace(m.FilesPath)
		if m.FilesPath == "" {
			return fail("mastodon_files_required")
		}
	case "s3":
		o := &m.ObjectStorage
		o.Endpoint, o.Bucket, o.PublicURL = strings.TrimSpace(o.Endpoint), strings.TrimSpace(o.Bucket), strings.TrimRight(strings.TrimSpace(o.PublicURL), "/")
		switch m.MediaMode {
		case "reference":
			if u, err := url.Parse(o.PublicURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fail("object_storage_fields_required")
			}
		case "copy":
			if o.Endpoint == "" || o.Bucket == "" || o.AccessKey == "" {
				return fail("object_storage_fields_required")
			}
		default:
			return fail("object_storage_fields_required")
		}
	default:
		return fail("mastodon_files_required")
	}
	return nil
}

func openMastodon(m MastodonParams, password string) (*gorm.DB, error) {
	conn, err := openMisskey(MisskeyParams{Host: m.Host, Port: m.Port, Name: m.Name, User: m.User, SSLMode: m.SSLMode}, password)
	if err != nil {
		e := err.(*Error)
		return nil, fail("mastodon_connect_failed", e.Detail)
	}
	return conn, nil
}

// MastodonInspection summarizes a source database for the wizard.
type MastodonInspection struct {
	Counts            map[string]int64 `json:"counts"`
	DetectedBaseURL   string           `json:"detected_base_url"`
	BaseURLCandidates []string         `json:"base_url_candidates,omitempty"`
}

func inspectMastodon(conn *gorm.DB) (*MastodonInspection, *Error) {
	for _, t := range []string{"accounts", "statuses", "users"} {
		if !conn.Migrator().HasTable(t) {
			return nil, fail("mastodon_not_mastodon")
		}
	}
	res := &MastodonInspection{Counts: map[string]int64{}}
	count := func(key, sql string) {
		var n int64
		if conn.Raw(sql).Scan(&n).Error == nil {
			res.Counts[key] = n
		}
	}
	count("local_users", `SELECT count(*) FROM accounts WHERE domain IS NULL AND id > 0`)
	count("remote_users", `SELECT count(*) FROM accounts WHERE domain IS NOT NULL`)
	count("notes", `SELECT count(*) FROM statuses`)
	if conn.Migrator().HasTable("media_attachments") {
		count("files", `SELECT count(*) FROM media_attachments`)
	}
	if conn.Migrator().HasTable("custom_emojis") {
		count("emojis", `SELECT count(*) FROM custom_emojis WHERE domain IS NULL`)
	}
	if conn.Migrator().HasTable("follows") {
		count("follows", `SELECT count(*) FROM follows`)
	}
	// Local statuses store their canonical URI (https://<domain>/users/<name>/statuses/<id>).
	var uris []string
	conn.Raw(`SELECT s.uri FROM statuses s JOIN accounts a ON a.id = s.account_id WHERE a.domain IS NULL AND s.uri LIKE 'http%' ORDER BY s.id DESC LIMIT 200`).Scan(&uris)
	votes := map[string]int{}
	for _, u := range uris {
		if o := originOf(u, "/users/"); o != "" {
			votes[o]++
		}
	}
	for o := range votes {
		res.BaseURLCandidates = append(res.BaseURLCandidates, o)
	}
	sort.Slice(res.BaseURLCandidates, func(i, j int) bool { return votes[res.BaseURLCandidates[i]] > votes[res.BaseURLCandidates[j]] })
	if len(res.BaseURLCandidates) == 1 {
		res.DetectedBaseURL = res.BaseURLCandidates[0]
	}
	return res, nil
}

func (r *runner) mastodon() (*MastodonParams, error) {
	job := r.t.snapshot()
	if job.Mastodon == nil {
		return nil, fail("stage_failed", "no Mastodon source")
	}
	return job.Mastodon, nil
}

func mastodonScript() string {
	return utils.ResolveAppPath(filepath.Join("scripts", "migration", "export_mastodon_db.py"))
}

func (m *MastodonParams) pg(pass string) pgSource {
	return pgSource{m.Host, m.Port, m.Name, m.User, m.SSLMode, pass}
}

// stageMastodonMedia mirrors the bucket's Paperclip objects in copy mode.
// The exporter lists the keys, so the path rules live in one place.
func stageMastodonMedia(ctx context.Context, r *runner) error {
	m, err := r.mastodon()
	if err != nil {
		return err
	}
	if m.MediaSource != "s3" || m.MediaMode != "copy" {
		return errSkipStage
	}
	script := mastodonScript()
	if !exporterAvailable(script) {
		return fail("export_tool_missing")
	}
	client, err := objectClient(m.ObjectStorage, r.secrets.S3SecretKey)
	if err != nil {
		return err
	}
	listDir := filepath.Join(workDir(), "keys")
	_ = os.RemoveAll(listDir)
	defer func() { _ = os.RemoveAll(listDir) }()
	var objects []mirrorObject
	job := r.t.snapshot()
	r.t.logf("info", "listing media objects…")
	err = runExporter(ctx, r, "mastodon_media", script, m.pg(r.secrets.MisskeyPassword),
		[]string{"--base-url", job.BaseURL, "--output", listDir, "--list-object-keys", "--max-records", "100000000", "--max-snapshot-bytes", strconv.FormatInt(64<<30, 10)},
		func(line []byte) {
			var o struct {
				Key  string `json:"key"`
				Size int64  `json:"size"`
			}
			if json.Unmarshal(line, &o) == nil && o.Key != "" {
				objects = append(objects, mirrorObject{key: o.Key, size: o.Size})
			}
		})
	if err != nil {
		return err
	}
	return mirrorObjects(ctx, r, "mastodon_media", client, m.ObjectStorage.Bucket, objects)
}

// stageMastodonExport reads the Mastodon database in one read-only snapshot
// with scripts/migration/export_mastodon_db.py. A finished bundle is a
// checkpoint.
func stageMastodonExport(ctx context.Context, r *runner) error {
	m, err := r.mastodon()
	if err != nil {
		return err
	}
	out := exportDir()
	if _, err := os.Stat(filepath.Join(out, "bundle.json")); err == nil {
		r.t.logf("info", "export already complete; reusing %s", out)
		return nil
	}
	script := mastodonScript()
	if !exporterAvailable(script) {
		return fail("export_tool_missing")
	}
	_ = os.RemoveAll(out)
	job := r.t.snapshot()
	args := []string{"--base-url", job.BaseURL, "--output", out, "--max-records", "100000000",
		"--max-snapshot-bytes", strconv.FormatInt(64<<30, 10), "--max-media-bytes", strconv.FormatInt(16<<40, 10)}
	switch {
	case m.MediaSource == "local":
		args = append(args, "--media-dir", m.FilesPath)
	case m.MediaMode == "copy":
		args = append(args, "--object-storage-mode", "copy", "--object-storage-dir", filepath.Join(workDir(), "bucket"))
	default:
		args = append(args, "--object-storage-mode", "reference", "--object-base-url", m.ObjectStorage.PublicURL)
	}
	r.t.logf("info", "reading the Mastodon database in one read-only snapshot…")
	return runExporter(ctx, r, "mastodon_export", script, m.pg(r.secrets.MisskeyPassword), args, nil)
}
