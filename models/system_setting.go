package models

type SystemSetting struct {
	ID  int64  `gorm:"primary_key;autoIncrement"           json:"id"`
	Key string `gorm:"unique;not null;type:varchar(64)"    json:"key"`
	Val any    `gorm:"type:json;not null; serializer:json" json:"val"`
}

func (SystemSetting) TableName() string {
	return "system_settings"
}
