package models

import (
	"context"
	"database/sql"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StatusContentInspection summarizes status_contents integrity without mutating
// any row or database sequence. Reblogs without a content row are reported
// separately because pure boosts legitimately have no status_contents entry.
type StatusContentInspection struct {
	MissingNonReblogTotal int64  `json:"missing_non_reblog_total"`
	MissingLocal          int64  `json:"missing_local"`
	MissingRemote         int64  `json:"missing_remote"`
	ReblogsWithoutContent int64  `json:"reblogs_without_content"`
	MaxStatusID           uint64 `json:"max_status_id"`
	MaxContentStatusID    uint64 `json:"max_content_status_id"`
	SequenceName          string `json:"sequence_name,omitempty"`
	SequenceLastValue     int64  `json:"sequence_last_value,omitempty"`
	SequenceLagging       bool   `json:"sequence_lagging"`
}

// InspectStatusContents checks for non-reblog statuses missing a status_contents
// row and, on PostgreSQL, inspects whether a legacy status_contents sequence
// lags behind MAX(status_id). It never calls setval or modifies data.
func InspectStatusContents(ctx context.Context, conn *gorm.DB) (StatusContentInspection, error) {
	var out StatusContentInspection
	if conn == nil {
		return out, errors.New("database unavailable")
	}
	db := conn.WithContext(ctx)

	baseMissing := func() *gorm.DB {
		return db.Table("statuses s").
			Joins("LEFT JOIN status_contents c ON c.status_id = s.id").
			Where("s.deleted_at IS NULL AND c.status_id IS NULL")
	}

	if err := baseMissing().Where("(s.reblog_id IS NULL OR s.reblog_id = 0) AND (s.domain IS NULL OR s.domain = '') AND (s.origin_uri IS NULL OR s.origin_uri = '')").Count(&out.MissingLocal).Error; err != nil {
		return out, err
	}
	if err := baseMissing().Where("(s.reblog_id IS NULL OR s.reblog_id = 0) AND ((s.domain IS NOT NULL AND s.domain <> '') OR (s.origin_uri IS NOT NULL AND s.origin_uri <> ''))").Count(&out.MissingRemote).Error; err != nil {
		return out, err
	}
	out.MissingNonReblogTotal = out.MissingLocal + out.MissingRemote

	if err := baseMissing().Where("s.reblog_id IS NOT NULL AND s.reblog_id > 0").Count(&out.ReblogsWithoutContent).Error; err != nil {
		return out, err
	}

	_ = db.Table("statuses").Select("COALESCE(MAX(id), 0)").Scan(&out.MaxStatusID).Error
	_ = db.Table("status_contents").Select("COALESCE(MAX(status_id), 0)").Scan(&out.MaxContentStatusID).Error

	if dialect.IsPostgres(db) {
		var seq sql.NullString
		if err := db.Raw("SELECT pg_get_serial_sequence('status_contents', 'status_id')").Scan(&seq).Error; err == nil && seq.Valid && seq.String != "" {
			out.SequenceName = seq.String
			var lastVal sql.NullInt64
			// pg_sequences.last_value is NULL before the first nextval call.
			if err := db.Raw("SELECT last_value FROM pg_sequences WHERE schemaname = current_schema() AND sequencename = split_part(?, '.', 2)", seq.String).Scan(&lastVal).Error; err == nil {
				if lastVal.Valid {
					out.SequenceLastValue = lastVal.Int64
				}
				target := out.MaxContentStatusID
				if out.MaxStatusID > target {
					target = out.MaxStatusID
				}
				if target > 0 && (!lastVal.Valid || uint64(lastVal.Int64) < target) {
					out.SequenceLagging = true
				}
			}
		}
	}
	return out, nil
}

// BackfillMissingStatusContents inserts empty plain-text status_contents rows
// using each status's explicit parent id for live non-reblog statuses that are
// missing a content row. Reblogs are never touched, PK conflicts are ignored,
// and database sequences are never advanced.
func BackfillMissingStatusContents(ctx context.Context, conn *gorm.DB, dryRun bool) (int64, error) {
	if conn == nil {
		return 0, errors.New("database unavailable")
	}
	db := conn.WithContext(ctx)
	var cursor uint64
	var repaired int64

	for {
		var ids []uint64
		err := db.Table("statuses s").
			Select("s.id").
			Joins("LEFT JOIN status_contents c ON c.status_id = s.id").
			Where("s.id > ? AND s.deleted_at IS NULL AND (s.reblog_id IS NULL OR s.reblog_id = 0) AND c.status_id IS NULL", cursor).
			Order("s.id ASC").
			Limit(500).
			Pluck("s.id", &ids).Error
		if err != nil {
			return repaired, err
		}
		if len(ids) == 0 {
			break
		}
		cursor = ids[len(ids)-1]
		if dryRun {
			repaired += int64(len(ids))
			continue
		}
		rows := make([]StatusContent, 0, len(ids))
		for _, id := range ids {
			if id == 0 {
				continue
			}
			rows = append(rows, StatusContent{
				StatusId:      id,
				ContentFormat: "plain",
				Content:       "",
			})
		}
		if len(rows) == 0 {
			continue
		}
		if err := db.Session(&gorm.Session{SkipHooks: true}).
			Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "status_id"}}, DoNothing: true}).
			Create(&rows).Error; err != nil {
			return repaired, err
		}
		repaired += int64(len(rows))
	}
	return repaired, nil
}
