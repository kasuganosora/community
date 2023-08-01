package models

// UserSetting 用户设置
type UserSetting struct {
	ID     string         `json:"id" gorm:"primary_key"`
	UserID int64          `json:"user_id" gorm:"unique;not null"`
	Data   map[string]any `json:"data" gorm:"type:json;serializer:json"`
}

// TableName 设置表名
func (UserSetting) TableName() string {
	return "user_setting"
}

func (us *UserSetting) GetaggrAgatesReblogs() bool {
	if _aggregatesReblogs, ok := us.Data["aggregates_reblogs"]; ok {
		return _aggregatesReblogs.(bool)
	}
	return true
}
