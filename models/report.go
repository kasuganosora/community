package models

import (
	"github.com/kasuganosora/community/event"
	"gorm.io/gorm"
	"time"
)

const (
	ReportTypeHarassment = iota
	ReportTypePorn
	ReportTypeInfringement
	ReportTypeThreat
	ReportTypePoliticalSensitivity
	ReportTypeOther
)

const (
	ReportTargetTypeStatus = iota
	ReportTargetTypeAccount
)

const (
	ReportStatusPending = iota
	ReportStatusProcessed
	ReportStatusIgnored
)

type Report struct {
	Id               uint64         `gorm:"primaryKey;autoIncrement"`
	AccountId        uint64         `gorm:"index;not null"`
	StatusIds        []uint64       `gorm:"type:json;serializer:json"`
	ReportAccountIds []uint64       `gorm:"type:json;serializer:json"`
	ReportTargetType uint16         `gorm:"not null"`       // 0: status, 1: account
	ReportType       uint16         `gorm:"not null;index"` // 0: 骚扰, 1: 色情, 2: 侵权, 3: 威胁， 4: 政治敏感， 5: 其他
	ReportContent    string         `gorm:"type:text"`
	ResultComment    string         `gorm:"type:text"`
	Status           uint16         `gorm:"not null;index"` // 0: 未处理, 1: 已处理, 2: 已忽略
	ClosedAt         *time.Time     `gorm:"default:false"`
	CreatedAt        *time.Time     `gorm:"autoCreateTime"`
	UpdatedAt        *time.Time     `gorm:"autoUpdateTime"`
	DeletedAt        gorm.DeletedAt `gorm:"index"`
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
	if r.Status == ReportStatusProcessed {
		now := time.Now()
		r.ClosedAt = &now
	}
	event.Trigger("report", event.Event{
		Name: `before_save`,
		Data: r,
	})

	return
}

func (r *Report) GetStatusString() string {
	switch r.Status {
	case ReportStatusPending:
		return "Pending"
	case ReportStatusProcessed:
		return "Processed"
	case ReportStatusIgnored:
		return "Ignored"
	default:
		return "Unknown"
	}
}

type Reports []*Report
