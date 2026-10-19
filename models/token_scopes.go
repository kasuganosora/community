package models

import (
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
	"admin:read":           {AppScopeTypeAdmin, AppScopeAdminRead},
	"admin:write":          {AppScopeTypeAdmin, AppScopeAdminWrite},
	"admin:read:accounts":  {AppScopeTypeAdmin, AppScopeAdminReadAccounts},
	"admin:write:accounts": {AppScopeTypeAdmin, AppScopeAdminWriteAccounts},
	"admin:read:reports":   {AppScopeTypeAdmin, AppScopeAdminReadReports},
	"admin:write:reports":  {AppScopeTypeAdmin, AppScopeAdminWriteReports},
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
