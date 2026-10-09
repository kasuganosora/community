package serializer

import (
	"strings"
	"time"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
)

// Field is a Mastodon profile field: name is plain text, value is HTML.
type Field struct {
	Name       string     `json:"name"`
	Value      string     `json:"value"`
	VerifiedAt *time.Time `json:"verified_at"`
}

// ToFields returns the stored fields unchanged (local fields hold the text
// the user typed; remote ones hold sanitized HTML).
func ToFields(fields []models.AccountField) []Field {
	ret := make([]Field, 0, len(fields))
	for _, v := range fields {
		ret = append(ret, Field{
			Name:       v.Name,
			Value:      v.Val,
			VerifiedAt: v.VerifiedAt,
		})
	}

	return ret
}

// toAPIFields renders fields for the client API: local values are linkified
// plain text, remote values are re-sanitized HTML.
func toAPIFields(fields []models.AccountField, local bool, opts htmlsafe.RenderOptions) []Field {
	ret := make([]Field, 0, len(fields))
	for _, v := range fields {
		name := v.Name
		if !local {
			name = htmlsafe.StripTags(name)
		}
		value := htmlsafe.LocalOrRemote(v.Val, local, opts)
		if local {
			// Mastodon field values are inline HTML without a paragraph.
			if inner, ok := strings.CutPrefix(value, "<p>"); ok && strings.Count(value, "<p>") == 1 {
				value = strings.TrimSuffix(inner, "</p>")
			}
		}
		ret = append(ret, Field{Name: name, Value: value, VerifiedAt: v.VerifiedAt})
	}

	return ret
}
