package activitypub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

// quoteLinkRels are the rel values of FEP-e232 object links that point at the
// quoted post.
var quoteLinkRels = map[string]bool{
	"https://misskey-hub.net/ns#_misskey_quote": true,
	"http://fedibird.com/ns#quoteUri":           true,
	"https://w3id.org/fep/044f#quote":           true,
}

// quoteObjectURI returns the quoted post of a Note: FEP-044f / Mastodon
// "quote", Misskey "_misskey_quote", and the older "quoteUrl"/"quoteUri".
func quoteObjectURI(obj map[string]any) string {
	for _, key := range []string{"quote", "_misskey_quote", "quoteUrl", "quoteUri", "quoteURL"} {
		switch v := obj[key].(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			if id, _ := v["id"].(string); id != "" {
				return id
			}
		}
	}
	return quoteLinkHref(obj["tag"])
}

// quoteLinkHref finds a FEP-e232 Link tag that references the quoted post.
func quoteLinkHref(tags any) string {
	var list []any
	switch v := tags.(type) {
	case []any:
		list = v
	case map[string]any:
		list = []any{v}
	}
	for _, item := range list {
		tag, _ := item.(map[string]any)
		if tag == nil {
			continue
		}
		if kind, _ := tag["type"].(string); kind != "Link" {
			continue
		}
		rel, _ := tag["rel"].(string)
		if !quoteLinkRels[rel] {
			continue
		}
		if href, _ := tag["href"].(string); href != "" {
			return href
		}
	}
	return ""
}

// knownQuoteID links a quote to a post this server already has. Unknown
// quotes are not fetched here (the content keeps its "RE: <link>" line), and
// the API hides the embed from viewers who cannot see the quoted post.
func knownQuoteID(ctx context.Context, obj map[string]any) *uint64 {
	uri := quoteObjectURI(obj)
	if uri == "" {
		return nil
	}
	quoted, err := findInboundParent(ctx, uri)
	if err != nil || quoted == nil || quoted.Id == 0 {
		return nil
	}
	if quoted.Visibility != models.StatusVisibilityPublic && quoted.Visibility != models.StatusVisibilityUnlisted {
		return nil
	}
	id := quoted.Id
	return &id
}

// quoteFetchSlots bounds concurrent background quote fetches.
var quoteFetchSlots = make(chan struct{}, 4)

// startQuoteFetch runs f in the background; tests replace it to run inline.
var startQuoteFetch = func(f func()) { go f() }

var (
	quoteResolverOnce sync.Once
	quoteResolverInst *Resolver
)

// quoteFetcher is a resolver that never follows quotes of the posts it
// imports, which bounds quote fetching to depth one.
var quoteFetcher = func() *Resolver {
	quoteResolverOnce.Do(func() {
		quoteResolverInst = NewResolver(nil, InstanceActorURI())
		quoteResolverInst.noQuoteFetch = true
	})
	return quoteResolverInst
}

// scheduleQuoteFetch imports the post a new remote status quotes when this
// server does not have it. Failures leave the "RE: <link>" text as it is.
func scheduleQuoteFetch(r *Resolver, status *models.Status, obj map[string]any) {
	if status == nil || status.Id == 0 || status.QuoteId != nil || (r != nil && r.noQuoteFetch) {
		return
	}
	uri := quoteObjectURI(obj)
	if uri == "" {
		return
	}
	id := status.Id
	startQuoteFetch(func() {
		quoteFetchSlots <- struct{}{}
		defer func() { <-quoteFetchSlots }()
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic fetching quote %s: %v", uri, rec)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := fetchAndLinkQuote(ctx, id, uri); err != nil {
			log.Debug("quote %s of status %d not imported: %v", uri, id, err)
		}
	})
}

