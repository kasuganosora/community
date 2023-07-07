package models

import "time"

type Field struct {
	ID         int64      `json:"id" gorm:"primary_key;autoIncrement"`
	AccountId  int64      `json:"-" gorm:"not null;type:bigint(20);index"`
	Name       string     `json:"name" gorm:"type:varchar(255);not null"`
	Value      string     `json:"value" gorm:"type:text;not null"`
	VerifiedAt *time.Time `json:"verified_at" gor:"type:datetime"`
}

func (Field) TableName() string {
	return "fields"
}
