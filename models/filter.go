package models

import (
	"crypto/md5" //nolint:gosec // G501: reviewed and safe
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"strconv"
	"strings"
	"time"
)

const (
	FilterActionNone = iota // 无操作
	FilterActionWarn        // 隐藏时显示警告信息
	FilterActionHide        // 完全隐藏
)

type Filter struct {
	Id           uint64           `gorm:"primary_key;auto_increment"         json:"id"`
	AccountId    uint64           `gorm:"not null;index"                     json:"account_id"` // 账户ID, 0为全局过滤器
	Title        string           `gorm:"not null"                           json:"title"`
	ExpiresAt    *time.Time       `gorm:"not null"                           json:"expires_at"`
	Context      []string         `gorm:"not null;type:json;serializer:json" json:"context"`
	FilterAction uint8            `gorm:"not null;"                          json:"filter_action"`
	Keywords     []*FilterKeyword `gorm:"not null;type:json;serializer:json" json:"keywords"`
	CreatedAt    *time.Time       `gorm:"not null;autoCreateTime"            json:"created_at"`
	UpdatedAt    *time.Time       `gorm:"not null;autoUpdateTime"            json:"updated_at"`
}

func (f *Filter) TableName() string {
	return "filters"
}

func (f *Filter) GetFilterActionString() string {
	switch f.FilterAction {
	case FilterActionNone:
		return "none" //nolint:goconst
	case FilterActionWarn:
		return "warn"
	case FilterActionHide:
		return "hide"
	default:
		return "none"
	}
}

func (f *Filter) GetStatusFilterAction(status *Status) (filterAction uint8) {
	if f.FilterAction == FilterActionNone {
		return FilterActionNone
	}

	for _, keyword := range f.Keywords {
		if keyword.WholeWord {
			if keyword.Keyword == status.Content.Content {
				return f.FilterAction
			}
		} else {
			if strings.Contains(status.Content.Content+status.Content.SpoilerText, keyword.Keyword) {
				return f.FilterAction
			}
		}
	}

	return FilterActionNone
}

func GetFilters(dbConn *gorm.DB, accountId uint64) (filters Filters, err error) {
	filters = make([]*Filter, 0)
	err = dbConn.Where("account_id = ? or account_id = 0", accountId).Find(&filters).Error

	return
}

type Filters []*Filter

func (ff Filters) GetStatusFilters(status *Status, account *Account) (filters Filters) {
	filters = make([]*Filter, 0)

	for _, filter := range ff {
		if account != nil && status.AccountId == account.Id {
			continue // 自己的状态不过滤
		}

		if filter.GetStatusFilterAction(status) != FilterActionNone {
			filters = append(filters, filter)
		}
	}

	return filters
}

func (ff Filters) GetFilterActions() (filterActions []uint8) {
	filterActions = make([]uint8, 0)

	for _, filter := range ff {
		if filter.FilterAction != FilterActionNone {
			filterActions = append(filterActions, filter.FilterAction)
		}
	}

	return filterActions
}

func (ff Filters) GetFilterActionsName() (filterActionsName []string) {
	filterActionsName = make([]string, 0)

	for _, filter := range ff {
		if filter.FilterAction != FilterActionNone {
			filterActionsName = append(filterActionsName, filter.GetFilterActionString())
		}
	}

	return filterActionsName
}

func (ff Filters) GetHeightLevelFilter() *Filter {
	lastActionLevel := uint8(0)

	var lastFilter *Filter

	for _, filter := range ff {
		if filter.FilterAction <= lastActionLevel {
			continue
		}

		lastActionLevel = filter.FilterAction
		lastFilter = filter
	}

	return lastFilter
}

type FilterKeyword struct {
	Uuid      string `json:"-"`
	Keyword   string `json:"keyword"`
	WholeWord bool   `json:"whole_word"`
}

func (fw *FilterKeyword) Hash() string {
	rawStrs := []string{fw.Keyword, strconv.FormatBool(fw.WholeWord)}

	return utils.Hash(md5.New(), strings.Join(rawStrs, "-")) //nolint:gosec // G401: reviewed and safe
}
