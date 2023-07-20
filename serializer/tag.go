package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

type Tag struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

func ToTag(tag *models.Tag) (ret *Tag) {
	url := systemsettings.GetString(systemsettings.KeyDomain, "localhost") + "/tags/" + tag.Name
	ret = &Tag{
		Name: tag.Name,
		Url:  url,
	}
	return
}

func ToTags(tags []*models.Tag) (ret []*Tag) {
	if tags == nil {
		ret = make([]*Tag, 0)
		return
	}
	for _, tag := range tags {
		ret = append(ret, ToTag(tag))
	}
	return
}
