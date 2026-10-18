package filter

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/models"
)

// Stable error codes for filter validation failures. The web app maps them
// to translated messages; "error" keeps a readable English message for other
// clients.
const (
	ErrCodeFilterTitleRequired      = "FILTER_TITLE_REQUIRED"
	ErrCodeFilterKeywordsRequired   = "FILTER_KEYWORDS_REQUIRED"
	ErrCodeFilterKeywordEmpty       = "FILTER_KEYWORD_EMPTY"
	ErrCodeFilterKeywordTooLong     = "FILTER_KEYWORD_TOO_LONG"
	ErrCodeFilterTooManyKeywords    = "FILTER_TOO_MANY_KEYWORDS"
	ErrCodeFilterActionUnsupported  = "FILTER_ACTION_UNSUPPORTED"
	ErrCodeFilterContextUnsupported = "FILTER_CONTEXT_UNSUPPORTED"
	ErrCodeFilterNotFound           = "FILTER_NOT_FOUND"
	ErrCodeFilterKeywordNotFound    = "FILTER_KEYWORD_NOT_FOUND"
	ErrCodeFilterInvalidRequest     = "FILTER_INVALID_REQUEST"
)

const (
	maxKeywordLength   = 200
	maxKeywordsPerList = 200
)

func fail(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg, "code": code})
}

// flexBool accepts the spellings Rails clients send for _destroy:
// true/false, "true"/"false", "1"/"0", 1/0.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case bool:
		*b = flexBool(t)
	case float64:
		*b = t != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		*b = s == "true" || s == "1"
	default:
		*b = false
	}

	return nil
}

// KeywordAttribute is one entry of Mastodon's keywords_attributes[].
type KeywordAttribute struct {
	Id        string   `json:"id"`
	Keyword   string   `json:"keyword"`
	WholeWord *bool    `json:"whole_word"`
	Destroy   flexBool `json:"_destroy"`
}

// keywordUUID extracts the stored uuid from a public keyword id
// ("<filter id>-<uuid>"); a bare uuid is accepted too.
func keywordUUID(filterID, id string) string {
	id = strings.TrimSpace(id)
	if prefix := filterID + "-"; strings.HasPrefix(id, prefix) {
		return strings.TrimPrefix(id, prefix)
	}

	return id
}

type keywordError struct {
	status int
	code   string
	msg    string
}

func validKeyword(text string) (string, *keywordError) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", &keywordError{422, ErrCodeFilterKeywordEmpty, "Keyword cannot be empty"}
	}
	if len([]rune(text)) > maxKeywordLength {
		return "", &keywordError{422, ErrCodeFilterKeywordTooLong, "Keyword is too long"}
	}

	return text, nil
}

// applyKeywordAttributes applies keywords_attributes to the current keyword
// list: entries with an id update or (with _destroy) remove that keyword,
// entries without one add a keyword. Adding a keyword that already exists
// with the same text is a no-op, so retries don't create duplicates.
func applyKeywordAttributes(filterID string, current []*models.FilterKeyword, attrs []KeywordAttribute) ([]*models.FilterKeyword, *keywordError) {
	out := make([]*models.FilterKeyword, 0, len(current)+len(attrs))
	for _, k := range current {
		if k != nil {
			cp := *k
			cp.Uuid = cp.KeyID() // pin legacy ids so an edit doesn't change them
			out = append(out, &cp)
		}
	}

	indexOf := func(pred func(*models.FilterKeyword) bool) int {
		for i, k := range out {
			if pred(k) {
				return i
			}
		}

		return -1
	}

	for _, attr := range attrs {
		if attr.Id != "" {
			id := keywordUUID(filterID, attr.Id)
			i := indexOf(func(k *models.FilterKeyword) bool { return k.KeyID() == id })
			if i < 0 {
				return nil, &keywordError{404, ErrCodeFilterKeywordNotFound, "keyword not found"}
			}
			if attr.Destroy {
				out = append(out[:i], out[i+1:]...)

				continue
			}
			if attr.Keyword != "" {
				text, kerr := validKeyword(attr.Keyword)
				if kerr != nil {
					return nil, kerr
				}
				out[i].Keyword = text
			}
			if attr.WholeWord != nil {
				out[i].WholeWord = *attr.WholeWord
			}

			continue
		}
		if attr.Destroy {
			continue
		}
		text, kerr := validKeyword(attr.Keyword)
		if kerr != nil {
			return nil, kerr
		}
		whole := attr.WholeWord != nil && *attr.WholeWord
		if i := indexOf(func(k *models.FilterKeyword) bool { return k.Keyword == text }); i >= 0 {
			out[i].WholeWord = whole

			continue
		}
		out = append(out, &models.FilterKeyword{Uuid: uuid.NewString(), Keyword: text, WholeWord: whole})
	}

	if len(out) > maxKeywordsPerList {
		return nil, &keywordError{422, ErrCodeFilterTooManyKeywords, "Too many keywords"}
	}

	return out, nil
}
