package serializer

import (
	"context"
	"fmt"
	"github.com/kasuganosora/community/mediaproxy"
	"gorm.io/gorm"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

type Status struct {
	EmojiReactions     []*Reaction    `json:"emoji_reactions"`
	Id                 string         `json:"id"`
	CreatedAt          *APITime       `json:"created_at"`
	InReplyToId        *string        `json:"in_reply_to_id"`
	InReplyToAccountId *string        `json:"in_reply_to_account_id"`
	Sensitive          bool           `json:"sensitive"`
	SpoilerText        string         `json:"spoiler_text"`
	Visibility         string         `json:"visibility"`
	Language           *string        `json:"language"`
	Uri                string         `json:"uri"`
	Url                string         `json:"url"`
	RepliesCount       uint64         `json:"replies_count"`
	ReblogsCount       uint64         `json:"reblogs_count"`
	FavouritesCount    uint64         `json:"favourites_count"`
	QuotesCount        uint64         `json:"quotes_count"`
	EditedAt           *APITime       `json:"edited_at"`
	Favourited         bool           `json:"favourited"`
	Reblogged          bool           `json:"reblogged"`
	Muted              bool           `json:"muted"`
	Bookmarked         bool           `json:"bookmarked"`
	Pinned             bool           `json:"pinned"`
	Content            string         `json:"content"`
	Filtered           []FilterResult `json:"filtered"`
	Reblog             *Status        `json:"reblog"`
	Quote              *Status        `json:"quote,omitempty"`
	Application        *Application   `json:"application"`
	Account            *Account       `json:"account"`
	MediaAttachments   []*Attachment  `json:"media_attachments"`
	Mentions           []*Mention     `json:"mentions"`
	Tags               []*Tag         `json:"tags"`
	Emojis             []*Emoji       `json:"emojis"`
	Card               Card           `json:"card"`
	Poll               *Poll          `json:"poll"`
	intId              uint64         `json:"-"`
}

func ToStatusBase(status *models.Status) (ret *Status, err error) {
	return toStatusBase(status, nil)
}
func toStatusBase(status *models.Status, accounts *accountSerialization) (ret *Status, err error) {
	if status == nil {
		return ret, err
	}

	ret = &Status{EmojiReactions: make([]*Reaction, 0), Filtered: []FilterResult{}}
	ret.Id = strconv.FormatUint(status.Id, 10)
	ret.intId = status.Id
	ret.CreatedAt = NewAPITime(status.CreatedAt)

	if status.InReplyToId != nil && *status.InReplyToId > 0 {
		ret.InReplyToId = new(string)
		*ret.InReplyToId = strconv.FormatUint(*status.InReplyToId, 10)
	}

	if status.InReplyToAccountId != nil && *status.InReplyToAccountId > 0 {
		ret.InReplyToAccountId = new(string)
		*ret.InReplyToAccountId = strconv.FormatUint(*status.InReplyToAccountId, 10)
	}

	ret.Sensitive = status.Sensitive
	for _, a := range status.Attachments {
		if a != nil && a.Sensitive {
			ret.Sensitive = true

			break
		}
	}
	ret.SpoilerText = status.SpoilerTextPlain()
	if ret.SpoilerText != "" {
		ret.Sensitive = true // like Mastodon: a content warning implies sensitive
	}
	ret.Visibility = status.GetVisibilityString()

	ret.Language = normalizeLanguage(status.Language)
	ret.Uri = status.ObjectURI(systemsettings.GetBaseURL())

	if status.OriginUri != nil && isAbsoluteURL(*status.OriginUri) {
		ret.Url = *status.OriginUri
	} else if status.Account != nil {
		ret.Url = fmt.Sprintf("%s/@%s/%d", systemsettings.GetBaseURL(), status.Account.Username, status.Id)
	} else {
		// author not loaded: fall back to the canonical object URI
		ret.Url = ret.Uri
	}

	ret.RepliesCount = status.RepliesCount
	ret.ReblogsCount = status.ReblogsCount
	ret.FavouritesCount = status.FavouritesCount

	if status.Edited && status.UpdatedAt != nil && status.CreatedAt != nil && status.UpdatedAt.Unix() > status.CreatedAt.Unix() {
		ret.EditedAt = NewAPITime(status.UpdatedAt)
	}

	ret.Content = status.ContentHTML(contentRenderOptions())

	if status.Reblog != nil && (status.Reblog.Id > 0 || status.Reblog.Account != nil) {
		ret.Reblog, err = toStatusBase(status.Reblog, accounts)
		if err != nil {
			return ret, err
		}
	}

	if status.Quote != nil && status.Quote.Id > 0 {
		ret.Quote, err = toStatusBase(status.Quote, accounts)
		if err != nil {
			return ret, err
		}
	}

	ret.MediaAttachments = ToAttachments(status.Attachments)
	if status.Application != nil || status.Domain == nil {
		ret.Application = ToApplication(status.Application)
	}
	ret.Mentions = ToMentions(status.ToAccounts)
	ret.Tags = ToTags(status.Tags)
	emojiDomain := ""
	if status.Domain != nil {
		emojiDomain = *status.Domain
	}
	ret.Emojis = ToEmojis(models.EmojisInText(status.Emojis, ret.Content+" "+ret.SpoilerText, emojiDomain, false))
	ret.Card = proxiedCard(status.Content.Card)
	if accounts == nil {
		ret.Account = ToAccount(status.Account)
	} else {
		ret.Account = accounts.account(status.Account)
	}

	return ret, err
}

func ToStatus(status *models.Status, currentAccount *models.Account) (ret *Status, err error) {
	if err = models.AttachQuotes(db.GetDB(), []*models.Status{status}); err != nil {
		return nil, err
	}
	models.AttachEditFlags(db.GetDB(), []*models.Status{status})
	ret, err = ToStatusBase(status)
	if err != nil {
		return ret, err
	}

	ret.EmojiReactions, err = StatusReactions(status.Id, currentAccount, false)
	if err != nil {
		return ret, err
	}

	if status.Reblog != nil && status.Reblog.Id > 0 {
		ret.Reblog, err = ToStatus(status.Reblog, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if ret.Quote != nil && status.Quote != nil {
		visible, viewErr := status.Quote.CanViewEx(db.GetDB(), currentAccount)
		if viewErr != nil {
			return ret, viewErr
		}
		if !visible {
			ret.Quote = nil
		}
	}

	if status.Poll != nil {
		ret.Poll, err = ToPoll(status.Poll, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if currentAccount == nil {
		return ret, err
	}

	dbConn := db.GetDB()

	if status.ReblogId == nil || *status.ReblogId == 0 {
		var isPined, isReblogged, isFavourited, isMuted, isBookmarked bool
		if isPined, err = status.IsPinned(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isReblogged, err = status.IsReblogged(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isFavourited, err = status.IsFavourited(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isMuted, err = status.IsMuted(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isBookmarked, err = status.IsBookmarked(dbConn, currentAccount); err != nil {
			return ret, err
		}

		ret.Pinned = isPined
		ret.Reblogged = isReblogged
		ret.Favourited = isFavourited
		ret.Muted = isMuted
		ret.Bookmarked = isBookmarked
	}

	filters, err := models.GetFilters(dbConn, currentAccount.Id)
	if err != nil {
		return ret, err
	}

	ret.Filtered = ToFilterResults(filters.GetStatusFiltersIn(status, currentAccount, filterContextName(dbConn)))

	return ret, err
}

func ToStatuses(statuses models.Statuses, currentAccount *models.Account) ([]*Status, error) {
	return toStatusesWithDB(db.GetDB(), statuses, currentAccount)
}

// ToStatusesContext propagates the request deadline/cancellation through every
// serializer query without changing the legacy API for non-request callers.
func ToStatusesContext(ctx context.Context, statuses models.Statuses, currentAccount *models.Account) ([]*Status, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn := db.GetDB()
	if conn != nil {
		conn = conn.WithContext(ctx)
	}
	return toStatusesWithDB(conn, statuses, currentAccount)
}
func toStatusesWithDB(conn *gorm.DB, statuses models.Statuses, currentAccount *models.Account) (ret []*Status, err error) {
	ret = make([]*Status, 0, len(statuses))
	if len(statuses) == 0 {
		return ret, nil
	}
	statuses, err = models.FilterVisibleStatuses(conn, currentAccount, statuses)
	if err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return ret, nil
	}
	flat := make(models.Statuses, 0, len(statuses))
	seen := map[*models.Status]bool{}
	var collect func(*models.Status)
	collect = func(s *models.Status) {
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		flat = append(flat, s)
		collect(s.Reblog)
	}
	for _, s := range statuses {
		collect(s)
	}
	if err = models.AttachQuotes(conn, statuses); err != nil {
		return nil, err
	}
	models.AttachEditFlags(conn, statuses)
	ids := make([]uint64, 0, len(flat))
	authors := make([]*models.Account, 0, len(flat))
	polls := make([]*models.Poll, 0)
	uniqueIDs := map[uint64]bool{}
	for _, s := range flat {
		if !uniqueIDs[s.Id] {
			ids = append(ids, s.Id)
			uniqueIDs[s.Id] = true
		}
		authors = append(authors, s.Account)
		if s.Poll != nil {
			polls = append(polls, s.Poll)
		}
	}
	accounts, err := newAccountSerializationWithDB(conn, authors)
	if err != nil {
		return nil, err
	}
	reactions, err := statusReactionsBatchWithDB(conn, ids, currentAccount)
	if err != nil {
		return nil, err
	}
	filters, err := models.GetFilters(conn, func() uint64 {
		if currentAccount == nil {
			return 0
		}
		return currentAccount.Id
	}())
	if err != nil {
		return nil, err
	}
	pinned := map[uint64]bool{}
	favourited := map[uint64]bool{}
	reblogged := map[uint64]bool{}
	bookmarked := map[uint64]bool{}
	muted := map[uint64]bool{}
	mutedAuthors := map[uint64]bool{}
	if currentAccount != nil {
		// Fetch independent viewer flags in one round trip. UNION ALL preserves
		// each flag's identity even when the same status has several interactions.
		authorIDs := make([]uint64, 0, len(authors))
		seenAuthors := map[uint64]bool{}
		for _, a := range authors {
			if a != nil && !seenAuthors[a.Id] {
				authorIDs = append(authorIDs, a.Id)
				seenAuthors[a.Id] = true
			}
		}
		var flags []struct {
			Kind     string
			ObjectID uint64
		}
		err := conn.Raw(`SELECT 'pinned' AS kind, status_id AS object_id FROM pin_statuses WHERE account_id = ? AND status_id IN ?
UNION ALL SELECT 'favourited', status_id FROM favorite_statuses WHERE account_id = ? AND status_id IN ?
UNION ALL SELECT 'reblogged', reblog_id FROM statuses WHERE account_id = ? AND reblog_id IN ?
UNION ALL SELECT 'bookmarked', status_id FROM bookmark_statuses WHERE account_id = ? AND status_id IN ?
UNION ALL SELECT 'muted', status_id FROM mute_statuses WHERE account_id = ? AND status_id IN ?
UNION ALL SELECT 'muted_author', target_id FROM mute_accounts WHERE account_id = ? AND target_id IN ? AND (expired_at IS NULL OR expired_at > ?)`,
			currentAccount.Id, ids, currentAccount.Id, ids, currentAccount.Id, ids,
			currentAccount.Id, ids, currentAccount.Id, ids, currentAccount.Id, authorIDs, time.Now()).Scan(&flags).Error
		if err != nil {
			return nil, err
		}
		destinations := map[string]map[uint64]bool{"pinned": pinned, "favourited": favourited, "reblogged": reblogged, "bookmarked": bookmarked, "muted": muted, "muted_author": mutedAuthors}
		for _, flag := range flags {
			destinations[flag.Kind][flag.ObjectID] = true
		}

	}
	serializedPolls, err := toPollsMapByStatusIdWithDB(conn, polls, currentAccount)
	if err != nil {
		return nil, err
	}
	var apply func(*models.Status, *Status)
	apply = func(s *models.Status, out *Status) {
		if s == nil || out == nil {
			return
		}
		out.EmojiReactions = reactions[s.Id]
		out.Pinned = pinned[s.Id]
		out.Favourited = favourited[s.Id]
		out.Reblogged = reblogged[s.Id]
		out.Bookmarked = bookmarked[s.Id]
		out.Muted = muted[s.Id] || mutedAuthors[s.AccountId] || (s.Reblog != nil && mutedAuthors[s.Reblog.AccountId])
		out.Filtered = ToFilterResults(filters.GetStatusFiltersIn(s, currentAccount, filterContextName(conn)))
		out.Poll = serializedPolls[s.Id]
		apply(s.Reblog, out.Reblog)
	}
	for _, s := range statuses {
		if conn != nil {
			if err := conn.Statement.Context.Err(); err != nil {
				return nil, err
			}
		}
		out, e := toStatusBase(s, accounts)
		if e != nil {
			return nil, e
		}
		if err = clearHiddenQuote(conn, s, out, currentAccount); err != nil {
			return nil, err
		}
		if s.Reblog != nil && out.Reblog != nil {
			if err = clearHiddenQuote(conn, s.Reblog, out.Reblog, currentAccount); err != nil {
				return nil, err
			}
		}
		apply(s, out)
		ret = append(ret, out)
	}
	return ret, nil
}

func filterContextName(conn *gorm.DB) string {
	if conn == nil || conn.Statement == nil {
		return ""
	}
	return models.FilterContextOf(conn.Statement.Context)
}

// contentRenderOptions returns the options used to render local plain text
// (status content, profile notes) as HTML for this instance.
func contentRenderOptions() htmlsafe.RenderOptions {
	return htmlsafe.OptionsForBaseURL(systemsettings.GetBaseURL())
}

func clearHiddenQuote(conn *gorm.DB, src *models.Status, out *Status, viewer *models.Account) error {
	if out == nil || out.Quote == nil || src == nil || src.Quote == nil {
		return nil
	}
	visible, err := src.Quote.CanViewEx(conn, viewer)
	if err != nil {
		return err
	}
	if !visible {
		out.Quote = nil
	}
	return nil
}

// proxiedCard returns the strictly typed Mastodon card (nil when the stored
// card is empty or has no URL), with a remote preview image routed through
// the media proxy. The stored map is not modified.
func proxiedCard(card map[string]any) Card {
	rawURL, _ := card["url"].(string)
	if !isAbsoluteURL(rawURL) {
		return nil
	}

	str := func(k string) string { v, _ := card[k].(string); return v }
	num := func(k string) int {
		switch v := card[k].(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		}

		return 0
	}

	cardType := str("type")
	switch cardType {
	case "link", "photo", "video", "rich":
	default:
		cardType = "link"
	}

	var image any

	img := str("image")
	if strings.HasPrefix(img, "/") && !strings.HasPrefix(img, "//") {
		img = strings.TrimRight(systemsettings.GetBaseURL(), "/") + img
	}

	if isAbsoluteURL(img) {
		image = mediaproxy.URL(mediaproxy.KindPreview, img)
	}

	return Card{
		"url": rawURL, "title": str("title"), "description": str("description"), "language": nil,
		"type": cardType, "author_name": str("author_name"), "author_url": str("author_url"),
		"provider_name": str("provider_name"), "provider_url": str("provider_url"),
		"html": str("html"), "width": num("width"), "height": num("height"),
		"image": image, "image_description": str("image_description"), "embed_url": str("embed_url"),
		"blurhash": nil, "published_at": nil,
	}
}

var languageRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// normalizeLanguage returns nil for empty or malformed language tags:
// Mastodon's language is null or an ISO code, never "".
func normalizeLanguage(l string) *string {
	l = strings.TrimSpace(l)
	if !languageRe.MatchString(l) {
		return nil
	}

	return &l
}

func isAbsoluteURL(raw string) bool {
	u, err := url.Parse(raw)

	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
