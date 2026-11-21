package models

import (
	"maps"
	"slices"
	"strings"
)

type scopeBits struct {
	kind uint8
	bits uint64
}

// personalTokenScopes maps the Mastodon scope names a personal access token
// may request to scope bits. Admin scopes are limited to the ones that guard
// real endpoints in this server.
var personalTokenScopes = map[string]scopeBits{
	"read":                 {AppScopeTypeRead, AppScopeRead},
	"read:accounts":        {AppScopeTypeRead, AppScopeReadAccounts},
	"read:blocks":          {AppScopeTypeRead, AppScopeReadBlocks},
	"read:bookmarks":       {AppScopeTypeRead, AppScopeReadBookmarks},
	"read:favourites":      {AppScopeTypeRead, AppScopeReadFavourites},
	"read:filters":         {AppScopeTypeRead, AppScopeReadFilters},
	"read:follows":         {AppScopeTypeRead, AppScopeReadFollows},
	"read:lists":           {AppScopeTypeRead, AppScopeReadLists},
	"read:mutes":           {AppScopeTypeRead, AppScopeReadMutes},
	"read:notifications":   {AppScopeTypeRead, AppScopeReadNotifications},
	"read:search":          {AppScopeTypeRead, AppScopeReadSearch},
	"read:statuses":        {AppScopeTypeRead, AppScopeReadStatuses},
	"write":                {AppScopeTypeWrite, AppScopeWrite},
	"write:accounts":       {AppScopeTypeWrite, AppScopeWriteAccounts},
	"write:blocks":         {AppScopeTypeWrite, AppScopeWriteBlocks},
	"write:bookmarks":      {AppScopeTypeWrite, AppScopeWriteBookmarks},
	"write:conversations":  {AppScopeTypeWrite, AppScopeWriteConversations},
	"write:favourites":     {AppScopeTypeWrite, AppScopeWriteFavourites},
	"write:filters":        {AppScopeTypeWrite, AppScopeWriteFilters},
	"write:follows":        {AppScopeTypeWrite, AppScopeWriteFollows},
	"write:lists":          {AppScopeTypeWrite, AppScopeWriteLists},
	"write:media":          {AppScopeTypeWrite, AppScopeWriteMedia},
	"write:mutes":          {AppScopeTypeWrite, AppScopeWriteMutes},
	"write:notifications":  {AppScopeTypeWrite, AppScopeWriteNotifications},
	"write:reports":        {AppScopeTypeWrite, AppScopeWriteReports},
	"write:statuses":       {AppScopeTypeWrite, AppScopeWriteStatuses},
	"push":                 {AppScopeTypeOther, AppScopePush},
	"follow":               {AppScopeTypeOther, AppScopeFollow},
	"read:matrix":          {AppScopeTypeOther, AppScopeReadMatrix},
	"write:matrix":         {AppScopeTypeOther, AppScopeWriteMatrix},
	"admin:read":           {AppScopeTypeAdmin, AppScopeAdminRead},
	"admin:write":          {AppScopeTypeAdmin, AppScopeAdminWrite},
	"admin:read:accounts":  {AppScopeTypeAdmin, AppScopeAdminReadAccounts},
	"admin:write:accounts": {AppScopeTypeAdmin, AppScopeAdminWriteAccounts},
	"admin:read:reports":   {AppScopeTypeAdmin, AppScopeAdminReadReports},
	"admin:write:reports":  {AppScopeTypeAdmin, AppScopeAdminWriteReports},
}

// OAuth applications additionally support the full set of admin scopes.
var oauthScopes = func() map[string]scopeBits {
	scopes := maps.Clone(personalTokenScopes)
	for name, bits := range map[string]uint64{
		"admin":                              AppScopeAdmin,
		"admin:read:canonical_email_blocks":  AppScopeAdminReadCanonicalEmailBlocks,
		"admin:read:domain_allows":           AppScopeAdminReadDomainAllows,
		"admin:read:domain_blocks":           AppScopeAdminReadDomainBlocks,
		"admin:read:email_domain_blocks":     AppScopeAdminReadEmailDomainBlocks,
		"admin:read:ip_blocks":               AppScopeAdminReadIpBlocks,
		"admin:read:thumbnails":              AppScopeAdminReadThumbnails,
		"admin:write:canonical_email_blocks": AppScopeAdminWriteCanonicalEmailBlocks,
		"admin:write:domain_allows":          AppScopeAdminWriteDomainAllows,
		"admin:write:domain_blocks":          AppScopeAdminWriteDomainBlocks,
		"admin:write:email_domain_blocks":    AppScopeAdminWriteEmailDomainBlocks,
		"admin:write:ip_blocks":              AppScopeAdminWriteIpBlocks,
		"admin:write:rules":                  AppScopeAdminWriteRules,
		"admin:write:thumbnails":             AppScopeAdminWriteThumbnails,
	} {
		scopes[name] = scopeBits{AppScopeTypeAdmin, bits}
	}
	scopes["read:collections"] = scopeBits{AppScopeTypeRead, AppScopeReadCollections}
	scopes["write:collections"] = scopeBits{AppScopeTypeWrite, AppScopeWriteCollections}
	scopes["profile"] = scopeBits{AppScopeTypeOther, AppScopeProfile}
	return scopes
}()

