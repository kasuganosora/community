package serializer

import (
	"github.com/kasuganosora/community/models"
	"time"
)

type Field struct {
	Name       string     `json:"name"`
	Val        string     `json:"val"`
	VerifiedAt *time.Time `json:"verified_at"`
}

func ToFields(fields []models.AccountField) []Field {
	ret := make([]Field, 0)
	for _, v := range fields {
		ret = append(ret, Field{
			Name:       v.Name,
			Val:        v.Val,
			VerifiedAt: v.VerifiedAt,
		})
	}

	return ret
}
