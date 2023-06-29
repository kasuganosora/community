package models

type SystemSetting struct {
	ID  int64  `json:"id" gorm:"primary_key;autoIncrement"`
	Key string `json:"key" gorm:"unique;not null;type:varchar(64)"`
	Val any    `json:"val" gorm:"type:json;not null; serializer:json"`
}

func (SystemSetting) TableName() string {
	return "system_settings"
}
