package datatransfer

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (i *importer) importEmojis() error {
	for _, e := range i.bundle.Emojis {
		if i.personal() && e.Domain == "" {
			var existing models.Emoji
			if sameOrigin(i.bundle.Source.BaseURL, i.options.TargetBaseURL) {
				err := i.tx.Where("shortcode = ? AND domain = ? AND url = ? AND disabled = ?", e.Shortcode, "", e.URL, false).First(&existing).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if existing.Id != 0 {
				if _, err := i.apply("emoji", e.ID, e, func(tx *gorm.DB) (uint64, error) { return existing.Id, nil }); err != nil {
					return err
				}
				continue
			}
			// A personal import may reuse an exact local identity, but cannot
			// install a new instance-wide local emoji or trust an uploaded URL.
			if err := i.preserve("emoji", e.ID, e, "personal_unmatched_local_emoji"); err != nil {
				return err
			}
			i.report.Issues = append(i.report.Issues, Issue{Severity: "warning", Code: "personal_emoji_archived", Kind: "emoji", SourceID: e.ID, Message: "local emoji could not be safely matched; dependent content remains in the private archive"})
			continue
		}
		_, err := i.apply("emoji", e.ID, e, func(tx *gorm.DB) (uint64, error) {
			target := models.Emoji{Shortcode: e.Shortcode, Domain: e.Domain, Url: e.URL, StaticUrl: e.URL, Category: e.Category, Aliases: e.Aliases, Disabled: e.Disabled, IsSensitive: e.Sensitive, License: e.License, VisibleInPicker: !e.Disabled}
			if e.MediaID != "" {
				id, err := i.ref("media", e.MediaID)
				if err != nil {
					return 0, err
				}
				var media models.MediaAttachment
				if err = tx.First(&media, id).Error; err != nil {
					return 0, err
				}
				target.Url = strings.TrimRight(i.options.TargetBaseURL, "/") + fmt.Sprintf("/media/%d/%s", media.Id, url.PathEscape(media.FileName))
				target.StaticUrl = target.Url
				target.ContentType = media.ContentType
				target.FileSize = media.FileSize
			}
			var existing models.Emoji
			if err := tx.Where("shortcode = ? AND domain = ?", e.Shortcode, e.Domain).First(&existing).Error; err == nil {
				if existing.Url != target.Url {
					return 0, errors.New("emoji identity conflict")
				}
				return existing.Id, nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, err
			}
			err := tx.Create(&target).Error
			return target.Id, err
		})
		if err != nil {
			return err
		}
	}
	return i.importAccountEmojis()
}
func (i *importer) importExtras() error {
	for _, list := range i.bundle.Lists {
		unresolved := false
		for _, id := range list.MemberIDs {
			if i.ids["account"][id] == 0 {
				unresolved = true
			}
		}
		if i.personal() && unresolved {
			if err := i.preserve("list", list.ID, list, "personal_unresolved_member"); err != nil {
				return err
			}
			continue
		}
		_, err := i.apply("list", list.ID, list, func(tx *gorm.DB) (uint64, error) {
			owner, err := i.ref("account", list.OwnerID)
			if err != nil {
				return 0, err
			}
			target := models.List{AccountId: owner, Title: list.Title, Visibility: models.ListVisibilityPrivate}
			target.SetRepliesPolicy(list.RepliesPolicy)
			if err = tx.Create(&target).Error; err != nil {
				return 0, err
			}
			if err = tx.Create(&models.ListFollowAccount{ListId: target.Id, AccountId: owner}).Error; err != nil {
				return 0, err
			}
			seen := map[uint64]bool{}
			for _, ref := range list.MemberIDs {
				id, e := i.ref("account", ref)
				if e != nil {
					return 0, e
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				if err = tx.Create(&models.ListAccount{ListId: target.Id, AccountId: id}).Error; err != nil {
					return 0, err
				}
			}
			return target.Id, nil
		})
		if err != nil {
			return err
		}
		if list.Exclusive {
			if err = i.preserve("exclusive_list", list.ID, list, ""); err != nil {
				return err
			}
			i.report.Issues = append(i.report.Issues, Issue{Severity: "warning", Code: "exclusive_list_archived", Kind: "list", SourceID: list.ID, Message: "exclusive flag retained privately; target lists do not exclude home-feed posts"})
		}
	}
	for _, poll := range i.bundle.Polls {
		if i.ids["post"][poll.PostID] == 0 {
			if err := i.preserve("poll", poll.ID, poll, "post_not_published"); err != nil {
				return err
			}
			continue
		}
		_, err := i.apply("poll", poll.ID, poll, func(tx *gorm.DB) (uint64, error) {
			postID, _ := i.ref("post", poll.PostID)
			var post models.Status
			if err := tx.First(&post, postID).Error; err != nil {
				return 0, err
			}
			target := models.Poll{StatusId: postID, AccountId: post.AccountId, ExpiresAt: poll.ExpiresAt, Multiple: poll.Multiple}
			if err := tx.Create(&target).Error; err != nil {
				return 0, err
			}
			for idx, opt := range poll.Options {
				row := models.PollOption{PollId: target.Id, Index: uint8(idx), Content: opt.Title, Votes: opt.Votes}
				if err := tx.Create(&row).Error; err != nil {
					return 0, err
				}
			}
			return target.Id, nil
		})
		if err != nil {
			return err
		}
	}
	for _, vote := range i.bundle.PollVotes {
		id := fmt.Sprintf("%s/%s/%d", vote.PollID, vote.AccountID, vote.Option)
		if i.ids["poll"][vote.PollID] == 0 || i.personal() && vote.AccountID != i.bundle.OwnerID {
			reason := "poll_not_published"
			if i.ids["poll"][vote.PollID] != 0 {
				reason = "personal_foreign_account"
			}
			if err := i.preserve("poll_vote", id, vote, reason); err != nil {
				return err
			}
			continue
		}
		_, err := i.apply("poll_vote", id, vote, func(tx *gorm.DB) (uint64, error) {
			poll, _ := i.ref("poll", vote.PollID)
			account, err := i.ref("account", vote.AccountID)
			if err != nil {
				return 0, err
			}
			var option models.PollOption
			if err = tx.Where("poll_id = ?", poll).Where(clause.Eq{Column: "index", Value: vote.Option}).First(&option).Error; err != nil {
				return 0, err
			}
			row := models.PollOptionVote{PollId: poll, AccountId: account, OptionId: option.Id, CreatedAt: timePointer(vote.CreatedAt)}
			err = tx.Create(&row).Error
			return row.Id, err
		})
		if err != nil {
			return err
		}
	}
	for _, reaction := range i.bundle.Reactions {
		id := reaction.ID
		if id == "" {
			id = reaction.AccountID + "/" + reaction.PostID + "/" + reaction.Emoji
		}
		missingEmoji, err := i.reactionEmojiMissing(reaction.Emoji)
		if err != nil {
			return err
		}
		if missingEmoji || i.ids["post"][reaction.PostID] == 0 || i.personal() && reaction.AccountID != i.bundle.OwnerID {
			reason := "personal_foreign_account"
			switch {
			case i.ids["post"][reaction.PostID] == 0:
				reason = "post_not_published"
			case missingEmoji:
				reason = "custom_emoji_not_imported"
			}
			if err := i.preserve("reaction", id, reaction, reason); err != nil {
				return err
			}
			continue
		}
		_, err = i.apply("reaction", id, reaction, func(tx *gorm.DB) (uint64, error) {
			post, _ := i.ref("post", reaction.PostID)
			account, err := i.ref("account", reaction.AccountID)
			if err != nil {
				return 0, err
			}
			if reaction.Emoji == "" {
				row := models.FavoriteStatus{StatusId: post, AccountId: account, CreatedAt: timePointer(reaction.CreatedAt)}
				if err = tx.Create(&row).Error; err != nil {
					return 0, err
				}
				if err = tx.Model(&models.Status{}).Where("id = ?", post).UpdateColumn("favourites_count", gorm.Expr("favourites_count + 1")).Error; err != nil {
					return 0, err
				}
				return row.Id, nil
			}
			row := models.StatusReaction{StatusID: post, AccountID: account, Name: reaction.Emoji, CreatedAt: reaction.CreatedAt}
			if strings.HasPrefix(reaction.Emoji, ":") {
				parts := strings.SplitN(strings.Trim(reaction.Emoji, ":"), "@", 2)
				domain := ""
				if len(parts) == 2 {
					domain = parts[1]
					if domain == "." {
						domain = ""
					}
				}
				var emoji models.Emoji
				if err = tx.Where("shortcode = ? AND domain = ?", parts[0], domain).First(&emoji).Error; err != nil {
					return 0, err
				}
				row.EmojiID = emoji.Id
				if domain == "" {
					row.Name = ":" + emoji.Shortcode + ":"
				}
			} else if !models.ValidUnicodeReaction(reaction.Emoji) {
				return 0, errors.New("invalid emoji reaction")
			}
			err = tx.Create(&row).Error
			return row.ID, err
		})
		if err != nil {
			return err
		}
	}
	for _, bookmark := range i.bundle.Bookmarks {
		id := bookmark.AccountID + "/" + bookmark.PostID + "/" + bookmark.PostURI
		if i.ids["post"][bookmark.PostID] == 0 || i.personal() && bookmark.AccountID != i.bundle.OwnerID {
			reason := "personal_foreign_account"
			if i.ids["post"][bookmark.PostID] == 0 {
				reason = "post_not_published"
			}
			if err := i.preserve("bookmark", id, bookmark, reason); err != nil {
				return err
			}
			continue
		}
		_, err := i.apply("bookmark", id, bookmark, func(tx *gorm.DB) (uint64, error) {
			post, _ := i.ref("post", bookmark.PostID)
			account, err := i.ref("account", bookmark.AccountID)
			if err != nil {
				return 0, err
			}
			row := models.BookmarkStatus{AccountId: account, StatusId: post}
			err = tx.Create(&row).Error
			return row.Id, err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// reactionEmojiMissing reports a custom-emoji reaction whose emoji does not
// exist on the target (e.g. a remote emoji outside a local-only migration).
// Such reactions are archived instead of aborting the whole transaction.
func (i *importer) reactionEmojiMissing(name string) (bool, error) {
	if !strings.HasPrefix(name, ":") {
		return false, nil
	}
	parts := strings.SplitN(strings.Trim(name, ":"), "@", 2)
	domain := ""
	if len(parts) == 2 && parts[1] != "." {
		domain = parts[1]
	}
	var n int64
	if err := i.tx.Model(&models.Emoji{}).Where("shortcode = ? AND domain = ?", parts[0], domain).Count(&n).Error; err != nil {
		return false, err
	}
	return n == 0, nil
}

func (i *importer) importAccountEmojis() error {
	for _, a := range i.bundle.Accounts {
		if i.personal() && a.ID != i.bundle.OwnerID {
			continue
		}
		if len(a.EmojiIDs) == 0 {
			continue
		}
		accountID, err := i.ref("account", a.ID)
		if err != nil {
			return err
		}
		_, err = i.apply("profile_emojis", a.ID, a.EmojiIDs, func(tx *gorm.DB) (uint64, error) {
			if err := tx.Table("account_emojis").Where("account_id = ?", accountID).Delete(&models.AccountEmoji{}).Error; err != nil {
				return 0, err
			}
			seen := map[uint64]bool{}
			for _, sourceID := range a.EmojiIDs {
				emojiID := i.ids["emoji"][sourceID]
				if emojiID == 0 && i.personal() {
					continue
				}
				if emojiID == 0 {
					return 0, errors.New("profile emoji unavailable")
				}
				if seen[emojiID] {
					continue
				}
				seen[emojiID] = true
				if err := tx.Table("account_emojis").Create(map[string]any{"account_id": accountID, "emoji_id": emojiID}).Error; err != nil {
					return 0, err
				}
			}
			return accountID, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