// fetchAndLinkQuote imports the quoted post (if needed) and sets quote_id.
func fetchAndLinkQuote(ctx context.Context, statusID uint64, uri string) error {
	quoted, err := fetchQuotedStatus(ctx, uri)
	if err != nil {
		return err
	}
	res := db.GetDB().WithContext(ctx).Model(&models.Status{}).
		Where("id = ? AND quote_id IS NULL", statusID).
		UpdateColumn("quote_id", quoted.Id)
	return res.Error
}

// fetchQuotedStatus returns the stored copy of the post at uri, fetching it
// with the signed fetch client first. Only public or unlisted posts hosted on
// their author's server are imported; the author profile is fetched too.
func fetchQuotedStatus(ctx context.Context, uri string) (*models.Status, error) {
	return fetchPublicStatus(ctx, quoteFetcher(), uri)
}

// errStatusNotPublic rejects followers-only and direct posts.
var errStatusNotPublic = errors.New("post is not public")

// fetchPublicStatus returns the stored copy of the public or unlisted post at
// uri, importing it through r when this server does not have it yet.
func fetchPublicStatus(ctx context.Context, r *Resolver, uri string) (*models.Status, error) {
	if err := validateFederationURI(uri); err != nil {
		return nil, err
	}
	if existing, err := findInboundParent(ctx, uri); err == nil && existing != nil && existing.Id != 0 {
		if existing.Visibility > models.StatusVisibilityUnlisted {
			return nil, errStatusNotPublic
		}
		return existing, nil
	}
	obj, canonical, err := r.fetchCanonicalObject(ctx, uri)
	if err != nil {
		return nil, err
	}
	if canonical != uri {
		if existing, err := findInboundParent(ctx, canonical); err == nil && existing != nil && existing.Id != 0 {
			if existing.Visibility > models.StatusVisibilityUnlisted {
				return nil, errStatusNotPublic
			}
			return existing, nil
		}
	}
	if getVisibilityFromActivity(obj) > models.StatusVisibilityUnlisted {
		return nil, errStatusNotPublic
	}
	status, err := r.createStatusFromObject(obj, canonical) //nolint:contextcheck
	if err != nil {
		return nil, fmt.Errorf("importing post: %w", err)
	}
	return &status, nil
}

// BackfillQuotes links existing remote quote posts whose quoted post was not
// imported. It looks at the newest limit statuses created within days, makes
// at most limit fetches and returns how many posts were linked.
func BackfillQuotes(ctx context.Context, days, limit int) (linked int, err error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 200
	}
	var rows []struct {
		Id      uint64
		Content string
	}
	since := time.Now().AddDate(0, 0, -days)
	err = db.GetDB().WithContext(ctx).Table("statuses").
		Select("statuses.id, status_contents.content AS content").
		Joins("JOIN status_contents ON status_contents.status_id = statuses.id").
		Where("statuses.domain IS NOT NULL AND statuses.quote_id IS NULL AND statuses.deleted_at IS NULL AND statuses.reblog_id IS NULL AND statuses.created_at > ?", since).
		Where("status_contents.content LIKE ?", "%RE: <a%").
		Order("statuses.id DESC").Limit(limit * 4).Scan(&rows).Error
	if err != nil {
		return 0, err
	}
	attempts := 0
	for _, row := range rows {
		if attempts >= limit || ctx.Err() != nil {
			break
		}
		uri := reLinkHref(row.Content)
		if uri == "" {
			continue
		}
		attempts++
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ferr := fetchAndLinkQuote(fctx, row.Id, uri)
		cancel()
		if ferr == nil {
			linked++
		}
	}
	return linked, nil
}

// reLinkHref extracts the quoted URL from the "RE: <a href=...>" line that
// servers without quote support append to a quote post.
func reLinkHref(content string) string {
	i := strings.Index(content, "RE: <a")
	if i < 0 {
		return ""
	}
	rest := content[i:]
	j := strings.Index(rest, `href="`)
	if j < 0 {
		return ""
	}
	rest = rest[j+len(`href="`):]
	k := strings.Index(rest, `"`)
	if k < 0 {
		return ""
	}
	return strings.ReplaceAll(rest[:k], "&amp;", "&")
}
