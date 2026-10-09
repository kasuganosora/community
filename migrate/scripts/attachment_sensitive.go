package scripts

import (
	"gorm.io/gorm"
)

// AttachmentSensitiveScript adds media_attachments.sensitive, the per-file
// flag federated servers send. NOT NULL DEFAULT false, so it is safe (a
// metadata-only change on PostgreSQL 11+) on a populated table. Idempotent.
type AttachmentSensitiveScript struct{}

type attachmentSensitiveRow struct {
	Sensitive bool `gorm:"not null;default:false"`
}

func (attachmentSensitiveRow) TableName() string { return "media_attachments" }

func (AttachmentSensitiveScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("media_attachments") || m.HasColumn(&attachmentSensitiveRow{}, "sensitive") {
		return nil
	}

	return m.AddColumn(&attachmentSensitiveRow{}, "sensitive")
}

func (AttachmentSensitiveScript) Down(*gorm.DB) error { return nil }
