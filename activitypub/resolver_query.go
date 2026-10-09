package activitypub

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// AcctQuery is a search term of the form [@]user@domain or acct:user@domain.
type AcctQuery struct {
	Username string
	// Domain is lower-cased; it is empty for accounts on this instance,
	// including queries that spell out our own domain.
	Domain string
}

// ParseAcctQuery recognises a full account address. Bare usernames ("luna",
// "@luna") are not account addresses and return ok=false.
func ParseAcctQuery(query string) (AcctQuery, bool) {
	identifier, ok := remoteSearchIdentifier(query)
	if !ok || IsURLQuery(identifier) {
		return AcctQuery{}, false
	}
	username, domain, _ := strings.Cut(identifier, "@")
	domain = strings.ToLower(domain)
	if IsLocalDomain(domain) {
		domain = ""
	}
	return AcctQuery{Username: username, Domain: domain}, true
}

// IsURLQuery reports whether a search term is an http(s) URL.
func IsURLQuery(query string) bool {
	query = strings.TrimSpace(query)
	return strings.HasPrefix(query, "https://") || strings.HasPrefix(query, "http://")
}

// IsLocalDomain reports whether host names this instance.
func IsLocalDomain(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if local := strings.ToLower(systemsettings.GetDomain()); local != "" && host == local {
		return true
	}
	if base, err := url.Parse(systemsettings.GetBaseURL()); err == nil && base.Host != "" && strings.ToLower(base.Host) == host {
		return true
	}
	return false
}

// ResolveSearchAccount resolves only exact remote identifiers. Ordinary search
// terms stay local, and all network requests retain federation SSRF protection.
func ResolveSearchAccount(ctx context.Context, query string) (*models.Account, error) {
	identifier, ok := remoteSearchIdentifier(query)
	if !ok {
		return nil, nil
	}
	if IsURLQuery(identifier) {
		if err := validateFederationURI(identifier); err != nil {
			return nil, err
		}
		if parsed, err := url.Parse(identifier); err == nil && IsLocalDomain(parsed.Host) {
			return nil, nil
		}
	} else if acct, _ := ParseAcctQuery(identifier); acct.Domain == "" {
		// Our own domain: the local index already has every account.
		return nil, nil
	}
	return NewResolver(nil, "").ResolveAccount(ctx, identifier)
}

// ErrNotSearchable is returned for URLs that name neither an actor nor a
// status-like object.
var ErrNotSearchable = errors.New("remote object is neither an account nor a status")

// ResolveSearchURL fetches a pasted URL (profile page, actor id, note URL or
// id) and stores the remote account or status it names. Exactly one of the
// returned values is non-nil on success. Local URLs return nil, nil.
func ResolveSearchURL(ctx context.Context, rawURL string) (*models.Account, *models.Status, error) {
	identifier, ok := remoteSearchIdentifier(rawURL)
	if !ok || !IsURLQuery(identifier) {
		return nil, nil, nil
	}
	if err := validateFederationURI(identifier); err != nil {
		return nil, nil, err
	}
	if parsed, err := url.Parse(identifier); err == nil && IsLocalDomain(parsed.Host) {
		return nil, nil, nil
	}
	resolver := NewResolver(nil, "")
	obj, canonical, err := resolver.fetchCanonicalObject(ctx, identifier)
	if err != nil {
		return nil, nil, err
	}
	objectType, _ := obj["type"].(string)
	switch {
	case actorTypes[objectType]:
		account, err := resolver.resolveAccountByURI(ctx, canonical)
		return account, nil, err
	case statusObjectTypes[objectType]:
		status, err := resolver.ResolveStatus(ctx, canonical)
		return nil, status, err
	}
	return nil, nil, ErrNotSearchable
}

func remoteSearchIdentifier(query string) (string, bool) {
	query = strings.TrimSpace(query)
	if strings.HasPrefix(query, "https://") || strings.HasPrefix(query, "http://") {
		parsed, err := url.Parse(query)
		return query, err == nil && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
	}
	query = strings.TrimPrefix(strings.TrimPrefix(query, "acct:"), "@")
	parts := strings.Split(query, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(query, " /?#\\\t\r\n") {
		return "", false
	}
	host, err := url.Parse("https://" + parts[1])
	if err != nil || host.Hostname() == "" || host.User != nil {
		return "", false
	}
	return query, true
}
