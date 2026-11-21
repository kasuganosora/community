package home

import "gorm.io/gorm"

// ExclusiveHomeRecipients reports followers whose exclusive lists contain the
// author. Their home timeline keeps the author's own rows out, matching
// homeCandidates, while the author's own home timeline is unchanged.
func ExclusiveHomeRecipients(conn *gorm.DB, authorID uint64, recipientIDs []uint64) (map[uint64]bool, error) {
	excluded := map[uint64]bool{}
	if conn == nil || authorID == 0 || len(recipientIDs) == 0 {
		return excluded, nil
	}
	ids := make([]uint64, 0, len(recipientIDs))
	seen := map[uint64]bool{}
	for _, id := range recipientIDs {
		if id == 0 || id == authorID || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return excluded, nil
	}
	var owners []uint64
	err := conn.Table("lists").
		Joins("JOIN list_accounts ON list_accounts.list_id = lists.id").
		Where("lists.deleted_at IS NULL AND lists.exclusive = ? AND list_accounts.account_id = ? AND lists.account_id IN ?", true, authorID, ids).
		Distinct().
		Pluck("lists.account_id", &owners).Error
	if err != nil {
		return nil, err
	}
	for _, id := range owners {
		if id != authorID {
			excluded[id] = true
		}
	}
	return excluded, nil
}
