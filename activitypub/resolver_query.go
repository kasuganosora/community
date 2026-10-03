package activitypub

import (
	"context"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/models"
)

// ResolveSearchAccount resolves only exact remote identifiers. Ordinary search
// terms stay local, and all network requests retain federation SSRF protection.
func ResolveSearchAccount(ctx context.Context, query string) (*models.Account, error) {
	identifier, ok := remoteSearchIdentifier(query)
	if !ok {
		return nil, nil
	}
	if strings.HasPrefix(identifier, "http://") || strings.HasPrefix(identifier, "https://") {
		if err := validateFederationURI(identifier); err != nil {
			return nil, err
		}
	}
	return NewResolver(nil, "").ResolveAccount(ctx, identifier)
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
