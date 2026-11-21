package models

// Endorsement represents an account endorsement.
type Endorsement struct {
	Id        uint64 `gorm:"primary_key;autoIncrement"                                                  json:"id"`
	AccountId uint64 `gorm:"not null;index;uniqueIndex:uidx_endorsements_account_target,priority:1" json:"account_id"`
	TargetId  uint64 `gorm:"not null;index;uniqueIndex:uidx_endorsements_account_target,priority:2" json:"target_id"`
}

func (e *Endorsement) TableName() string {
	return "endorsements"
}
