package datatransfer

import (
	"errors"
	"sort"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func visibility(s string) uint8 {
	switch s {
	case "unlisted":
		return models.StatusVisibilityUnlisted
	case "private", "followers":
		return models.StatusVisibilityPrivate
	case "direct":
		return models.StatusVisibilityDirect
	}
	return models.StatusVisibilityPublic
}
func (i *importer) importPosts() error {
	pending := append([]Post(nil), i.bundle.Posts...)
	sort.SliceStable(pending, func(a, b int) bool {
		if pending[a].CreatedAt.Equal(pending[b].CreatedAt) {
			return pending[a].ID < pending[b].ID
		}
		return pending[a].CreatedAt.Before(pending[b].CreatedAt)
	})
	for len(pending) > 0 {
		advanced := false
		var next []Post
		for _, p := range pending {
			if i.archivePost(p) {
				if err := i.preserve("post", p.ID, p); err != nil {
					return err
				}
				advanced = true
				continue
			}
			if p.ReplyID != "" && i.ids["post"][p.ReplyID] == 0 || p.ReblogID != "" && i.ids["post"][p.ReblogID] == 0 {
				next = append(next, p)
				continue
			}
			if err := i.importPost(p); err != nil {
				return err
			}
			advanced = true
		}
		if !advanced {
			if i.personal() {
				for _, p := range next {
					if err := i.preserve("post", p.ID, p); err != nil {
						return err
					}
					i.report.Issues = append(i.report.Issues, Issue{Severity: "warning", Code: "archived_parent", Kind: "post", SourceID: p.ID, Message: "post depends on private archived history and was not published"})
				}
				return nil
			}
			return errors.New("cyclic or archived live post reference")
		}
		pending = next
	}
	return nil
}
func (i *importer) importPost(p Post) error {
	_, err := i.apply("post", p.ID, p, func(tx *gorm.DB) (uint64, error) {
		author, err := i.ref("account", p.AuthorID)
		if err != nil {
			return 0, err
		}
		source := i.sourceAccount(p.AuthorID)
		target := models.Status{LocalOnly: p.LocalOnly, AccountId: author, Visibility: visibility(p.Visibility), Sensitive: p.Sensitive, Language: p.Language, CreatedAt: timePointer(p.CreatedAt), UpdatedAt: timePointer(p.UpdatedAt), Properties: map[string]any{}}
		if !i.personal() && source.Local {
			target.LocalObjectURI = p.URI
			if p.URL != "" {
				target.OriginUrl = &p.URL
			}
		} else if !i.personal() {
			target.OriginUri = &p.URI
			target.OriginUrl = &p.URL
			target.Domain = sourceDomain(source)
		}
		if p.ReplyID != "" {
			id, _ := i.ref("post", p.ReplyID)
			target.InReplyToId = &id
			var parent models.Status
			if err = tx.First(&parent, id).Error; err != nil {
				return 0, err
			}
			target.InReplyToAccountId = &parent.AccountId
			if !models.ValidateReplyVisibility(&parent, target.Visibility) {
				return 0, errors.New("reply widens parent audience")
			}
		}
		if p.ReblogID != "" {
			id, _ := i.ref("post", p.ReblogID)
			target.ReblogId = &id
		}
		if err = tx.Create(&target).Error; err != nil {
			return 0, err
		}
		text := p.Text
		if source.Local || i.personal() {
			if text == "" && p.HTML != "" {
				text = htmlsafe.StripTags(p.HTML)
				target.Properties["import_original_html"] = htmlsafe.Sanitize(p.HTML)
			}
		} else {
			if p.HTML != "" {
				text = htmlsafe.Sanitize(p.HTML)
			} else {
				text = htmlsafe.RenderPlainText(text, htmlsafe.RenderOptions{})
			}
		}
		content := models.StatusContent{StatusId: target.Id, Content: text, SpoilerText: p.SpoilerText}
		if err = tx.Create(&content).Error; err != nil {
			return 0, err
		}
		participants := map[uint64]bool{author: true}
		for _, ref := range p.RecipientIDs {
			id, e := i.ref("account", ref)
			if e != nil {
				return 0, e
			}
			if participants[id] {
				continue
			}
			participants[id] = true
			if err = tx.Create(&models.StatusToAccount{StatusId: target.Id, AccountId: id, CreatedAt: timePointer(p.CreatedAt)}).Error; err != nil {
				return 0, err
			}
		}
		if target.Visibility == models.StatusVisibilityDirect {
			audience := make([]uint64, 0, len(participants))
			for id := range participants {
				audience = append(audience, id)
			}
			sort.Slice(audience, func(a, b int) bool { return audience[a] < audience[b] })
			sourceConversation := p.ConversationID
			if sourceConversation == "" {
				sourceConversation = p.ID
			}
			audienceDigest, _ := digestValue(audience)
			conversationID, err := i.apply("conversation", sourceConversation+"/"+audienceDigest, audience, func(tx *gorm.DB) (uint64, error) {
				c := models.Conversation{AccountsIds: audience, CreatedAt: timePointer(p.CreatedAt)}
				if err := tx.Create(&c).Error; err != nil {
					return 0, err
				}
				for _, id := range audience {
					if err := tx.Create(&models.AccountConversation{AccountId: id, ConversationId: c.Id, LastStatusId: target.Id}).Error; err != nil {
						return 0, err
					}
				}
				return c.Id, nil
			})
			if err != nil {
				return 0, err
			}
			target.ConversationId = &conversationID
			var conversation models.Conversation
			if err = tx.First(&conversation, conversationID).Error; err != nil {
				return 0, err
			}
			conversation.StatusesIds = append(conversation.StatusesIds, target.Id)
			if err = tx.Model(&conversation).Select("statuses_ids").Updates(&conversation).Error; err != nil {
				return 0, err
			}
			if err = tx.Model(&models.AccountConversation{}).Where("conversation_id = ?", conversationID).UpdateColumn("last_status_id", target.Id).Error; err != nil {
				return 0, err
			}
		}
		for _, mediaRef := range p.MediaIDs {
			id, e := i.ref("media", mediaRef)
			if e != nil {
				return 0, e
			}
			var attachment models.MediaAttachment
			if err = tx.First(&attachment, id).Error; err != nil {
				return 0, err
			}
			if attachment.AccountId != author {
				return 0, errors.New("media ownership or attachment conflict")
			}
			if attachment.StatusId != nil {
				attachment.Id = 0
				attachment.StatusId = &target.Id
				err = tx.Create(&attachment).Error
			} else {
				err = tx.Model(&attachment).UpdateColumn("status_id", target.Id).Error
			}
			if err != nil {
				return 0, err
			}
		}
		for _, emojiRef := range p.EmojiIDs {
			id, e := i.ref("emoji", emojiRef)
			if e != nil {
				return 0, e
			}
			if err = tx.Create(&models.StatusEmoji{StatusId: target.Id, EmojiId: id}).Error; err != nil {
				return 0, err
			}
		}
		seenTags := map[uint64]bool{}
		for _, tag := range p.Tags {
			if !models.ValidTagName(models.NormalizeTagName(tag)) {
				continue
			}
			// Case-insensitive find-or-create: #OSINT and #osint are one tag.
			var model *models.Tag
			if model, err = models.FindOrCreateTag(tx, models.NormalizeTagName(tag)); err != nil {
				return 0, err
			}
			if seenTags[model.Id] {
				continue
			}
			seenTags[model.Id] = true
			if err = tx.Create(&models.StatusTag{StatusId: target.Id, TagId: model.Id}).Error; err != nil {
				return 0, err
			}
			if err = tx.Model(model).UpdateColumn("status_count", gorm.Expr("status_count + 1")).Error; err != nil {
				return 0, err
			}
		}
		if p.Pinned {
			if err = tx.Create(&models.PinStatus{AccountId: author, StatusId: target.Id}).Error; err != nil {
				return 0, err
			}
		}
		if target.InReplyToId != nil && target.ReblogId == nil {
			if err = tx.Model(&models.Status{}).Where("id = ?", *target.InReplyToId).UpdateColumn("replies_count", gorm.Expr("replies_count + 1")).Error; err != nil {
				return 0, err
			}
		}
		if target.ReblogId != nil {
			if err = tx.Model(&models.Status{}).Where("id = ?", *target.ReblogId).UpdateColumn("reblogs_count", gorm.Expr("reblogs_count + 1")).Error; err != nil {
				return 0, err
			}
		} else {
			if err = tx.Model(&models.AccountExtra{}).Where("account_id = ?", author).UpdateColumn("statuses_count", gorm.Expr("statuses_count + 1")).Error; err != nil {
				return 0, err
			}
		}
		if err = tx.Model(&target).Select("conversation_id", "properties").Updates(&target).Error; err != nil {
			return 0, err
		}
		return target.Id, nil
	})
	return err
}
