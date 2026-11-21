package serializer

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// Tag represents a Mastodon-compatible hashtag.
type Tag struct {
	Name      string       `json:"name"`
	Url       string       `json:"url"`
	History   []TagHistory `json:"history,omitempty"`
	Following bool         `json:"following,omitempty"`
	// Lifetime counts also include non-public posts and must stay off the wire.
	StatusCount  *uint64 `json:"-"`
	AccountCount *uint64 `json:"-"`
}

// TagHistory represents tag usage history.
type TagHistory struct {
	Day      string `json:"day"`
	Uses     string `json:"uses"`
	Accounts string `json:"accounts"`
}

// ToTag converts a models.Tag to serializer.Tag.
func ToTag(tag *models.Tag) (ret *Tag) {
	if tag == nil {
		return nil
	}

	name := strings.TrimPrefix(tag.Name, "#")
	ret = &Tag{Name: name, Url: systemsettings.GetBaseURL() + "/tags/" + url.PathEscape(name)}

	// Preserve the counters for Go callers.
	if tag.StatusCount > 0 {
		ret.StatusCount = &tag.StatusCount
	}

	if tag.AccountCount > 0 {
		ret.AccountCount = &tag.AccountCount
	}

	return
}

// ToTags converts a slice of models.Tag to serializer.Tag.
func ToTags(tags []*models.Tag) (ret []*Tag) {
	if tags == nil {
		ret = make([]*Tag, 0)

		return
	}

	ret = make([]*Tag, 0, len(tags))

	for _, tag := range tags {
		if t := ToTag(tag); t != nil {
			ret = append(ret, t)
		}
	}

	return
}

// ToTagWithHistory creates a tag with usage history.
func ToTagWithHistory(tag *models.Tag) (ret *Tag) {
	ret = ToTag(tag)
	if ret == nil {
		return nil
	}

	// Create dummy history for now (real implementation would track daily usage)
	ret.History = []TagHistory{
		{
			Day:      "0",
			Uses:     strconv.FormatUint(tag.StatusCount, 10),
			Accounts: strconv.FormatUint(tag.AccountCount, 10),
		},
	}

	return
}
