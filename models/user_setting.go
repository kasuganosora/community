package models

// UserSetting 用户设置.
type UserSetting struct { //nolint:recvcheck
	ID     string         `gorm:"primary_key"               json:"id"`
	UserID int64          `gorm:"unique;not null"           json:"user_id"`
	Data   map[string]any `gorm:"type:json;serializer:json" json:"data"`
}

// TableName 设置表名.
func (UserSetting) TableName() string {
	return "user_setting"
}

func (us *UserSetting) GetAggregatesReblogs() bool {
	if _aggregatesReblogs, ok := us.Data["aggregates_reblogs"]; ok {
		if v, ok := _aggregatesReblogs.(bool); ok {
			return v
		}
	}

	return true
}

func (us *UserSetting) IsFollowProtect() bool {
	// Users created through registration have no user_setting row, so
	// Settings is nil; treat that as "not protected".
	if us == nil {
		return false
	}

	if _followProtect, ok := us.Data["follow_protect"]; ok {
		if v, ok := _followProtect.(bool); ok {
			return v
		}
	}

	return false
}
