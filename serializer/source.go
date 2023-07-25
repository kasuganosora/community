package serializer

import (
	"github.com/kasuganosora/community/models"
	"strconv"
)

type Source struct {
	Id          string `json:"id"`
	Text        string `json:"text"`
	SpoilerText string `json:"spoiler_text"`
}

func ToSource(sc *models.StatusContent) (source *Source) {
	source = &Source{
		Id:          strconv.FormatUint(sc.StatusId, 10),
		Text:        sc.Content,
		SpoilerText: sc.SpoilerText,
	}
	return
}
