package models

import "time"

// MediaProcessTask is the durable record for one local upload that did not
// finish inside the request. ConfigJSON is frozen when the task is created.
type MediaProcessTask struct {
	ID             uint64     `gorm:"primaryKey"                         json:"id"`
	MediaID        uint64     `gorm:"uniqueIndex;not null"               json:"media_id"`
	AccountID      uint64     `gorm:"index;not null"                     json:"account_id"`
	Kind           string     `gorm:"size:16;not null"                   json:"kind"`
	DetectedMIME   string     `gorm:"size:128;not null"                  json:"-"`
	State          string     `gorm:"size:16;not null;index"             json:"state"`
	Progress       int        `gorm:"not null;default:0"                 json:"progress"`
	ErrorText      string     `gorm:"size:200"                           json:"error,omitempty"`
	StagingPath    string     `gorm:"size:500"                           json:"-"`
	OutputKey      string     `gorm:"size:500"                           json:"-"`
	ClaimToken     string     `gorm:"size:64"                            json:"-"`
	ClaimExpiresAt *time.Time `json:"-"`
	ConfigJSON     string     `gorm:"type:text"                          json:"-"`
	InputBytes     int64      `json:"-"`
	OutputBytes    int64      `json:"-"`
	DurationMs     int64      `json:"-"`
	CreatedAt      time.Time  `gorm:"autoCreateTime"                     json:"-"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime"                     json:"-"`
}

func (MediaProcessTask) TableName() string { return "media_process_tasks" }

// MediaQueueGate is a single row locked during video admission. It exists
// even when the task table is empty, so the lock is not a lock on task rows.
type MediaQueueGate struct {
	Name       string `gorm:"primaryKey;size:32"`
	LockSerial int64  `gorm:"not null;default:0"`
}

func (MediaQueueGate) TableName() string { return "media_queue_gates" }
