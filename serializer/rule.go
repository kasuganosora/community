package serializer

import (
	"github.com/kasuganosora/community/models"
	"strconv"
)

type Rule struct {
	Id    string `json:"id"`
	Text  string `json:"text"`
	intId uint64
}

func ToRule(r *models.Rule) *Rule {
	return &Rule{
		Id:    strconv.FormatUint(r.Id, 10),
		Text:  r.Text,
		intId: r.Id,
	}
}

func ToRules(rs []*models.Rule) []*Rule {
	ret := make([]*Rule, 0, len(rs))
	for _, r := range rs {
		ret = append(ret, ToRule(r))
	}

	return ret
}
