package models

import (
	"encoding/json"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserMeta struct {
	Id     uint64 `gorm:"primary_key;autoIncrement"                 json:"id"`
	UserId uint64 `gorm:"not null; index:user_key"                  json:"user_id"`
	Key    string `gorm:"type:varchar(64);not null; index:user_key" json:"key"`
	Value  string `gorm:"type:text;not null"                        json:"value"`
}

// TableName 设置表名.
func (UserMeta) TableName() string {
	return "user_meta"
}

func SaveUserMeta(db *gorm.DB, userId uint64, key string, value any) (err error) {
	meta := &UserMeta{}

	err = db.Where("user_id = ?", userId).Where(clause.Eq{Column: "key", Value: key}).First(meta).Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		err = errors.Wrap(err, "SaveUserMeta")

		return err
	}

	// 需要删除，但是没有找到了，所以直接返回
	if errors.Is(err, gorm.ErrRecordNotFound) && value == nil {
		return err
	}

	if err == nil && value == nil {
		err = db.Delete(meta).Error
		if err != nil {
			err = errors.Wrap(err, "SaveUserMeta")
		}

		return err
	}

	meta.Key = key

	jsonBytes, err := json.Marshal(value)
	if err != nil {
		err = errors.Wrap(err, "SaveUserMeta")

		return err
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

	return err
}

func GetUserMeta(db *gorm.DB, userId uint64, key string, outValue any) (err error) {
	meta := &UserMeta{}

	err = db.Where("user_id = ?", userId).Where(clause.Eq{Column: "key", Value: key}).First(meta).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = errors.Errorf("user meta not found: %s", key)

			return
		}

		err = errors.Wrap(err, "GetUserMeta")

		return
	}

	if len(meta.Value) == 0 {
		return
	}

	err = json.Unmarshal([]byte(meta.Value), outValue)
	if err != nil {
		err = errors.Wrap(err, "GetUserMeta")

		return
	}

	return
}
