package models

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: reviewed and safe
	"strings"
	"time"
	"unicode"

	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"strconv"
)

const (
	FilterActionNone = iota // 无操作
	FilterActionWarn        // 隐藏时显示警告信息
	FilterActionHide        // 完全隐藏
)

type Filter struct {
	Id           uint64           `gorm:"primary_key;autoIncrement"         json:"id"`
	AccountId    uint64           `gorm:"not null;index"                     json:"account_id"` // 账户ID, 0为全局过滤器
	Title        string           `gorm:"not null"                           json:"title"`
	ExpiresAt    *time.Time       `json:"expires_at"`
	Context      []string         `gorm:"not null;type:json;serializer:json" json:"context"`
	FilterAction uint8            `gorm:"type:tinyint;not null;"                          json:"filter_action"`
	Keywords     []*FilterKeyword `gorm:"not null;type:json;serializer:json" json:"keywords"`
	CreatedAt    *time.Time       `gorm:"not null;autoCreateTime"            json:"created_at"`
	UpdatedAt    *time.Time       `gorm:"not null;autoUpdateTime"            json:"updated_at"`
}

func ParseFilterAction(raw string) (uint8, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "warn":
		return FilterActionWarn, true
	case "hide":
		return FilterActionHide, true
	default:
		return FilterActionNone, false
	}
}

func ValidFilterContexts(contexts []string) bool {
	if len(contexts) == 0 {
		return false
	}
	for _, name := range contexts {
		switch name {
		case "home", "notifications", "public", "thread", "account":
		default:
			return false
		}
	}
	return true
}

type filterContextKey struct{}

func WithFilterContext(ctx context.Context, name string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, filterContextKey{}, name)
}

func FilterContextOf(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(filterContextKey{}).(string)
	return name
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
	if f == nil || status == nil || f.FilterAction == FilterActionNone {
		return FilterActionNone
	}

	if keywordsMatch(f.Keywords, status.Content.Content, status.Content.SpoilerText) {
		return f.FilterAction
	}

	return FilterActionNone
}

func keywordsMatch(keywords []*FilterKeyword, content, spoiler string) bool {
	for _, keyword := range keywords {
		if keyword == nil {
			continue
		}
		if keywordMatches(keyword, content, spoiler) {
			return true
		}
	}
	return false
}

func keywordMatches(keyword *FilterKeyword, content, spoiler string) bool {
	needle := strings.ToLower(strings.TrimSpace(keyword.Keyword))
	if needle == "" {
		return false
	}
	for _, text := range []string{content, spoiler} {
		hay := strings.ToLower(text)
		if hay == "" {
			continue
		}
		if keyword.WholeWord {
			if containsWholeWord(hay, needle) {
				return true
			}
			continue
		}
		if strings.Contains(hay, needle) {
			return true
		}
	}
	return false
}

func containsWholeWord(hay, needle string) bool {
	hayRunes := []rune(hay)
	needleRunes := []rune(needle)
	if len(needleRunes) == 0 || len(hayRunes) < len(needleRunes) {
		return false
	}
	limit := len(hayRunes) - len(needleRunes)
	for i := 0; i <= limit; i++ {
		if string(hayRunes[i:i+len(needleRunes)]) != string(needleRunes) {
			continue
		}
		beforeOK := i == 0 || !isWordRune(hayRunes[i-1])
		after := i + len(needleRunes)
		afterOK := after == len(hayRunes) || !isWordRune(hayRunes[after])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (f *Filter) expired() bool {
	return f.ExpiresAt != nil && f.ExpiresAt.Before(time.Now())
}

func contextAllows(contexts []string, name string) bool {
	if name == "" || len(contexts) == 0 {
		return true
	}
	for _, item := range contexts {
		if item == name {
			return true
		}
	}
	return false
}

func GetFilters(dbConn *gorm.DB, accountId uint64) (filters Filters, err error) {
	filters = make([]*Filter, 0)
	err = dbConn.Where("account_id = ? or account_id = 0", accountId).Find(&filters).Error

	return
}

type Filters []*Filter

func (ff Filters) GetStatusFilters(status *Status, account *Account) (filters Filters) {
	return ff.GetStatusFiltersIn(status, account, "")
}

func (ff Filters) GetStatusFiltersIn(status *Status, account *Account, contextName string) (filters Filters) {
	filters = make([]*Filter, 0)
	if status == nil {
		return filters
	}

	own := account != nil && status.AccountId == account.Id
	for _, filter := range ff {
		if filter == nil || !contextAllows(filter.Context, contextName) || filter.expired() {
			continue
		}
		hit := !own && filter.GetStatusFilterAction(status) != FilterActionNone
		if !hit && status.Reblog != nil && (account == nil || status.Reblog.AccountId != account.Id) {
			hit = filter.GetStatusFilterAction(status.Reblog) != FilterActionNone
		}
		if hit {
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
