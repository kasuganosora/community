package systemsettings

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"sort"
	"strings"
)

var settings = map[string]any{}
var loaded = false

func Load() {
	if loaded {
		return
	}

	rows := make([]models.SystemSetting, 0)
	err := db.GetDB().Model(&models.SystemSetting{}).Find(&rows).Error
	log.FatalIf(err)

	for _, row := range rows {
		settings[row.Key] = row.Val
	}

	loaded = true
}

func Get(key string, defaultValue any) any {
	Load()

	if v, ok := settings[key]; ok {
		return v
	}

	return defaultValue
}

func GetInt(key string, defaultValue any) int {
	v := Get(key, defaultValue)
	if v == nil {
		return 0
	}

	return v.(int)
}

func GetInt64(key string, defaultValue any) int64 {
	v := Get(key, defaultValue)
	if v == nil {
		return 0
	}

	return v.(int64)
}

func GetFloat64(key string, defaultValue any) float64 {
	v := Get(key, defaultValue)
	if v == nil {
		return 0
	}

	return v.(float64)
}

func GetBool(key string, defaultValue any) bool {
	v := Get(key, defaultValue)
	if v == nil {
		return false
	}

	return v.(bool)
}

func GetString(key string, defaultValue any) string {
	v := Get(key, defaultValue)
	if v == nil {
		return ""
	}

	return v.(string)
}

func GetStrings(key string, defaultValue any) []string {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	return v.([]string)
}

func GetMap(key string, defaultValue any) map[string]any {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	return v.(map[string]any)
}

func GetInts(key string, defaultValue any) []int {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	return v.([]int)
}

func GetInt64s(key string, defaultValue any) []int64 {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	return v.([]int64)
}

func Set(key string, val any) {
	settings[key] = val
	return
}

func SaveToDB(keys ...string) (err error) {
	rows := make([]models.SystemSetting, 0)
	err = db.GetDB().Model(&models.SystemSetting{}).Where("key in (?)", keys).Find(&rows).Error

	if err != nil {
		err = errors.Wrapf(err, "failed to get system settings from db, keys: %v", keys)
		return
	}

	existsKeyMap := make(map[string]bool)
	for _, row := range rows {
		existsKeyMap[row.Key] = true
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) (err error) {
		for _, key := range keys {
			if _, ok := existsKeyMap[key]; ok {
				err = tx.Model(&models.SystemSetting{}).Where("key = ?", key).Update("val", settings[key]).Error
			} else {
				err = tx.Model(&models.SystemSetting{}).Create(&models.SystemSetting{
					Key: key,
					Val: settings[key],
				}).Error
			}

			if err != nil {
				return
			}
		}

		return
	})

	if err != nil {
		err = errors.Wrapf(err, "failed to save system settings to db, keys: %v", keys)
		return
	}

	return
}

// GetAllFlatStyle 获取所有扁平化样式
// 例如： key a.b.c, val 1
// 返回： map[a.b.c]1
func GetAllFlatStyle() (settings map[string]any) {
	return settings
}

// GetAllTreeStyle 获取所有树形样式
// 例如： key a.b.c, val 1
// 返回： map[a][b][c]1
func GetAllTreeStyle() (settings map[string]any) {
	ret := make(map[string]any)
	keys := make([]string, 0)

	for key, _ := range settings {
		keys = append(keys, key)
	}

	// 把key 升序排序
	sort.Sort(sort.StringSlice(keys))

	for _, key := range keys {
		val := settings[key]
		keyParts := strings.Split(key, ".")
		keyLen := len(keyParts)

		for i := 0; i < keyLen; i++ {
			if i == keyLen-1 {
				ret[keyParts[i]] = val
				break
			}

			if _, ok := ret[keyParts[i]]; !ok {
				ret[keyParts[i]] = make(map[string]any)
			}

			ret = ret[keyParts[i]].(map[string]any)
		}

	}

	return
}
