package serializer

import (
	"github.com/kasuganosora/community/models"
	"strconv"
)

type List struct {
	Id            string `json:"id"`
	Title         string `json:"title"`
	RepliesPolicy string `json:"replies_policy"`
}

func ToList(list *models.List) *List {
	return &List{
		Id:            strconv.FormatUint(list.Id, 10),
		Title:         list.Title,
		RepliesPolicy: list.GetRepliesPolicy(),
	}
}

func ToLists(lists []*models.List) []*List {
	result := make([]*List, 0, len(lists))

	for _, list := range lists {
		result = append(result, ToList(list))
	}

	return result
}
