package models

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Mutations frequently use Where(...).Delete(&Model{}), whose hook receiver is
// empty. Read the actual affected rows before deletion instead of losing their
// audience IDs. This runs inside GORM's mutation transaction.
func feedAffectedRows[T any](tx *gorm.DB, model any, id uint64, rows *[]T) error {
	q := tx.Session(&gorm.Session{NewDB: true}).Model(model).Clauses(clause.Locking{Strength: "UPDATE"})
	if id > 0 {
		q = q.Where("id = ?", id)
	} else if where, ok := tx.Statement.Clauses["WHERE"]; ok {
		q = q.Where(where.Expression)
	}
	return q.Find(rows).Error
}
func queueFeedFollowChange(tx *gorm.DB, f *Follow) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []Follow
	if err := feedAffectedRows(tx, &Follow{}, f.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: row.AccountId, RecipientIDs: []uint64{row.AccountId}, TargetAccountID: row.FollowId, RelationshipID: row.Id, Reason: "follow_changed"}); err != nil {
			return err
		}
	}
	return nil
}
func (f *Follow) BeforeUpdate(tx *gorm.DB) error { return queueFeedFollowChange(tx, f) }
func (f *Follow) BeforeDelete(tx *gorm.DB) error { return queueFeedFollowChange(tx, f) }
func (b *Block) AfterCreate(tx *gorm.DB) error {
	return QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: b.AccountId, RecipientIDs: []uint64{b.AccountId, b.TargetAccountId}, TargetAccountID: b.TargetAccountId, TargetDomain: b.TargetDomain, RelationshipID: b.Id, Reason: "block_changed"})
}
func (b *Block) BeforeDelete(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []Block
	if err := feedAffectedRows(tx, &Block{}, b.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := row.AfterCreate(tx); err != nil {
			return err
		}
	}
	return nil
}
func (m *MuteAccount) AfterCreate(tx *gorm.DB) error {
	return QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: m.AccountId, RecipientIDs: []uint64{m.AccountId}, TargetAccountID: m.TargetId, RelationshipID: m.Id, Reason: "mute_changed"})
}
func (m *MuteAccount) BeforeDelete(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []MuteAccount
	if err := feedAffectedRows(tx, &MuteAccount{}, m.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := row.AfterCreate(tx); err != nil {
			return err
		}
	}
	return nil
}
func (m *MuteAccount) BeforeUpdate(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []MuteAccount
	if err := feedAffectedRows(tx, &MuteAccount{}, m.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := row.AfterCreate(tx); err != nil {
			return err
		}
	}
	return nil
}
func (b *DomainBlock) AfterCreate(tx *gorm.DB) error {
	return QueueFeedInvalidation(tx, FeedEventMetadata{TargetDomain: b.Domain, RelationshipID: b.Id, Reason: "domain_policy_changed"})
}
func (b *DomainBlock) BeforeDelete(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []DomainBlock
	if err := feedAffectedRows(tx, &DomainBlock{}, b.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := row.AfterCreate(tx); err != nil {
			return err
		}
	}
	return nil
}
func (b *DomainBlock) BeforeUpdate(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []DomainBlock
	if err := feedAffectedRows(tx, &DomainBlock{}, b.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := row.AfterCreate(tx); err != nil {
			return err
		}
	}
	return nil
}
func queueFeedListAccount(tx *gorm.DB, la *ListAccount) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var list List
	if err := tx.Session(&gorm.Session{NewDB: true}).First(&list, la.ListId).Error; err != nil {
		return err
	}
	return QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: list.AccountId, RecipientIDs: []uint64{list.AccountId}, ListIDs: []uint64{la.ListId}, TargetAccountID: la.AccountId, Reason: "list_changed"})
}
func (la *ListAccount) BeforeDelete(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []ListAccount
	q := tx.Session(&gorm.Session{NewDB: true}).Model(&ListAccount{}).Clauses(clause.Locking{Strength: "UPDATE"})
	if la.ListId > 0 {
		q = q.Where("list_id = ?", la.ListId)
	}
	if la.AccountId > 0 {
		q = q.Where("account_id = ?", la.AccountId)
	}
	if where, ok := tx.Statement.Clauses["WHERE"]; ok {
		q = q.Where(where.Expression)
	}
	if err := q.Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := queueFeedListAccount(tx, &row); err != nil {
			return err
		}
	}
	return nil
}
func (l *List) AfterUpdate(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []List
	if err := feedAffectedRows(tx, &List{}, l.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: row.AccountId, RecipientIDs: []uint64{row.AccountId}, ListIDs: []uint64{row.Id}, Reason: "list_changed"}); err != nil {
			return err
		}
	}
	return nil
}
func (l *List) BeforeDelete(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []List
	if err := feedAffectedRows(tx, &List{}, l.Id, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := row.AfterUpdate(tx); err != nil {
			return err
		}
	}
	return nil
}

func (lfa *ListFollowAccount) BeforeDelete(tx *gorm.DB) error {
	if skipFeedHooks(tx) {
		return nil
	}
	var rows []ListFollowAccount
	if err := feedAffectedRows(tx, &ListFollowAccount{}, lfa.ID, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if err := QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: row.AccountId, RecipientIDs: []uint64{row.AccountId}, ListIDs: []uint64{row.ListId}, Reason: "list_subscription_changed"}); err != nil {
			return err
		}
	}
	return nil
}
