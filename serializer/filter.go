package serializer

import (
	"github.com/kasuganosora/community/models"
	"strconv"
	"time"
)

type Filter struct {
	Id           string           `json:"id"`
	Title        string           `json:"title"`
	Context      []string         `json:"context"`
	ExpiresAt    *time.Time       `json:"expires_at"`
	FilterAction string           `json:"filter_action"`
	Keywords     []*FilterKeyword `json:"keywords"`
	Statuses     []*Status        `json:"statuses"`
}

func ToFilter(filter *models.Filter) *Filter {
	return &Filter{
		Id:           strconv.FormatUint(filter.Id, 10),
		Title:        filter.Title,
		Context:      filter.Context,
		ExpiresAt:    filter.ExpiresAt,
		FilterAction: filter.GetFilterActionString(),
		Keywords:     ToFilterKeywords(filter),
	}
}

type FilterKeyword struct {
	Id string `json:"id"`
	models.FilterKeyword
}

func ToFilterKeywords(filter *models.Filter) []*FilterKeyword {
	keywords := make([]*FilterKeyword, 0, len(filter.Keywords))
	for _, keyword := range filter.Keywords {
		keywords = append(keywords, ToFilterKeyword(filter, keyword))
	}

	return keywords
}

func ToFilterKeyword(filter *models.Filter, keyword *models.FilterKeyword) *FilterKeyword {
	return &FilterKeyword{
		Id:            strconv.FormatUint(filter.Id, 10) + "-" + keyword.Uuid,
		FilterKeyword: *keyword,
	}
}
