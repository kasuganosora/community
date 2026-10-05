package models

import (
	"strings"

	"gorm.io/gorm"
)

// AccountsBlocked applies both directions: a previous direct-message address
// does not remain a grant after either participant blocks the other.
func AccountsBlocked(conn *gorm.DB, first, second *Account) (bool, error) {
	if first == nil || second == nil || first.Id == second.Id {
		return false, nil
	}
	var blocks []Block
	if err := conn.Where("account_id IN ?", []uint64{0, first.Id, second.Id}).Find(&blocks).Error; err != nil {
		return false, err
	}
	for _, block := range blocks {
		if block.Type == BlockTypeAccount {
			if (block.AccountId == first.Id || block.AccountId == 0) && block.TargetAccountId == second.Id {
				return true, nil
			}
			if (block.AccountId == second.Id || block.AccountId == 0) && block.TargetAccountId == first.Id {
				return true, nil
			}
		}
		if block.Type == BlockTypeDomain {
			if (block.AccountId == first.Id || block.AccountId == 0) && second.Domain != nil && strings.EqualFold(block.TargetDomain, *second.Domain) {
				return true, nil
			}
			if (block.AccountId == second.Id || block.AccountId == 0) && first.Domain != nil && strings.EqualFold(block.TargetDomain, *first.Domain) {
				return true, nil
			}
		}
	}
	suspend, _, err := DomainSeverities(conn)
	if err != nil {
		return false, err
	}
	if first.Domain != nil && domainListed(suspend, *first.Domain) {
		return true, nil
	}
	if second.Domain != nil && domainListed(suspend, *second.Domain) {
		return true, nil
	}
	return false, nil
}

// ValidateReplyVisibility prevents a reply endpoint from publishing a broader
// audience than the private/direct parent selected by the caller.
func ValidateReplyVisibility(parent *Status, visibility uint8) bool {
	return parent == nil || visibility >= parent.Visibility
}
