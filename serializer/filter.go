package serializer

import (
	"github.com/kasuganosora/community/models"
	"strconv"
)

type FilterSummary struct {
	Id           string   `json:"id"`
	Title        string   `json:"title"`
	Context      []string `json:"context"`
	ExpiresAt    *APITime `json:"expires_at"`
	FilterAction string   `json:"filter_action"`
}

type Filter struct {
	FilterSummary
	Keywords []*FilterKeyword `json:"keywords"`
	Statuses []FilterStatus   `json:"statuses"`
}

func ToFilter(filter *models.Filter) *Filter {
	return &Filter{
		FilterSummary: toFilterSummary(filter),
		Keywords:      ToFilterKeywords(filter),
		Statuses:      []FilterStatus{},
	}
}

func toFilterSummary(filter *models.Filter) FilterSummary {
	ctx := filter.Context
	if ctx == nil {
		ctx = []string{}
	}

	return FilterSummary{
		Id:           strconv.FormatUint(filter.Id, 10),
		Title:        filter.Title,
		Context:      ctx,
		ExpiresAt:    NewAPITime(filter.ExpiresAt),
		FilterAction: filter.GetFilterActionString(),
	}
}

type FilterKeyword struct {
	Id        string `json:"id"`
	Keyword   string `json:"keyword"`
	WholeWord bool   `json:"whole_word"`
}

func ToFilterKeywords(filter *models.Filter) []*FilterKeyword {
	keywords := make([]*FilterKeyword, 0, len(filter.Keywords))
	for _, keyword := range filter.Keywords {
		if keyword != nil {
			keywords = append(keywords, ToFilterKeyword(filter, keyword))
		}
	}

	return keywords
}

func ToFilterKeyword(filter *models.Filter, keyword *models.FilterKeyword) *FilterKeyword {
	return &FilterKeyword{
		Id:        strconv.FormatUint(filter.Id, 10) + "-" + keyword.KeyID(),
		Keyword:   keyword.Keyword,
		WholeWord: keyword.WholeWord,
	}
}

// FilterStatus is a v2 filter's status rule.
type FilterStatus struct {
	Id       string `json:"id"`
	StatusId string `json:"status_id"`
}

// FilterResult is the entry type of Status#filtered (Mastodon FilterResult).
type FilterResult struct {
	Filter         FilterSummary `json:"filter"`
	KeywordMatches []string      `json:"keyword_matches"`
	StatusMatches  []string      `json:"status_matches"`
}

// ToFilterResults converts matched filters (action warn/hide) to the
// Mastodon FilterResult array; never nil.
func ToFilterResults(filters models.Filters, status *models.Status, viewer *models.Account) []FilterResult {
	out := make([]FilterResult, 0, len(filters))

	for _, f := range filters {
		if f == nil || f.FilterAction == models.FilterActionNone {
			continue
		}

		out = append(out, FilterResult{Filter: toFilterSummary(f), KeywordMatches: f.MatchedKeywords(status, viewer)})
	}

	return out
}
