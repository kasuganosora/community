package datatransfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ImportReceipt is an immutable source-to-target mapping, shared by retries and
// later jobs in the same authority namespace. Source identifiers never become
// database primary keys. The digest prevents silently replacing an old import.
type ImportReceipt struct {
	ID         uint64 `gorm:"primaryKey"`
	Namespace  string `gorm:"size:64;not null;uniqueIndex:idx_import_identity,priority:1"`
	Kind       string `gorm:"size:80;not null;uniqueIndex:idx_import_identity,priority:2"`
	SourceHash string `gorm:"size:64;not null;uniqueIndex:idx_import_identity,priority:3"`
	SourceID   string `gorm:"type:text;not null"`
	Digest     string `gorm:"size:64;not null"`
	TargetID   uint64
	Archived   bool
	JobID      string `gorm:"size:64"`
	CreatedAt  time.Time
}

func (ImportReceipt) TableName() string { return "data_transfer_receipts" }

// StoredPreservedRecord is private archival data. It is never a live status,
// credential, or permission grant, and must only be read through owner checks.
type StoredPreservedRecord struct {
	Scope          string          `gorm:"size:16;index;not null" json:"-"`
	ID             uint64          `gorm:"primaryKey"`
	Namespace      string          `gorm:"size:64;index;not null"`
	OwnerUserID    uint64          `gorm:"index;not null"`
	OwnerAccountID uint64          `gorm:"index;not null"`
	Kind           string          `gorm:"type:text;not null"`
	SourceID       string          `gorm:"type:text;not null"`
	Data           json.RawMessage `gorm:"type:longblob;not null"`
	CreatedAt      time.Time
}

func (StoredPreservedRecord) TableName() string { return "data_transfer_preserved_records" }

func digestValue(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	return digestBytes(b), nil
}
func digestBytes(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

func (i *importer) apply(kind, id string, value any, fn func(*gorm.DB) (uint64, error)) (uint64, error) {
	digest, err := digestValue(value)
	if err != nil {
		return 0, err
	}
	receipt := ImportReceipt{Namespace: i.namespace, Kind: kind, SourceHash: digestBytes([]byte(id)), SourceID: id, Digest: digest, JobID: i.options.JobID, Archived: strings.HasPrefix(kind, "archive:")}
	result := i.tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		var prior ImportReceipt
		if err = i.tx.Where("namespace = ? AND kind = ? AND source_hash = ?", i.namespace, kind, receipt.SourceHash).First(&prior).Error; err != nil {
			return 0, err
		}
		if prior.Digest != digest || prior.SourceID != id {
			return 0, fmt.Errorf("import conflict for %s source identifier", kind)
		}
		if prior.TargetID == 0 {
			return 0, errors.New("incomplete import receipt")
		}
		i.remember(kind, id, prior.TargetID)
		reportKind := kind
		if strings.HasPrefix(kind, "archive:") {
			reportKind = "archived_record"
		}
		i.report.Skipped[reportKind]++
		return prior.TargetID, nil
	}
	target, err := fn(i.tx)
	if err != nil {
		return 0, err
	}
	if target == 0 {
		return 0, errors.New("import did not produce a target")
	}
	if err = i.tx.Model(&receipt).UpdateColumn("target_id", target).Error; err != nil {
		return 0, err
	}
	i.remember(kind, id, target)
	if !strings.HasPrefix(kind, "archive:") {
		i.report.Created[kind]++
	}
	return target, nil
}
func (i *importer) remember(kind, id string, target uint64) {
	if i.ids[kind] == nil {
		i.ids[kind] = map[string]uint64{}
	}
	i.ids[kind][id] = target
}
func (i *importer) ref(kind, id string) (uint64, error) {
	if value := i.ids[kind][id]; value != 0 {
		return value, nil
	}
	return 0, fmt.Errorf("unresolved %s reference", kind)
}
func (i *importer) preserve(kind, id string, value any) error {
	_, err := i.apply("archive:"+digestBytes([]byte(kind)), id, value, func(tx *gorm.DB) (uint64, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return 0, err
		}
		record := StoredPreservedRecord{Scope: i.options.Scope, Namespace: i.namespace, OwnerUserID: i.options.OwnerUserID, OwnerAccountID: i.options.OwnerAccountID, Kind: kind, SourceID: id, Data: data}
		if err = tx.Create(&record).Error; err != nil {
			return 0, err
		}
		bucket := kind
		switch kind {
		case "post", "account", "relation", "media", "media_metadata", "emoji", "remote_key", "poll", "poll_vote", "list", "exclusive_list", "reaction", "bookmark":
		default:
			bucket = "record"
		}
		i.report.Archived[bucket]++
		return record.ID, nil
	})
	return err
}
