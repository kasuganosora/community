package newacctmod

import "time"

// ReviewJob is one async LLM (or heuristic) review of a local status posted
// during the new-account observation window.
type ReviewJob struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement"`
	StatusID     uint64     `gorm:"not null;uniqueIndex:idx_newacct_review_status"`
	AccountID    uint64     `gorm:"not null;index:idx_newacct_review_claim"`
	State        string     `gorm:"type:varchar(20);not null;default:'pending';index:idx_newacct_review_claim"`
	Attempts     int        `gorm:"not null;default:0"`
	LeaseToken   string     `gorm:"type:varchar(64);not null;default:''"`
	NextAttemptAt time.Time `gorm:"not null;index:idx_newacct_review_claim"`
	LastError    string     `gorm:"type:text"`
	Verdict      string     `gorm:"type:varchar(24);not null;default:''"` // pass|flag|suspend|""
	Reason       string     `gorm:"type:text"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (ReviewJob) TableName() string { return "new_account_review_jobs" }

// Appeal is the single appeal allowed for one auto-suspension of a local account.
type Appeal struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement"`
	AccountID       uint64     `gorm:"not null;uniqueIndex:idx_newacct_appeal_account_suspend,priority:1"`
	SuspensionKey   string     `gorm:"type:varchar(64);not null;uniqueIndex:idx_newacct_appeal_account_suspend,priority:2"`
	Body            string     `gorm:"type:text;not null"`
	StatusID        uint64     `gorm:"not null;default:0"`
	State           string     `gorm:"type:varchar(20);not null;default:'open';index"` // open|accepted|rejected
	AdminNote       string     `gorm:"type:text"`
	ResolvedByID    uint64     `gorm:"not null;default:0"`
	ResolvedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Appeal) TableName() string { return "new_account_appeals" }

// Models lists tables owned by this package (for migrations).
func Models() []any { return []any{&ReviewJob{}, &Appeal{}} }
