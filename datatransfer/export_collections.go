package datatransfer

import (
	"encoding/json"
	"errors"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func exportPersonalCollections(db *gorm.DB, owner *models.Account, b *Bundle, accounts map[uint64]bool, posts map[uint64]bool, baseURL string) error {
	preserve := func(kind, id string, v any) {
		data, _ := json.Marshal(v)
		b.Records = append(b.Records, PreservedRecord{Kind: kind, ID: id, OwnerID: b.OwnerID, Data: data})
	}
	var follows []models.Follow
	if err := db.Where("account_id = ?", owner.Id).Find(&follows).Error; err != nil {
		return err
	}
	for _, v := range follows {
		kind := "follow"
		if v.Status != 0 {
			kind = "follow_request"
		}
		accounts[v.FollowId] = true
		b.Relations = append(b.Relations, Relation{Kind: kind, AccountID: b.OwnerID, TargetID: exportID(v.FollowId), CreatedAt: exportTime(v.CreatedAt)})
		preserve("follow_options", exportID(v.Id), v.Properties)
	}
	var followers []models.Follow
	if err := db.Where("follow_id = ?", owner.Id).Find(&followers).Error; err != nil {
		return err
	}
	for _, v := range followers {
		preserve("follower", exportID(v.Id), v)
	}
	var blocks []models.Block
	if err := db.Where("account_id = ?", owner.Id).Find(&blocks).Error; err != nil {
		return err
	}
	for _, v := range blocks {
		r := Relation{Kind: "block", AccountID: b.OwnerID, CreatedAt: exportTime(v.CreatedAt)}
		if v.Type == 1 {
			r.Kind = "domain_block"
			r.TargetDomain = v.TargetDomain
		} else {
			accounts[v.TargetAccountId] = true
			r.TargetID = exportID(v.TargetAccountId)
		}
		b.Relations = append(b.Relations, r)
	}
	var mutes []models.MuteAccount
	if err := db.Where("account_id = ?", owner.Id).Find(&mutes).Error; err != nil {
		return err
	}
	for _, v := range mutes {
		accounts[v.TargetId] = true
		b.Relations = append(b.Relations, Relation{Kind: "mute", AccountID: b.OwnerID, TargetID: exportID(v.TargetId), CreatedAt: exportTime(v.CreatedAt)})
		preserve("mute_options", exportID(v.Id), v)
	}
	var lists []models.List
	if err := db.Where("account_id = ?", owner.Id).Preload("Accounts").Find(&lists).Error; err != nil {
		return err
	}
	for _, v := range lists {
		l := List{ID: exportID(v.Id), OwnerID: b.OwnerID, Title: v.Title, RepliesPolicy: v.GetRepliesPolicy()}
		for _, a := range v.Accounts {
			accounts[a.Id] = true
			l.MemberIDs = append(l.MemberIDs, exportID(a.Id))
		}
		b.Lists = append(b.Lists, l)
	}
	var favorites []models.FavoriteStatus
	if err := db.Where("account_id = ?", owner.Id).Find(&favorites).Error; err != nil {
		return err
	}
	for _, v := range favorites {
		if posts[v.StatusId] {
			b.Reactions = append(b.Reactions, Reaction{ID: "favorite:" + exportID(v.Id), AccountID: b.OwnerID, PostID: exportID(v.StatusId), CreatedAt: exportTime(v.CreatedAt)})
		} else {
			preserve("favorite", exportID(v.Id), v)
		}
	}
	var reactions []models.StatusReaction
	if err := db.Where("account_id = ?", owner.Id).Find(&reactions).Error; err != nil {
		return err
	}
	for _, v := range reactions {
		if posts[v.StatusID] {
			b.Reactions = append(b.Reactions, Reaction{ID: "reaction:" + exportID(v.ID), AccountID: b.OwnerID, PostID: exportID(v.StatusID), Emoji: v.Name, CreatedAt: v.CreatedAt})
		} else {
			preserve("reaction", exportID(v.ID), v)
		}
	}
	var bookmarks []models.BookmarkStatus
	if err := db.Where("account_id = ?", owner.Id).Find(&bookmarks).Error; err != nil {
		return err
	}
	for _, v := range bookmarks {
		if posts[v.StatusId] {
			b.Bookmarks = append(b.Bookmarks, Bookmark{AccountID: b.OwnerID, PostID: exportID(v.StatusId)})
		} else {
			preserve("bookmark", exportID(v.Id), v)
		}
	}
	var pins []models.PinStatus
	if err := db.Where("account_id = ?", owner.Id).Find(&pins).Error; err != nil {
		return err
	}
	for _, v := range pins {
		for i := range b.Posts {
			if b.Posts[i].ID == exportID(v.StatusId) {
				b.Posts[i].Pinned = true
			}
		}
	}
	var filters []models.Filter
	if err := db.Where("account_id = ?", owner.Id).Find(&filters).Error; err != nil {
		return err
	}
	for _, v := range filters {
		preserve("filter", exportID(v.Id), v)
	}
	var muted []models.MuteStatus
	if err := db.Where("account_id = ?", owner.Id).Find(&muted).Error; err != nil {
		return err
	}
	for _, v := range muted {
		preserve("muted_status", exportID(v.Id), v)
	}
	var settings []models.UserSetting
	if err := db.Where("user_id = ?", owner.UserId).Find(&settings).Error; err != nil {
		return err
	}
	for _, v := range settings {
		preserve("preferences", v.ID, v.Data)
	}
	var votes []models.PollOptionVote
	if err := db.Where("account_id = ?", owner.Id).Find(&votes).Error; err != nil {
		return err
	}
	for _, v := range votes {
		preserve("poll_vote", exportID(v.Id), v)
	}
	// Notifications retain only records addressed to this owner; associated report
	// bodies, other users' private statuses and credentials are never loaded.
	var notifications []models.Notification
	if err := db.Where("account_id = ?", owner.Id).Find(&notifications).Error; err != nil {
		return err
	}
	for _, v := range notifications {
		preserve("notification", exportID(v.Id), v)
	}
	var histories []models.StatusHistory
	if err := db.Where("account_id = ? AND status_id IN (SELECT id FROM statuses WHERE account_id = ? AND deleted_at IS NULL)", owner.Id, owner.Id).Find(&histories).Error; err != nil {
		return err
	}
	for _, v := range histories {
		preserve("status_history", exportID(v.Id), v)
	}
	var conversations []models.AccountConversation
	if err := db.Where("account_id = ?", owner.Id).Find(&conversations).Error; err != nil {
		return err
	}
	for _, v := range conversations {
		preserve("conversation", exportID(v.Id), v)
	}
	var imported []StoredPreservedRecord
	if err := db.Where("owner_account_id = ? AND owner_user_id = ? AND scope = ?", owner.Id, owner.UserId, "personal").Find(&imported).Error; err != nil {
		return err
	}
	exportedMedia := map[string]bool{}
	for _, m := range b.Media {
		exportedMedia[m.ID] = true
	}
	for _, v := range imported {
		content := v.Data
		if v.Kind == "post" {
			var post Post
			if err := json.Unmarshal(v.Data, &post); err == nil && post.ID != "" && post.AuthorID != "" {
				for index, sourceID := range post.MediaIDs {
					var receipt ImportReceipt
					err := db.Where("namespace = ? AND kind = ? AND source_id = ? AND archived = ?", v.Namespace, "media", sourceID, false).First(&receipt).Error
					mapped := ""
					if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					if err == nil && receipt.TargetID > 0 && exportedMedia[exportID(receipt.TargetID)] {
						var media models.MediaAttachment
						err = db.Where("id = ? AND account_id = ?", receipt.TargetID, owner.Id).First(&media).Error
						if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
							return err
						}
						if err == nil {
							mapped = exportID(media.Id)
						}
					}
					if mapped != "" {
						post.MediaIDs[index] = mapped
					} else {
						post.MediaIDs[index] = "unresolved:" + sourceID
						parserIssue(b, "warning", "archived_media_reference_missing", "post", post.ID, "An archived attachment lacks an owned media mapping and remains unavailable.")
					}
				}
				content = raw(post)
			}
		}
		b.Records = append(b.Records, PreservedRecord{Kind: v.Kind, ID: "imported:" + exportID(v.ID), OwnerID: b.OwnerID, Data: content})
	}
	if len(b.Records) > 0 {
		parserIssue(b, "info", "archival_collections", "record", "", "Collections that cannot safely recreate live actions are preserved privately without notifications or federation.")
	}
	return nil
}
