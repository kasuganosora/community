package models

import (
	"strconv"
	"strings"
)

// ActorURI preserves an imported same-domain identity without making it remote.
func (a *Account) ActorURI(base string) string {
	if a.Domain != nil && a.OriginId != "" {
		return a.OriginId
	}
	if a.Domain == nil && a.LocalActorURI != "" {
		return a.LocalActorURI
	}
	return strings.TrimRight(base, "/") + "/users/" + a.Username
}
func (s *Status) ObjectURI(base string) string {
	if s.OriginUri != nil && *s.OriginUri != "" {
		return *s.OriginUri
	}
	if s.LocalObjectURI != "" {
		return s.LocalObjectURI
	}
	if s.Account == nil {
		return ""
	}
	return s.Account.ActorURI(base) + "/statuses/" + strconv.FormatUint(s.Id, 10)
}
