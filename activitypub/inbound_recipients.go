package activitypub

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

func activityAddresses(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func recipientAccounts(ctx context.Context, object map[string]any, public bool) (models.Accounts, error) {
	return recipientAccountsWithResolver(ctx, object, public, resolveDirectRecipient)
}

func recipientAccountsWithResolver(ctx context.Context, object map[string]any, public bool, resolve func(context.Context, string) (*models.Account, error)) (models.Accounts, error) {
	addresses := append(activityAddresses(object["to"]), activityAddresses(object["cc"])...)
	direct := getVisibilityFromActivity(object) == models.StatusVisibilityDirect
	if direct {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
	}
	unknown := 0
	seenURI := map[string]bool{}
	// Public mentions are presentation/notification targets. For restricted
	// messages, only explicit addressing grants access; tags never add readers.
	if public {
		if tags, ok := object["tag"].([]any); ok {
			for _, v := range tags {
				if tag, ok := v.(map[string]any); ok && tag["type"] == "Mention" {
					if href, ok := tag["href"].(string); ok {
						addresses = append(addresses, href)
					}
				}
			}
		}
	}
	var result models.Accounts
	seen := map[uint64]bool{}
	for _, uri := range addresses {
		if seenURI[uri] {
			continue
		}
		seenURI[uri] = true
		if direct && directAddressIsCollection(uri) {
			continue
		}
		var account models.Account
		query := activityDB(ctx).Where("origin_id = ? AND domain IS NOT NULL", uri)
		if sameURIOrigin(uri, systemsettings.GetBaseURL()) {
			query = localActorQuery(activityDB(ctx), uri)
		}
		err := query.First(&account).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if !direct || sameURIOrigin(uri, systemsettings.GetBaseURL()) {
				continue
			}
			unknown++
			if unknown > 32 {
				return nil, errors.New("too many unresolved direct recipients")
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := validateFederationURI(uri); err != nil {
				return nil, err
			}
			resolved, resolveErr := resolve(ctx, uri)
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve explicit direct recipient: %w", resolveErr)
			}
			if resolved == nil || resolved.Id == 0 || resolved.Domain == nil || resolved.OriginId != uri {
				return nil, errors.New("invalid direct recipient actor")
			}
			account = *resolved
			err = nil
		}
		if err != nil {
			return nil, err
		}
		if !seen[account.Id] {
			result = append(result, &account)
			seen[account.Id] = true
		}
	}
	return result, nil
}

func sameURIOrigin(a, b string) bool {
	ua, ea := url.Parse(a)
	ub, eb := url.Parse(b)
	return ea == nil && eb == nil && ua.Scheme == ub.Scheme && ua.Host != "" && strings.EqualFold(ua.Host, ub.Host) && ua.User == nil && ub.User == nil
}

func findInboundParent(ctx context.Context, uri string) (*models.Status, error) {
	return models.FindReplyParent(activityDB(ctx), systemsettings.GetBaseURL(), uri)
}

func persistIncomingStatus(ctx context.Context, conn *gorm.DB, status *models.Status) error {
	ids := make([]uint64, 0, len(status.ToAccounts))
	for _, account := range status.ToAccounts {
		ids = append(ids, account.Id)
	}
	return conn.WithContext(models.WithStructuredRecipientIDs(ctx, ids)).Omit("ToAccounts", "Emojis").Create(status).Error
}

func directAddressIsCollection(uri string) bool {
	if uri == "https://www.w3.org/ns/activitystreams#Public" || uri == "as:Public" || uri == "Public" {
		return true
	}
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}
	p := strings.TrimRight(u.Path, "/")
	return strings.HasSuffix(p, "/followers") || strings.HasSuffix(p, "/following")
}

// Dereference only explicitly addressed actors. Collection objects and an actor
// claiming a different identity must not be imported as a private recipient.
func resolveDirectRecipient(ctx context.Context, uri string) (*models.Account, error) {
	actor, err := NewFetchClient().FetchActor(ctx, uri)
	if err != nil {
		return nil, err
	}
	if actor.ID != uri {
		return nil, errors.New("recipient actor ID mismatch")
	}
	switch actor.Type {
	case "Person", "Service", "Application", "Organization", "Group":
	default:
		return nil, errors.New("direct recipient is not an actor")
	}
	if actor.Inbox == "" {
		return nil, errors.New("recipient actor inbox missing")
	}
	if err := validateFederationURI(actor.Inbox); err != nil {
		return nil, err
	}
	account, err := createRemoteAccount(activityDB(ctx), actor)
	return &account, err
}
