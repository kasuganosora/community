package activitypub

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

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

func localActorName(uri string) (string, bool) {
	prefix := strings.TrimRight(systemsettings.GetBaseURL(), "/") + "/users/"
	if !strings.HasPrefix(uri, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(uri, prefix)
	if name == "" || strings.ContainsAny(name, "/?#%") {
		return "", false
	}
	return name, true
}

func recipientAccounts(ctx context.Context, object map[string]any, public bool) (models.Accounts, error) {
	addresses := append(activityAddresses(object["to"]), activityAddresses(object["cc"])...)
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
		var account models.Account
		query := activityDB(ctx).Where("origin_id = ? AND domain IS NOT NULL", uri)
		if name, ok := localActorName(uri); ok {
			query = activityDB(ctx).Where("username = ? AND domain IS NULL", name)
		}
		err := query.First(&account).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
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
	var status models.Status
	prefix := strings.TrimRight(systemsettings.GetBaseURL(), "/") + "/users/"
	if strings.HasPrefix(uri, prefix) {
		parts := strings.Split(strings.TrimPrefix(uri, prefix), "/")
		if len(parts) == 3 && parts[1] == "statuses" {
			id, err := strconv.ParseUint(parts[2], 10, 64)
			if err == nil {
				err = activityDB(ctx).Preload("ToAccounts").Joins("JOIN accounts ON accounts.id = statuses.account_id").Where("statuses.id = ? AND accounts.username = ? AND accounts.domain IS NULL", id, parts[0]).First(&status).Error
				return &status, err
			}
		}
	}
	err := activityDB(ctx).Preload("ToAccounts").Where("origin_uri = ? OR origin_url = ?", uri, uri).First(&status).Error
	return &status, err
}

func persistIncomingStatus(ctx context.Context, conn *gorm.DB, status *models.Status) error {
	ids := make([]uint64, 0, len(status.ToAccounts))
	for _, account := range status.ToAccounts {
		ids = append(ids, account.Id)
	}
	return conn.WithContext(models.WithStructuredRecipientIDs(ctx, ids)).Omit("ToAccounts").Create(status).Error
}
