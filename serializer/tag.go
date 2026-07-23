package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"strconv"
)

// Tag represents a Mastodon-compatible hashtag.
type Tag struct {
	Name         string       `json:"name"`
	Url          string       `json:"url"`
	History      []TagHistory `json:"history,omitempty"`
	Following    bool         `json:"following,omitempty"`
	StatusCount  *uint64      `json:"status_count,omitempty"`
	AccountCount *uint64      `json:"account_count,omitempty"`
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

	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	name := tag.Name
	// Remove # prefix if present for URL
	if len(name) > 0 && name[0] == '#' {
		name = name[1:]
	}

	ret = &Tag{
		Name: name,
		Url:  "https://" + domain + "/tags/" + name,
	}

	// Include counts for search results
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
