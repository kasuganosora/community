package serializer

import (
	"github.com/kasuganosora/community/models"
	"strconv"
	"time"
)

type Filter struct {
	Id           string                  `json:"id"`
	Title        string                  `json:"title"`
	Context      []string                `json:"context"`
	ExpiresAt    *time.Time              `json:"expires_at"`
	FilterAction string                  `json:"filter_action"`
	Keywords     []*models.FilterKeyword `json:"keywords"`
	Statuses     []*Status               `json:"statuses"`
}

func ToFilter(filter *models.Filter) *Filter {
	return &Filter{
		Id:           strconv.FormatUint(filter.Id, 10),
		Title:        filter.Title,
		Context:      filter.Context,
		ExpiresAt:    filter.ExpiresAt,
		FilterAction: filter.GetFilterActionString(),
		Keywords:     filter.Keywords,
	}
}
