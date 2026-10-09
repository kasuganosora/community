package activitypub

import (
	"context"

	"github.com/kasuganosora/community/models"
)

// objectHashtagNames returns the names of the Hashtag entries of an
// ActivityPub object's "tag" property ("#Tag" -> "Tag"), and whether the
// property was usable at all. Names are normalized and validated by
// models.WithStructuredTagNames.
func objectHashtagNames(tags any) ([]string, bool) {
	var list []any
	switch v := tags.(type) {
	case []any:
		list = v
	case map[string]any:
		list = []any{v}
	default:
		return nil, false
	}
	names := make([]string, 0, len(list))
	for _, entry := range list {
		tag, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := tag["type"].(string); kind != "Hashtag" {
			continue
		}
		if name, _ := tag["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names, true
}

// withObjectHashtags makes the object's Hashtag tags authoritative for the
// status written with the returned context. Without a "tag" array the
// hashtags are parsed from the visible text of the content instead (HTML
// stripped, entities decoded); see models.ExtractTagNames.
func withObjectHashtags(ctx context.Context, tags any) context.Context {
	names, ok := objectHashtagNames(tags)
	if !ok {
		return ctx
	}
	return models.WithStructuredTagNames(ctx, names)
}
