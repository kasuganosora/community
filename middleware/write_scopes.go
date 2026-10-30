package middleware

import (
	"net/http"
	"regexp"

	"github.com/kasuganosora/community/models"
)

// writeScopeRule maps state-changing Mastodon API routes to the granular
// scope Mastodon requires for them. Tokens issued with the parent "write"
// scope carry every bit, so this only narrows granular tokens.
type writeScopeRule struct {
	pattern *regexp.Regexp
	kind    uint8
	bits    uint64
	// legacy lets Mastodon's deprecated "follow" / "push" scope satisfy it.
	legacy uint64
}

var writeScopeRules = func() []writeScopeRule {
	api := `^/api/v[12]`
	r := func(p string, kind uint8, bits, legacy uint64) writeScopeRule {
		return writeScopeRule{regexp.MustCompile(api + p), kind, bits, legacy}
	}
	w := uint8(models.AppScopeTypeWrite)
	return []writeScopeRule{
		r(`/push/`, models.AppScopeTypeOther, models.AppScopePush, 0),
		r(`/accounts/[^/]+/(follow|unfollow|remove_from_followers)$`, w, models.AppScopeWriteFollows, models.AppScopeFollow),
		r(`/follow_requests(/|$)`, w, models.AppScopeWriteFollows, models.AppScopeFollow),
		r(`/(accounts/[^/]+/(block|unblock)$|domain_blocks$)`, w, models.AppScopeWriteBlocks, models.AppScopeFollow),
		r(`/accounts/[^/]+/(mute|unmute)$`, w, models.AppScopeWriteMutes, models.AppScopeFollow),
		r(`/(accounts/update_credentials|profile/|accounts/[^/]+/(note|pin|unpin|endorse|unendorse)$|featured_tags)`, w, models.AppScopeWriteAccounts, 0),
		r(`/lists(/|$)`, w, models.AppScopeWriteLists, 0),
		r(`/filters(/|$)`, w, models.AppScopeWriteFilters, 0),
		r(`/statuses/[^/]+/(favourite|unfavourite)$`, w, models.AppScopeWriteFavourites, 0),
		r(`/statuses/[^/]+/(bookmark|unbookmark)$`, w, models.AppScopeWriteBookmarks, 0),
		r(`/media(/|$)`, w, models.AppScopeWriteMedia, 0),
		r(`/notifications(/|$)`, w, models.AppScopeWriteNotifications, 0),
		r(`/reports$`, w, models.AppScopeWriteReports, 0),
		r(`/conversations(/|$)`, w, models.AppScopeWriteConversations, 0),
		r(`/(statuses|polls|scheduled_statuses|markers)(/|$)`, w, models.AppScopeWriteStatuses, 0),
	}
}()

// granularWriteAllowed checks an OAuth token's granular scope for a write
// request on a known Mastodon route. Unknown routes fall back to the coarse
// oauthTokenMayWrite rule and any route-level CheckScopeMiddleware.
func granularWriteAllowed(r *http.Request, td *models.AuthTokenData) bool {
	if td.AuthType != models.AuthTokenTypeOauth {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if isTranslatePath(r.URL.Path) || isAppealPath(r.URL.Path) {
		return true
	}
	s := td.Scopes
	if s == nil {
		return false
	}
	for _, rule := range writeScopeRules {
		if !rule.pattern.MatchString(r.URL.Path) {
			continue
		}
		if rule.legacy != 0 && s.OtherScope&rule.legacy != 0 {
			return true
		}
		return s.HasScope(rule.kind, rule.bits)
	}
	return true
}
