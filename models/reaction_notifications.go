package models

import (
	"context"
	"github.com/kasuganosora/community/event"
	"gorm.io/gorm"
)

type reactionNotificationsKey struct{}
type reactionNotifications struct{ created []*Notification }

// An inbox transaction can enclose the model's transaction. Reuse its buffer
// so notification broadcasts only occur after the outermost successful commit.
func BeginReactionNotifications(ctx context.Context) (context.Context, func()) {
	if _, ok := ctx.Value(reactionNotificationsKey{}).(*reactionNotifications); ok {
		return ctx, func() {}
	}
	buffer := &reactionNotifications{}
	return context.WithValue(ctx, reactionNotificationsKey{}, buffer), func() {
		for _, n := range buffer.created {
			event.Trigger("notification", event.Event{Name: "after_create", Data: n})
		}
		buffer.created = nil
	}
}
func deleteReactionNotification(tx *gorm.DB, actorID, statusID uint64) error {
	if !tx.Migrator().HasTable(&Notification{}) {
		return nil
	}
	return tx.Session(&gorm.Session{SkipHooks: true}).Where("type = ? AND from_account_id = ? AND status_id = ?", NotificationTypeEmojiReaction, actorID, statusID).Delete(&Notification{}).Error
}
func createReactionNotification(tx *gorm.DB, row *StatusReaction, status *Status, actor *Account) error {
	if actor.Id == status.AccountId || status.Account == nil || status.Account.Domain != nil || !tx.Migrator().HasTable(&Notification{}) {
		return nil
	}
	notification := &Notification{Type: NotificationTypeEmojiReaction, ActivityType: "StatusReaction", AccountId: status.AccountId, FromAccountId: actor.Id, StatusId: status.Id}
	if err := tx.Session(&gorm.Session{SkipHooks: true}).Create(notification).Error; err != nil {
		return err
	}
	if buffer, ok := tx.Statement.Context.Value(reactionNotificationsKey{}).(*reactionNotifications); ok {
		buffer.created = append(buffer.created, notification)
	}
	return nil
}
