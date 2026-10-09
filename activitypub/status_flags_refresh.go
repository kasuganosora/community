package activitypub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// StatusFlagsResult says what RefreshStatusFlags found.
type StatusFlagsResult struct {
	SensitiveChanged bool // status.sensitive differs from the origin's
	Sensitive        bool // the origin's (derived) value
	FilesUpdated     int  // attachments whose sensitive flag or blurhash changed
	Gone             bool
}

var errNotNote = errors.New("object is not a note")

// RefreshStatusFlags re-fetches a remote status' ActivityPub object and
// updates only what was lost or never read at import time: the status'
// sensitive flag and each attachment's sensitive flag and blurhash.
// Text, counters and edit history are left alone. With dryRun nothing is
// written.
func RefreshStatusFlags(ctx context.Context, conn *gorm.DB, client ActorFetcher, status *models.Status, dryRun bool) (StatusFlagsResult, error) {
	var res StatusFlagsResult
	if status.OriginUri == nil || *status.OriginUri == "" {
		return res, ErrNotRemoteAccount
	}
	uri := *status.OriginUri
	obj, err := client.FetchObject(ctx, uri)
	if err != nil {
		if code := fetchStatusCode(err); code == http.StatusNotFound || code == http.StatusGone {
			res.Gone = true

			return res, nil
		}

		return res, fmt.Errorf("fetching object: %w", err)
	}
	if id, _ := obj["id"].(string); id != uri {
		return res, fmt.Errorf("object id %q does not match %q", id, uri)
	}
	switch t, _ := obj["type"].(string); t {
	case "Note", "Question", "Article", "Page":
	case "Tombstone":
		res.Gone = true

		return res, nil
	default:
		return res, errNotNote
	}

	incoming, err := incomingAttachments(ctx, obj["attachment"], status.AccountId)
	if err != nil {
		return res, err
	}
	noteSensitive, _ := obj["sensitive"].(bool)
	summary, _ := obj["summary"].(string)
	res.Sensitive = deriveSensitive(noteSensitive, htmlsafe.StripTags(summary), incoming)
	res.SensitiveChanged = res.Sensitive != status.Sensitive
	// A flag is only ever raised here, never lowered below the stored value:
	// an admin or the importer may have set it for good reasons.
	if !res.Sensitive {
		res.SensitiveChanged = false
	}

	var stored []*models.MediaAttachment
	if err := conn.Where("status_id = ?", status.Id).Find(&stored).Error; err != nil {
		return res, err
	}
	byURL := map[string]*models.MediaAttachment{}
	for _, a := range stored {
		byURL[a.RemoteUrl] = a
		byURL[stripQuery(a.RemoteUrl)] = a
	}
	for _, in := range incoming {
		cur := byURL[in.RemoteUrl]
		if cur == nil {
			cur = byURL[stripQuery(in.RemoteUrl)]
		}
		if cur == nil {
			continue
		}
		updates := map[string]any{}
		if in.Sensitive && !cur.Sensitive {
			updates["sensitive"] = true
		}
		if in.Blurhash != "" && cur.Blurhash == "" {
			updates["blurhash"] = in.Blurhash
		}
		if len(updates) == 0 {
			continue
		}
		res.FilesUpdated++
		if !dryRun {
			if err := conn.Model(&models.MediaAttachment{}).Where("id = ?", cur.Id).Updates(updates).Error; err != nil {
				return res, err
			}
		}
	}
	if res.SensitiveChanged && !dryRun {
		if err := conn.Model(&models.Status{}).Where("id = ?", status.Id).UpdateColumn("sensitive", true).Error; err != nil {
			return res, err
		}
	}

	return res, nil
}

func stripQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery, u.Fragment = "", ""

	return strings.TrimSpace(u.String())
}

// FixRemoteStatusDomains sets statuses.domain on remote statuses that lack it
// (imported by the boosted-unknown fetch before it recorded the host), from
// the author's account domain. It returns how many rows are (or, with dryRun,
// would be) fixed. Reblogs are left alone.
func FixRemoteStatusDomains(conn *gorm.DB, dryRun bool) (int64, error) {
	const where = "domain IS NULL AND origin_uri IS NOT NULL AND origin_uri <> '' AND reblog_id IS NULL AND " +
		"EXISTS (SELECT 1 FROM accounts WHERE accounts.id = statuses.account_id AND accounts.domain IS NOT NULL AND accounts.domain <> '')"
	if dryRun {
		var n int64
		err := conn.Model(&models.Status{}).Unscoped().Where(where).Count(&n).Error

		return n, err
	}
	res := conn.Exec("UPDATE statuses SET domain = (SELECT accounts.domain FROM accounts WHERE accounts.id = statuses.account_id) WHERE " + where)

	return res.RowsAffected, res.Error
}
