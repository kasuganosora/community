package models

import "time"

// InstanceMetadata is the persisted per-domain nodeinfo cache shared by the
// admin instance list and remote reaction software detection. It is filled
// only by background/bounded lookups; admin pages never trigger fetches.
type InstanceMetadata struct {
	Domain        string     `gorm:"type:varchar(255);primaryKey"   json:"domain"`
	Name          string     `gorm:"type:varchar(255);default:''"   json:"name"`
	Software      string     `gorm:"type:varchar(64);default:''"    json:"software"`
	Version       string     `gorm:"type:varchar(64);default:''"    json:"version"`
	IconURL       string     `gorm:"type:varchar(1024);default:''"  json:"icon_url"`
	FetchedAt     *time.Time `json:"fetched_at"`
	FailedAt      *time.Time `json:"failed_at"`
	FailCount     int        `gorm:"default:0"                      json:"fail_count"`
	NextAttemptAt *time.Time `gorm:"index"                          json:"next_attempt_at"`
	// SuspensionState is Misskey's instance suspensionState: "none" or
	// "manuallySuspended" (deliveries to the instance stop; inbound
	// activities are still accepted).
	SuspensionState string `gorm:"type:varchar(32);not null;default:'none'" json:"suspension_state"`
}

const (
	InstanceSuspensionNone   = "none"
	InstanceSuspensionManual = "manuallySuspended"
)

func (InstanceMetadata) TableName() string { return "instance_metadata" }
