package models

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// QueueFeedStatus runs after associations are saved (or before deletion). The
// row lock and revision increment belong to the caller's mutation transaction.
func QueueFeedStatus(tx *gorm.DB, kind string, status *Status) error {
	if status.Id == 0 || skipFeedHooks(tx) {
		return nil
	}
	tx = tx.Session(&gorm.Session{NewDB: true})
	var current Status
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, status.Id).Error; err != nil {
		if kind == FeedStatusDelete && errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if current.ScheduledAt != nil && kind != FeedStatusDelete {
		return nil
	}
	if current.DeletedAt != nil {
		kind = FeedStatusDelete
	}
	revision := current.FeedRevision + 1
	if err := tx.Model(&Status{}).Where("id = ?", current.Id).UpdateColumn("feed_revision", revision).Error; err != nil {
		return err
	}
	status.FeedRevision = revision
	metadata := FeedEventMetadata{ActorID: current.AccountId, Visibility: current.Visibility, LocalOnly: current.LocalOnly, Local: current.IsLocal()}
	if current.ReblogId != nil {
		metadata.ReblogID = *current.ReblogId
	}
	if current.InReplyToId != nil {
		metadata.ReplyID = *current.InReplyToId
	}
	if current.ConversationId != nil {
		metadata.ConversationID = *current.ConversationId
	}
	// Deletion snapshots must be captured before join-table cleanup. Do not use
	// an in-memory Status whose associations may describe the previous version.
	if err := captureFeedAudience(tx, current.Id, &metadata); err != nil {
		return err
	}

	if tx.Migrator().HasTable("media_attachments") {
		var n int64
		if err := tx.Table("media_attachments").Where("status_id = ?", current.Id).Count(&n).Error; err != nil {
			return err
		}
		metadata.HasMedia = n > 0
	}
	if kind == FeedStatusRefresh {
		if prior, ok := tx.Statement.Context.Value(feedPreviousAudienceKey{}).(feedPreviousAudience); ok && prior.id == current.Id {
			metadata.PreviousRecipientIDs = append(metadata.PreviousRecipientIDs, prior.metadata.RecipientIDs...)
			metadata.PreviousTagNames = append(metadata.PreviousTagNames, prior.metadata.TagNames...)
		}
		if previous := status.feedPreviousMetadata; previous != nil {
			metadata.PreviousRecipientIDs = append(metadata.PreviousRecipientIDs, previous.RecipientIDs...)
			metadata.PreviousTagNames = append(metadata.PreviousTagNames, previous.TagNames...)
		}
		// AP handlers may have replaced joins before Save. The preceding durable
		// version retains that audience without trusting the mutated associations.
		if current.FeedRevision > 0 {
			var old FeedEvent
			result := tx.Where("event_key = ?", fmt.Sprintf("status:%d:%d", current.Id, current.FeedRevision)).Limit(1).Find(&old)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				metadata.PreviousRecipientIDs = append(metadata.PreviousRecipientIDs, old.Metadata.RecipientIDs...)
				metadata.PreviousTagNames = append(metadata.PreviousTagNames, old.Metadata.TagNames...)
			}
		}
	}
	status.feedPreviousMetadata = nil

	return tx.Create(&FeedEvent{EventKey: fmt.Sprintf("status:%d:%d", current.Id, revision), Kind: kind, ObjectType: "status", ObjectID: current.Id, ActorID: current.AccountId, ObjectVersion: revision, OperationID: feedOperation(tx), Metadata: metadata}).Error
}

func captureFeedAudience(tx *gorm.DB, id uint64, metadata *FeedEventMetadata) error {
	if tx.Migrator().HasTable("status_to_accounts") {
		if err := tx.Table("status_to_accounts").Where("status_id = ?", id).Order("account_id").Pluck("account_id", &metadata.RecipientIDs).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable("status_tags") {
		if err := tx.Table("status_tags").Where("status_id = ?", id).Order("tag_id").Pluck("tag_id", &metadata.TagIDs).Error; err != nil {
			return err
		}
	}
	if len(metadata.TagIDs) > 0 && tx.Migrator().HasTable("tags") {
		if err := tx.Table("tags").Where("id IN ?", metadata.TagIDs).Pluck("name", &metadata.TagNames).Error; err != nil {
			return err
		}
	}
	return nil
}

type feedPreviousAudienceKey struct{}
type feedPreviousAudience struct {
	id       uint64
	metadata FeedEventMetadata
}

// PreserveFeedStatusAudience must precede replacing content/recipient joins.
// Its context travels with the same SQL transaction; it stores IDs, not text.
func PreserveFeedStatusAudience(tx *gorm.DB, id uint64) (*gorm.DB, error) {
	if skipFeedHooks(tx) {
		return tx, nil
	}
	prior := feedPreviousAudience{id: id}
	if err := captureFeedAudience(tx.Session(&gorm.Session{NewDB: true}), id, &prior.metadata); err != nil {
		return nil, err
	}
	return tx.WithContext(context.WithValue(tx.Statement.Context, feedPreviousAudienceKey{}, prior)), nil
}
