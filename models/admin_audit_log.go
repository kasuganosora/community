package models

import "time"

// AdminAuditLog deliberately excludes request bodies, URLs, IPs and credentials.
// Pending means execution or its outcome could not be confirmed (including crashes).
type AdminAuditLog struct {
	Id        uint64 `gorm:"primaryKey;autoIncrement" json:"id,string"`
	ActorID   uint64 `gorm:"index;not null" json:"actor_id,string"`
	AccountID uint64 `gorm:"index;not null" json:"account_id,string"`
	Action    string `gorm:"size:255;index;not null" json:"action"`
	Method    string `gorm:"size:10;not null" json:"method"`
	Route     string `gorm:"size:220;not null" json:"route"`
	TargetID  string `gorm:"size:32" json:"target_id"`
	// Details is populated only from explicitly allowlisted administrative
	// fields, never by copying a request body or credentials.
	Details     map[string]string `gorm:"serializer:json;type:text" json:"details,omitempty"`
	Outcome     string            `gorm:"size:16;index;not null" json:"outcome"`
	Status      int               `json:"status"`
	DurationMS  int64             `json:"duration_ms"`
	CreatedAt   time.Time         `gorm:"index;not null" json:"created_at"`
	CompletedAt *time.Time        `json:"completed_at"`
}
