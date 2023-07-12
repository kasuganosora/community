package models

type Follow struct {
	Id         int64          `json:"id" gorm:"primary_key;type:bigint(20);"`
	AccountId  string         `json:"account_id" gorm:"not null;type:bigint(20);index"`
	FollowId   string         `json:"follow_id" gorm:"not null;type:bigint(20);index"`
	Properties map[string]any `json:"properties" gorm:"type:json;serializer:json"`
	CreatedAt  int64          `json:"created_at" gorm:"type:timestamp;autoCreateTime"`
}

func (Follow) TableName() string {
	return "follows"
}