var oauthScopeOrder = func() []string {
	// Match parents before their children, so each set is expressed without
	// redundancy. Keep the historic order of coarse and Matrix scopes.
	priority := []string{"read", "write", "admin", "admin:read", "admin:write", "follow", "push", "read:matrix", "write:matrix"}
	ordered := slices.Clone(priority)
	for _, name := range slices.Sorted(maps.Keys(oauthScopes)) {
		if !slices.Contains(priority, name) {
			ordered = append(ordered, name)
		}
	}
	return ordered
}()

func OAuthScopeNames() []string { return slices.Clone(oauthScopeOrder) }

func ParseOAuthScopes(raw string) (ApplicationScope, bool) {
	return parseOAuthScopes(raw, nil)
}

// OAuthGrant matches a request against the registered bits. Old applications
// could store "follow" without its implied bits: keep that bound even when
// issuing a new token for a previously authorized code.
func (registered ApplicationScope) OAuthGrant(raw string) (ApplicationScope, bool) {
	if registered == (ApplicationScope{}) {
		// Before registration scopes were persisted, empty meant any non-admin grant.
		req, ok := ParseOAuthScopes(raw)
		return req, ok && req != (ApplicationScope{}) && req.AdminScope == 0
	}
	req, ok := parseOAuthScopes(raw, &registered)
	return req, ok && registered.Covers(req)
}

func parseOAuthScopes(raw string, registered *ApplicationScope) (ApplicationScope, bool) {
	var scopes ApplicationScope
	for _, name := range strings.Fields(raw) {
		def, known := oauthScopes[name]
		if !known {
			return ApplicationScope{}, false
		}
		scopes.AddScope(def.kind, def.bits)
		if name == "follow" {
			for _, implied := range followImplies {
				d := oauthScopes[implied]
				if registered == nil || registered.HasScope(d.kind, d.bits) {
					scopes.AddScope(d.kind, d.bits)
				}
			}
		}
	}
	return scopes, true
}

// OAuthScopeString renders only permissions actually held, including during refresh.
func (s *ApplicationScope) OAuthScopeString() string {
	if s == nil {
		return ""
	}
	remaining := *s
	var parts []string
	for _, name := range oauthScopeOrder {
		def := oauthScopes[name]
		if remaining.HasScope(def.kind, def.bits) {
			parts = append(parts, name)
			remaining.RemoveScope(def.kind, def.bits)
			if name == "follow" {
				for _, implied := range followImplies {
					d := oauthScopes[implied]
					remaining.RemoveScope(d.kind, d.bits)
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

// followImplies is Mastodon's legacy "follow" scope.
var followImplies = []string{"read:follows", "write:follows", "read:blocks", "write:blocks", "read:mutes", "write:mutes"}

// ParsePersonalTokenScopes validates and dedupes requested scope names and
// returns them sorted with the resulting bits. ok is false for an empty list
// or any unknown name.
func ParsePersonalTokenScopes(names []string) (scopes ApplicationScope, normalized []string, ok bool) {
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		def, known := personalTokenScopes[name]
		if !known {
			return ApplicationScope{}, nil, false
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		normalized = append(normalized, name)
		scopes.AddScope(def.kind, def.bits)
		if name == "follow" {
			for _, implied := range followImplies {
				d := personalTokenScopes[implied]
				scopes.AddScope(d.kind, d.bits)
			}
		}
	}
	if len(normalized) == 0 {
		return ApplicationScope{}, nil, false
	}
	slices.Sort(normalized)
	return scopes, normalized, true
}

// HasAdminWrite reports whether any admin write bit is set.
func (s *ApplicationScope) HasAdminWrite() bool {
	return s != nil && s.AdminScope&AppScopeAdminWrite != 0
}
