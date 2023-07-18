package models

type Follow struct {
	Id         uint64         `json:"id" gorm:"primary_key;type:bigint(20);autoIncrement"`
	AccountId  uint64         `json:"account_id" gorm:"not null;type:bigint(20);index"`
	FollowId   uint64         `json:"follow_id" gorm:"not null;type:bigint(20);index"`
	Properties map[string]any `json:"properties" gorm:"type:json;serializer:json"`
	CreatedAt  int64          `json:"created_at" gorm:"type:timestamp;autoCreateTime"`
}

func (Follow) TableName() string {
	return "follows"
}
