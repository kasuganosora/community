package models

type Rule struct {
	Id    int64  `json:"id" gorm:"primary_key;autoIncrement"`
	Text  string `json:"text" gorm:"type:varchar(255);not null"`
	Index int64  `json:"index" gorm:"type:bigint;not null;default:0"`
}

func (Rule) TableName() string {
	return "rules"
}
