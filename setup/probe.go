package setup

import (
	"context"
	"database/sql"
	"math"
	"strings"

	"gorm.io/gorm"
)

// splitNullFraction splits a relation-size estimate into NULL and non-NULL
// parts. Misskey and Mastodon store local rows with a NULL host or domain.
// A missing fraction (table never analyzed) keeps the whole estimate on the
// local side instead of scanning the table.
func splitNullFraction(rows int64, nullFrac *float64) (local, remote int64) {
	if rows <= 0 {
		return 0, 0
	}
	if nullFrac == nil {
		return rows, 0
	}
	f := *nullFrac
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}
	local = int64(math.Round(float64(rows) * f))
	if local > rows {
		local = rows
	}
	if local < 0 {
		local = 0
	}
	return local, rows - local
}

// estimateRelation reads PostgreSQL's planner statistics for one table.
// It does not scan the heap. ok is false when the engine has no pg_class
// (or the table name does not resolve).
func estimateRelation(conn *gorm.DB, table, nullColumn string) (rows int64, nullFrac *float64, ok bool) {
	switch nullColumn {
	case "", "host", "domain":
	default:
		return 0, nil, false
	}
	var out struct {
		Rows     sql.NullFloat64
		NullFrac sql.NullFloat64
		Found    bool
	}
	err := conn.Raw(`
		SELECT c.reltuples AS rows, s.null_frac AS null_frac, TRUE AS found
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stats s ON s.schemaname = n.nspname AND s.tablename = c.relname AND s.attname = ?
		WHERE c.oid = to_regclass(?)`, nullColumn, table).Scan(&out).Error
	// reltuples is -1 until the table is analyzed. Publishing that as zero
	// would look like an empty instance, so leave the count unset.
	if err != nil || !out.Found || !out.Rows.Valid || out.Rows.Float64 < 0 {
		return 0, nil, false
	}
	if out.NullFrac.Valid {
		v := out.NullFrac.Float64
		nullFrac = &v
	}
	return int64(math.Round(out.Rows.Float64)), nullFrac, true
}

func setEstimate(counts map[string]int64, key, remoteKey string, rows int64, nullFrac *float64, ok bool) {
	if !ok {
		return
	}
	local, remote := splitNullFraction(rows, nullFrac)
	if nullFrac == nil && remoteKey == "" {
		counts[key] = rows
		return
	}
	counts[key] = local
	if remoteKey != "" {
		counts[remoteKey] = remote
	}
}

// probeSession pins the inspection to one transaction and, on PostgreSQL,
// bounds it with SET LOCAL. The limit disappears when the transaction
// commits, so a later export on another connection is not cut off.
func probeSession(conn *gorm.DB) (*gorm.DB, func()) {
	if conn.Dialector.Name() != "postgres" {
		return conn, func() {}
	}
	tx := conn.Begin()
	if tx.Error != nil {
		return conn, func() {}
	}
	if err := tx.Exec(`SET LOCAL statement_timeout = '8s'`).Error; err != nil {
		_ = tx.Rollback()
		return conn, func() {}
	}
	return tx, func() { _ = tx.Commit() }
}

// stageSourceProbe re-checks a migration source after the install request
// has already returned. It uses the same light inspection as the connect
// button (no full-table counts) and runs before configuration is written.
func stageSourceProbe(ctx context.Context, r *runner) error {
	job := r.t.snapshot()
	switch job.Mode {
	case "misskey":
		return probeMisskeyJob(ctx, r, job)
	case "mastodon":
		return probeMastodonJob(ctx, r, job)
	default:
		return errSkipStage
	}
}

func probeMisskeyJob(ctx context.Context, r *runner, job *Job) error {
	if job.Misskey == nil {
		return fail("stage_failed", "no Misskey source")
	}
	m := job.Misskey
	conn, err := openMisskey(*m, r.secrets.MisskeyPassword)
	if err != nil {
		return err
	}
	defer closeDB(conn)
	info, ierr := inspectMisskey(conn)
	if ierr != nil {
		return ierr
	}
	idMethod := m.IDMethod
	if idMethod == "" {
		idMethod = info.IDMethod
	}
	if idMethod == "" {
		return fail("invalid_id_method")
	}
	base, derr := sourceBaseURL(job.BaseURL, info.DetectedBaseURL, info.BaseURLCandidates)
	if derr != nil {
		return derr
	}
	if m.MediaSource == "s3" && m.MediaMode == "copy" {
		secret := r.secrets.S3SecretKey
		if secret == "" {
			secret = misskeyObjectSecret(conn)
		}
		if err := checkObjectStorage(ctx, m.ObjectStorage, secret); err != nil {
			return err
		}
	}
	r.t.setCounts("source_probe", info.Counts)
	r.t.update(func(j *Job) {
		j.BaseURL = base
		if j.Misskey != nil {
			j.Misskey.IDMethod = idMethod
		}
	})
	r.t.logf("info", "source database accepted")
	return nil
}

func probeMastodonJob(ctx context.Context, r *runner, job *Job) error {
	if job.Mastodon == nil {
		return fail("stage_failed", "no Mastodon source")
	}
	m := job.Mastodon
	conn, err := openMastodon(*m, r.secrets.MisskeyPassword)
	if err != nil {
		return err
	}
	defer closeDB(conn)
	info, ierr := inspectMastodon(conn)
	if ierr != nil {
		return ierr
	}
	base, derr := sourceBaseURL(job.BaseURL, info.DetectedBaseURL, info.BaseURLCandidates)
	if derr != nil {
		return derr
	}
	if m.MediaSource == "s3" && m.MediaMode == "copy" {
		if err := checkObjectStorage(ctx, m.ObjectStorage, r.secrets.S3SecretKey); err != nil {
			return err
		}
	}
	r.t.setCounts("source_probe", info.Counts)
	r.t.update(func(j *Job) { j.BaseURL = base })
	r.t.logf("info", "source database accepted")
	return nil
}

// misskeyLocalFileURLs keeps recent drive rows that can reveal the instance
// origin. The caller must pass a primary-key sample, not a filtered scan.
func misskeyLocalFileURLs(rows []misskeyFileSample) []string {
	urls := make([]string, 0, len(rows))
	for _, row := range rows {
		if !row.StoredInternal || strings.TrimSpace(row.URL) == "" {
			continue
		}
		if row.UserHost != nil && strings.TrimSpace(*row.UserHost) != "" {
			continue
		}
		urls = append(urls, row.URL)
	}
	return urls
}

type misskeyFileSample struct {
	URL            string
	StoredInternal bool
	UserHost       *string
}
