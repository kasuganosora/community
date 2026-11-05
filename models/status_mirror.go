package models

import (
	"net/url"
	"strings"

	"gorm.io/gorm"
)

// MirrorLocalIDs maps a remote status id to the local status it duplicates.
// A match is an origin URI or origin URL that is the local object, a local
// permalink whose id is that status, or a reblog of that status by the same
// person mirrored on this instance. Callers use the map to collapse timeline
// display and stream inserts. The remote row is left in place.
func MirrorLocalIDs(conn *gorm.DB, ids []uint64, baseURL string) (map[uint64]uint64, error) {
	out := map[uint64]uint64{}
	if conn == nil || len(ids) == 0 {
		return out, nil
	}
	var candidates []mirrorCandidate
	err := conn.Model(&Status{}).
		Select("statuses.id, statuses.origin_uri, statuses.origin_url, statuses.local_object_uri, statuses.reblog_id, accounts.username, accounts.domain AS account_domain").
		Joins("JOIN accounts ON accounts.id = statuses.account_id").
		Where("statuses.id IN ? AND statuses.deleted_at IS NULL", ids).
		Scan(&candidates).Error
	if err != nil {
		return nil, err
	}
	remotes := make([]mirrorCandidate, 0, len(candidates))
	for _, row := range candidates {
		if remoteMirrorCandidate(row) {
			remotes = append(remotes, row)
		}
	}
	if len(remotes) == 0 {
		return out, nil
	}
	locals, err := loadMirrorLocals(conn, remotes, baseURL)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint64]mirrorLocal, len(locals))
	byURI := map[string]mirrorLocal{}
	for _, local := range locals {
		byID[local.ID] = local
		rememberMirrorURI(byURI, local.LocalObjectURI, local)
		if local.OriginURL != nil {
			rememberMirrorURI(byURI, *local.OriginURL, local)
		}
	}
	for _, row := range remotes {
		local, ok := matchMirror(row, byID, byURI, baseURL)
		if ok && local.ID != row.ID {
			out[row.ID] = local.ID
		}
	}
	return out, nil
}

type mirrorCandidate struct {
	ID             uint64  `gorm:"column:id"`
	OriginURI      *string `gorm:"column:origin_uri"`
	OriginURL      *string `gorm:"column:origin_url"`
	LocalObjectURI string  `gorm:"column:local_object_uri"`
	ReblogID       *uint64 `gorm:"column:reblog_id"`
	Username       string  `gorm:"column:username"`
	AccountDomain  *string `gorm:"column:account_domain"`
}

type mirrorLocal struct {
	ID             uint64  `gorm:"column:id"`
	LocalObjectURI string  `gorm:"column:local_object_uri"`
	OriginURL      *string `gorm:"column:origin_url"`
	Username       string  `gorm:"column:username"`
}

func remoteMirrorCandidate(row mirrorCandidate) bool {
	if strings.TrimSpace(derefString(row.OriginURI)) != "" || strings.TrimSpace(derefString(row.OriginURL)) != "" {
		return true
	}
	return row.ReblogID != nil && *row.ReblogID > 0 && strings.TrimSpace(derefString(row.AccountDomain)) != ""
}

func loadMirrorLocals(conn *gorm.DB, remotes []mirrorCandidate, baseURL string) ([]mirrorLocal, error) {
	uris := make([]string, 0)
	seenURI := map[string]bool{}
	ids := make([]uint64, 0)
	seenID := map[uint64]bool{}
	addURI := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		for _, value := range []string{raw, canonicalMirrorURI(raw)} {
			if value == "" || seenURI[value] {
				continue
			}
			seenURI[value] = true
			uris = append(uris, value)
		}
		if id, ok := localIDFromMirrorURI(raw, baseURL); ok && !seenID[id] {
			seenID[id] = true
			ids = append(ids, id)
		}
	}
	for _, row := range remotes {
		addURI(derefString(row.OriginURI))
		addURI(derefString(row.OriginURL))
		if row.ReblogID != nil && *row.ReblogID > 0 && !seenID[*row.ReblogID] {
			seenID[*row.ReblogID] = true
			ids = append(ids, *row.ReblogID)
		}
	}
	if len(uris) == 0 && len(ids) == 0 {
		return nil, nil
	}
	query := conn.Model(&Status{}).
		Select("statuses.id, statuses.local_object_uri, statuses.origin_url, accounts.username").
		Joins("JOIN accounts ON accounts.id = statuses.account_id").
		Where("statuses.deleted_at IS NULL AND accounts.domain IS NULL AND (statuses.origin_uri IS NULL OR statuses.origin_uri = '')")
	parts := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if len(uris) > 0 {
		parts = append(parts, "(statuses.local_object_uri IN ? OR statuses.origin_url IN ?)")
		args = append(args, uris, uris)
	}
	if len(ids) > 0 {
		parts = append(parts, "statuses.id IN ?")
		args = append(args, ids)
	}
	var locals []mirrorLocal
	err := query.Where(strings.Join(parts, " OR "), args...).Scan(&locals).Error
	return locals, err
}

func matchMirror(row mirrorCandidate, byID map[uint64]mirrorLocal, byURI map[string]mirrorLocal, baseURL string) (mirrorLocal, bool) {
	for _, raw := range []string{derefString(row.OriginURI), derefString(row.OriginURL)} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if local, ok := byURI[raw]; ok {
			return local, true
		}
		if canon := canonicalMirrorURI(raw); canon != "" {
			if local, ok := byURI[canon]; ok {
				return local, true
			}
		}
		if id, ok := localIDFromMirrorURI(raw, baseURL); ok {
			if local, found := byID[id]; found {
				return local, true
			}
		}
	}
	if row.ReblogID != nil && *row.ReblogID > 0 {
		if local, found := byID[*row.ReblogID]; found && sameLocalPerson(row, local, baseURL) {
			return local, true
		}
	}
	return mirrorLocal{}, false
}

func rememberMirrorURI(byURI map[string]mirrorLocal, raw string, local mirrorLocal) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	byURI[raw] = local
	if canon := canonicalMirrorURI(raw); canon != "" {
		byURI[canon] = local
	}
}

func sameLocalPerson(row mirrorCandidate, local mirrorLocal, baseURL string) bool {
	domain := strings.TrimSpace(derefString(row.AccountDomain))
	if domain == "" || !strings.EqualFold(strings.TrimSpace(row.Username), strings.TrimSpace(local.Username)) {
		return false
	}
	host, hostname := mirrorHosts(baseURL)
	return host != "" && (strings.EqualFold(domain, host) || strings.EqualFold(domain, hostname))
}

func localIDFromMirrorURI(raw, baseURL string) (uint64, bool) {
	if !sameMirrorHost(raw, baseURL) {
		return 0, false
	}
	_, id, ok := localStatusPath(raw)
	return id, ok && id > 0
}

func sameMirrorHost(raw, baseURL string) bool {
	leftHost, leftName := mirrorHosts(raw)
	rightHost, rightName := mirrorHosts(baseURL)
	if leftName == "" || rightName == "" {
		return false
	}
	return strings.EqualFold(leftName, rightName) || strings.EqualFold(leftHost, rightHost)
}

func mirrorHosts(raw string) (host, hostname string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", ""
	}
	return parsed.Host, parsed.Hostname()
}

func canonicalMirrorURI(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
