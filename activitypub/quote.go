package activitypub

import (
	"context"

	"github.com/kasuganosora/community/models"
)

// quoteObjectURI returns the quoted post of a Note: FEP-044f / Mastodon
// "quote", Misskey "_misskey_quote", and the older "quoteUrl"/"quoteUri".
func quoteObjectURI(obj map[string]any) string {
	for _, key := range []string{"quote", "_misskey_quote", "quoteUrl", "quoteUri", "quoteURL"} {
		switch v := obj[key].(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			if id, _ := v["id"].(string); id != "" {
				return id
			}
		}
	}
	return ""
}

// knownQuoteID links a quote to a post this server already has. Unknown
// quotes are not fetched here (the content keeps its "RE: <link>" line), and
// the API hides the embed from viewers who cannot see the quoted post.
func knownQuoteID(ctx context.Context, obj map[string]any) *uint64 {
	uri := quoteObjectURI(obj)
	if uri == "" {
		return nil
	}
	quoted, err := findInboundParent(ctx, uri)
	if err != nil || quoted == nil || quoted.Id == 0 {
		return nil
	}
	if quoted.Visibility != models.StatusVisibilityPublic && quoted.Visibility != models.StatusVisibilityUnlisted {
		return nil
	}
	id := quoted.Id
	return &id
}
