package models

import (
	"github.com/kasuganosora/community/event"
	"gorm.io/gorm"
	"time"
)

const (
	ReportCategoryOther = iota
	ReportCategorySpam
	ReportCategoryLegal
	ReportCategoryViolation
)

type Report struct {
	Id                     uint64         `gorm:"primaryKey;autoIncrement"`
	StatusIds              []uint64       `gorm:"type:json;serializer:json"`
	Comment                string         `gorm:"type:text"`
	AccountId              uint64         `gorm:"index"`
	ActionTakenByAccountId uint64         `gorm:"index"`
	TargetAccountId        uint64         `gorm:"index"`
	AssignedAccountId      uint64         `gorm:"index"`
	Forwarded              bool           `gorm:"default:false"`
	Forward                bool           `gorm:"default:false"`
	Category               uint16         `gorm:"default:0"`
	RuleIds                []uint64       `gorm:"type:json;serializer:json"`
	ActionTakenAt          *time.Time     `gorm:"index"`
	Uri                    string         `gorm:"type:varchar(255)"`
	CreatedAt              *time.Time     `gorm:"autoCreateTime"`
	UpdatedAt              *time.Time     `gorm:"autoUpdateTime"`
	DeletedAt              gorm.DeletedAt `gorm:"index"`

	Account              *Account `gorm:"foreignKey:AccountId"`
	TargetAccount        *Account `gorm:"foreignKey:TargetAccountId"`
	ActionTakenByAccount *Account `gorm:"foreignKey:ActionTakenByAccountId"`
	AssignedAccount      *Account `gorm:"foreignKey:AssignedAccountId"`
}

func (r *Report) TableName() string {
	return "reports"
}

func (r *Report) BeforeCreate(tx *gorm.DB) (err error) {
	event.Trigger("report", event.Event{
		Name: `before_create`,
		Data: r,
	})

	return
}

func (r *Report) AfterCreate(tx *gorm.DB) (err error) {
	event.Trigger("report", event.Event{
		Name: `after_create`,
		Data: r,
	})

	return
}

func (r *Report) BeforeUpdate(tx *gorm.DB) (err error) {
	event.Trigger("report", event.Event{
		Name: `before_update`,
		Data: r,
	})

	return
}

func (r *Report) AfterUpdate(tx *gorm.DB) (err error) {
	event.Trigger("report", event.Event{
		Name: `after_update`,
		Data: r,
	})

	return
}

func (r *Report) BeforeDelete(tx *gorm.DB) (err error) {
	event.Trigger("report", event.Event{
		Name: `before_delete`,
		Data: r,
	})

	return
}

func (r *Report) AfterDelete(tx *gorm.DB) (err error) {
	event.Trigger("report", event.Event{
		Name: `after_delete`,
		Data: r,
	})

	return
}

func (r *Report) BeforeSave(tx *gorm.DB) (err error) {
	event.Trigger("report", event.Event{
		Name: `before_save`,
		Data: r,
	})

	return
}

func (r *Report) GetStatusString() string {
	if r.ActionTakenAt != nil {
		return "Resolved"
	}

	return "Unresolved"
}

func (r *Report) ToCategoryString() string {
	switch r.Category {
	case ReportCategoryOther:
		return "Other"
	case ReportCategorySpam:
		return "Spam"
	case ReportCategoryLegal:
		return "Legal"
	case ReportCategoryViolation:
		return "Violation"
	default:
		return "Unknown"
	}
}

func (r *Report) SetCategory(category string) {
	switch category {
	case "Other":
		r.Category = ReportCategoryOther
	case "Spam":
		r.Category = ReportCategorySpam
	case "Legal":
		r.Category = ReportCategoryLegal
	case "Violation":
		r.Category = ReportCategoryViolation
	default:
		r.Category = ReportCategoryOther
	}
}

type Reports []*Report
