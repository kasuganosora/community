package models

import (
	"time"
)

// DomainBlock represents an instance-level domain block (admin setting).
type DomainBlock struct {
	Id             uint64     `gorm:"primary_key;auto_increment"    json:"id"`
	Domain         string     `gorm:"not null;uniqueIndex;size:255" json:"domain"`
	Severity       string     `gorm:"default:'suspend';size:32"     json:"severity"` // suspend, silence, noop
	RejectMedia    bool       `gorm:"default:true"                  json:"reject_media"`
	RejectReports  bool       `gorm:"default:true"                  json:"reject_reports"`
	PrivateComment string     `gorm:"type:text"                     json:"private_comment"`
	PublicComment  string     `gorm:"type:text"                     json:"public_comment"`
	Obfuscate      bool       `gorm:"default:false"                 json:"obfuscate"`
	CreatedAt      *time.Time `gorm:"autoCreateTime"                json:"created_at"`
	UpdatedAt      *time.Time `gorm:"autoUpdateTime"                json:"updated_at"`
}

func (DomainBlock) TableName() string {
	return "domain_blocks"
}

type DomainBlocks []*DomainBlock
