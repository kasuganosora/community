package serializer

import (
	"strconv"
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// ReplyPreview is one level of context, never a recursively expanded thread.
// It deliberately contains no media URLs: opening the original post retains
// its normal content-warning and sensitive-media controls.
type ReplyPreview struct {
	Id          string         `json:"id"`
	Account     *Account       `json:"account"`
	CreatedAt   *APITime       `json:"created_at"`
	Content     string         `json:"content"`
	SpoilerText string         `json:"spoiler_text"`
	Sensitive   bool           `json:"sensitive"`
	Emojis      []*Emoji       `json:"emojis"`
	Filtered    []FilterResult `json:"filtered"`
}

func attachReplyPreviews(conn *gorm.DB, statuses []*Status, viewer *models.Account) error {
	targets := map[uint64][]*Status{}
	for _, out := range statuses {
		if out.Reblog != nil {
			out = out.Reblog
		}
		if out.InReplyToId == nil {
			continue
		}
		id, err := strconv.ParseUint(*out.InReplyToId, 10, 64)
		if err == nil && id != 0 && *out.InReplyToId != out.Id {
			targets[id] = append(targets[id], out)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	var parents models.Statuses
	// Explicit preloads keep this bounded regardless of the number of replies.
	query := conn.Session(&gorm.Session{SkipHooks: true}).Preload("Account").Preload("Account.Extra").Preload("Content").Preload("Emojis").Where("id IN ? AND deleted_at IS NULL AND (reblog_id IS NULL OR reblog_id = 0)", ids)
	if viewer != nil {
		query = query.Where(`NOT EXISTS (SELECT 1 FROM mute_accounts m WHERE m.account_id = ? AND m.target_id = statuses.account_id AND (m.expired_at IS NULL OR m.expired_at > ?)) AND NOT EXISTS (SELECT 1 FROM mute_statuses m WHERE m.account_id = ? AND m.status_id = statuses.id)`, viewer.Id, time.Now(), viewer.Id)
	}
	if err := query.Find(&parents).Error; err != nil {
		return err
	}
	parents, err := models.FilterVisibleStatuses(conn, viewer, parents)
	if err != nil {
		return err
	}
	if len(parents) == 0 {
		return nil
	}
	authors := make([]*models.Account, 0, len(parents))
	for _, parent := range parents {
		authors = append(authors, parent.Account)
	}
	accounts, err := newAccountSerializationWithDB(conn, authors)
	if err != nil {
		return err
	}
	viewerID := uint64(0)
	if viewer != nil {
		viewerID = viewer.Id
	}
	filters, err := models.GetFilters(conn, viewerID)
	if err != nil {
		return err
	}
	for _, parent := range parents {
		domain := ""
		if parent.Domain != nil {
			domain = *parent.Domain
		}
		content, spoiler := parent.ContentHTML(contentRenderOptions()), parent.SpoilerTextPlain()
		preview := &ReplyPreview{
			Id: strconv.FormatUint(parent.Id, 10), Account: accounts.account(parent.Account), CreatedAt: NewAPITime(parent.CreatedAt),
			Content: content, SpoilerText: spoiler, Sensitive: parent.Sensitive || spoiler != "",
			Emojis:   ToEmojis(models.EmojisInText(parent.Emojis, content+" "+spoiler, domain, false)),
			Filtered: ToFilterResults(filters.GetStatusFiltersIn(parent, viewer, filterContextName(conn))),
		}
		for _, out := range targets[parent.Id] {
			out.Reply = preview
		}
	}
	return nil
}
