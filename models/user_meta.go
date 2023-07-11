package models

import (
	"encoding/json"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

type UserMeta struct {
	Id     int64  `json:"id" gorm:"primary_key;autoIncrement"`
	UserId int64  `json:"user_id" gorm:"not null; index:user_key"`
	Key    string `json:"key" gorm:"type:varchar(64);not null; index:user_key"`
	Value  string `json:"value" gorm:"type:text;not null"`
}

// TableName 设置表名
func (UserMeta) TableName() string {
	return "user_meta"
}

func SaveUserMeta(db *gorm.DB, userId int64, key string, value any) (err error) {
	meta := &UserMeta{}

	err = db.Where("user_id = ? AND key = ?", userId, key).First(meta).Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		err = errors.Wrap(err, "SaveUserMeta")
		return
	}

	// 需要删除，但是没有找到了，所以直接返回
	if errors.Is(err, gorm.ErrRecordNotFound) && value == nil {
		return
	}

	if err == nil && value == nil {
		err = db.Delete(meta).Error
		if err != nil {
			err = errors.Wrap(err, "SaveUserMeta")
		}
		return
	}

	meta.Key = key

	jsonBytes, err := json.Marshal(value)

	if err != nil {
		err = errors.Wrap(err, "SaveUserMeta")
		return
	}

	meta.Value = string(jsonBytes)
	meta.UserId = userId

	if meta.Id > 0 {
		err = db.Save(meta).Error
	} else {
		err = db.Create(meta).Error
	}

	if err != nil {
		err = errors.Wrap(err, "SaveUserMeta")
	}

	return
}

func GetUserMeta(db *gorm.DB, userId int64, key string, outValue any) (err error) {
	meta := &UserMeta{}

	err = db.Where("user_id = ? AND key = ?", userId, key).First(meta).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = errors.Errorf("user meta not found: %s", key)
			return
		}
		err = errors.Wrap(err, "SaveUserMeta")
		return
	}

	if len(meta.Value) == 0 {
		return
	}

	err = json.Unmarshal([]byte(meta.Value), outValue)

	if err != nil {
		err = errors.Wrap(err, "SaveUserMeta")
		return
	}

	return
}
