package serializer

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"strconv"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

type Status struct {
	EmojiReactions     []*Reaction   `json:"emoji_reactions"`
	Id                 string        `json:"id"`
	CreatedAt          *time.Time    `json:"created_at"`
	InReplyToId        *string       `json:"in_reply_to_id"`
	InReplyToAccountId *string       `json:"in_reply_to_account_id"`
	Sensitive          bool          `json:"sensitive"`
	SpoilerText        string        `json:"spoiler_text"`
	Visibility         string        `json:"visibility"`
	Language           string        `json:"language"`
	Uri                string        `json:"uri"`
	Url                string        `json:"url"`
	RepliesCount       uint64        `json:"replies_count"`
	ReblogsCount       uint64        `json:"reblogs_count"`
	FavouritesCount    uint64        `json:"favourites_count"`
	QuotesCount        uint64        `json:"quotes_count"`
	EditedAt           *time.Time    `json:"edited_at"`
	Favourited         bool          `json:"favourited"`
	Reblogged          bool          `json:"reblogged"`
	Muted              bool          `json:"muted"`
	Bookmarked         bool          `json:"bookmarked"`
	Pinned             bool          `json:"pinned"`
	Content            string        `json:"content"`
	Filtered           []string      `json:"filtered"`
	Reblog             *Status       `json:"reblog"`
	Quote              *Status       `json:"quote,omitempty"`
	Application        *Application  `json:"application"`
	Account            *Account      `json:"account"`
	MediaAttachments   []*Attachment `json:"media_attachments"`
	Mentions           []*Mention    `json:"mentions"`
	Tags               []*Tag        `json:"tags"`
	Emojis             []*Emoji      `json:"emojis"`
	Card               Card          `json:"card"`
	Poll               *Poll         `json:"poll"`
	intId              uint64        `json:"-"`
}

func ToStatusBase(status *models.Status) (ret *Status, err error) {
	return toStatusBase(status, nil)
}
func toStatusBase(status *models.Status, accounts *accountSerialization) (ret *Status, err error) {
	if status == nil {
		return ret, err
	}

	ret = &Status{EmojiReactions: make([]*Reaction, 0)}
	ret.Id = strconv.FormatUint(status.Id, 10)
	ret.intId = status.Id
	ret.CreatedAt = status.CreatedAt

	if status.InReplyToId != nil && *status.InReplyToId > 0 {
		ret.InReplyToId = new(string)
		*ret.InReplyToId = strconv.FormatUint(*status.InReplyToId, 10)
	}

	if status.InReplyToAccountId != nil && *status.InReplyToAccountId > 0 {
		ret.InReplyToAccountId = new(string)
		*ret.InReplyToAccountId = strconv.FormatUint(*status.InReplyToAccountId, 10)
	}

	ret.Sensitive = status.Sensitive
	ret.SpoilerText = status.SpoilerTextPlain()
	ret.Visibility = status.GetVisibilityString()

	ret.Language = status.Language
	ret.Uri = status.ObjectURI(systemsettings.GetBaseURL())

	if status.OriginUri != nil {
		ret.Url = *status.OriginUri
	} else {
		ret.Url = fmt.Sprintf("%s/@%s/%d", systemsettings.GetBaseURL(), status.Account.Username, status.Id)
	}

	ret.RepliesCount = status.RepliesCount
	ret.ReblogsCount = status.ReblogsCount
	ret.FavouritesCount = status.FavouritesCount

	if status.UpdatedAt != nil && status.UpdatedAt.Unix() > status.CreatedAt.Unix() {
		ret.EditedAt = status.UpdatedAt
	}

	ret.Content = status.ContentHTML(contentRenderOptions())

	ret.Reblog, err = toStatusBase(status.Reblog, accounts)
	if err != nil {
		return ret, err
	}

	if status.Quote != nil && status.Quote.Id > 0 {
		ret.Quote, err = toStatusBase(status.Quote, accounts)
		if err != nil {
			return ret, err
		}
	}

	ret.MediaAttachments = ToAttachments(status.Attachments)
	ret.Application = ToApplication(status.Application)
	ret.Mentions = ToMentions(status.ToAccounts)
	ret.Tags = ToTags(status.Tags)
	emojiDomain := ""
	if status.Domain != nil {
		emojiDomain = *status.Domain
	}
	ret.Emojis = ToEmojis(models.EmojisInText(status.Emojis, ret.Content+" "+ret.SpoilerText, emojiDomain, false))
	ret.Card = status.Content.Card
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
	ret, err = ToStatusBase(status)
	if err != nil {
		return ret, err
	}

	ret.EmojiReactions, err = StatusReactions(status.Id, currentAccount, false)
	if err != nil {
		return ret, err
	}

	if status.Reblog != nil {
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

	ret.Filtered = filters.GetStatusFiltersIn(status, currentAccount, filterContextName(dbConn)).GetFilterActionsName()

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
		out.Filtered = filters.GetStatusFiltersIn(s, currentAccount, filterContextName(conn)).GetFilterActionsName()
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
