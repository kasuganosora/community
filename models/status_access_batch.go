package models

import (
	"strings"

	"gorm.io/gorm"
)

// FilterVisibleStatuses authorizes each root and its embedded original using
// request-local bulk reads. It never stores an authorization result on a model
// or in a cross-request cache, so unfollows and blocks take effect on next read.
func FilterVisibleStatuses(conn *gorm.DB, viewer *Account, statuses Statuses) (Statuses, error) {
	result := make(Statuses, 0, len(statuses))
	if len(statuses) == 0 {
		return result, nil
	}
	flat := make([]*Status, 0, len(statuses))
	seen := map[*Status]bool{}
	var collect func(*Status)
	collect = func(s *Status) {
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
	authors := map[uint64]*Account{}
	missingAuthors := map[uint64]bool{}
	for _, s := range flat {
		if s.Account != nil && s.Account.Id == s.AccountId {
			authors[s.AccountId] = s.Account
		} else {
			missingAuthors[s.AccountId] = true
		}
	}
	ids := make([]uint64, 0, len(missingAuthors))
	for id := range missingAuthors {
		if authors[id] == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		var accounts []*Account
		if err := conn.Where("id IN ?", ids).Find(&accounts).Error; err != nil {
			return nil, err
		}
		for _, a := range accounts {
			authors[a.Id] = a
		}
	}
	mentions := map[uint64]bool{}
	missingMentions := []uint64{}
	privateAuthors := map[uint64]bool{}
	authorIDs := make([]uint64, 0, len(authors))
	for id := range authors {
		authorIDs = append(authorIDs, id)
	}
	if viewer != nil {
		for _, s := range flat {
			if s.AccountId == viewer.Id {
				continue
			}
			if s.Visibility == StatusVisibilityPrivate {
				privateAuthors[s.AccountId] = true
			}
			if s.Visibility >= StatusVisibilityPrivate {
				if s.ToAccounts == nil {
					missingMentions = append(missingMentions, s.Id)
				}
				for _, a := range s.ToAccounts {
					if a != nil && a.Id == viewer.Id {
						mentions[s.Id] = true
					}
				}
			}
		}
	}
	if len(missingMentions) > 0 {
		var rows []StatusToAccount
		if err := conn.Select("status_id").Where("status_id IN ? AND account_id = ?", missingMentions, viewer.Id).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			mentions[r.StatusId] = true
		}
	}
	followed := map[uint64]bool{}
	if len(privateAuthors) > 0 {
		ids = ids[:0]
		for id := range privateAuthors {
			ids = append(ids, id)
		}
		var rows []Follow
		if err := conn.Select("follow_id").Where("account_id = ? AND status = ? AND follow_id IN ?", viewer.Id, FollowStatusFollowed, ids).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, f := range rows {
			followed[f.FollowId] = true
		}
	}
	globalAccounts := map[uint64]bool{}
	globalDomains := map[string]bool{}
	blockedAccounts := map[uint64]bool{}
	blockedDomains := map[string]bool{}
	if len(authorIDs) > 0 {
		viewerID := uint64(0)
		if viewer != nil {
			viewerID = viewer.Id
		}
		viewerDomain := ""
		if viewer != nil && viewer.Domain != nil {
			viewerDomain = *viewer.Domain
		}
		var blocks []Block
		query := conn.Where("account_id = ?", 0)
		if viewer != nil {
			query = conn.Where("account_id IN ? OR (account_id IN ? AND ((type = ? AND target_account_id = ?) OR (type = ? AND target_domain = ?)))", []uint64{0, viewerID}, authorIDs, BlockTypeAccount, viewerID, BlockTypeDomain, viewerDomain)
		}
		if err := query.Find(&blocks).Error; err != nil {
			return nil, err
		}
		for _, b := range blocks {
			if b.AccountId == 0 {
				if b.Type == BlockTypeAccount {
					globalAccounts[b.TargetAccountId] = true
				} else if b.Type == BlockTypeDomain {
					globalDomains[strings.ToLower(b.TargetDomain)] = true
				}
				continue
			}
			if b.AccountId == viewerID {
				if b.Type == BlockTypeAccount {
					blockedAccounts[b.TargetAccountId] = true
				} else if b.Type == BlockTypeDomain {
					blockedDomains[strings.ToLower(b.TargetDomain)] = true
				}
				continue
			}
			if b.Type == BlockTypeAccount && b.TargetAccountId == viewerID {
				blockedAccounts[b.AccountId] = true
			}
			if b.Type == BlockTypeDomain && viewer != nil && viewer.Domain != nil && strings.EqualFold(b.TargetDomain, viewerDomain) {
				blockedAccounts[b.AccountId] = true
			}
		}
	}
	suspend, _, err := DomainSeverities(conn)
	if err != nil {
		return nil, err
	}
	for domain := range suspend {
		globalDomains[domain] = true
	}
	var allowed func(*Status, map[*Status]bool) bool
	allowed = func(s *Status, chain map[*Status]bool) bool {
		if s != nil && s.LocalOnly && viewer != nil && viewer.Domain != nil {
			return false
		}
		if s == nil || s.DeletedAt != nil || chain[s] || len(chain) >= 32 {
			return false
		}
		chain[s] = true
		defer delete(chain, s)
		a := authors[s.AccountId]
		if a == nil || a.IsBan || globalAccounts[a.Id] || (a.Domain != nil && domainListed(globalDomains, *a.Domain)) {
			return false
		}
		owner := viewer != nil && s.AccountId == viewer.Id
		if !owner {
			if s.IsScheduled() {
				return false
			}
			if viewer != nil {
				a := authors[s.AccountId]
				if a == nil {
					return false
				}
				if globalAccounts[viewer.Id] || globalAccounts[a.Id] || blockedAccounts[a.Id] {
					return false
				}
				if viewer.Domain != nil && domainListed(globalDomains, *viewer.Domain) {
					return false
				}
				if a.Domain != nil && (domainListed(globalDomains, *a.Domain) || domainListed(blockedDomains, *a.Domain)) {
					return false
				}
			}
			switch s.Visibility {
			case StatusVisibilityPublic, StatusVisibilityUnlisted:
			case StatusVisibilityPrivate:
				if viewer == nil || (!followed[s.AccountId] && !mentions[s.Id]) {
					return false
				}
			case StatusVisibilityDirect:
				if viewer == nil || !mentions[s.Id] {
					return false
				}
			default:
				return false
			}
		}
		if s.ReblogId != nil && *s.ReblogId != 0 {
			if s.Reblog == nil || s.Reblog.Id != *s.ReblogId {
				return false
			}
			return allowed(s.Reblog, chain)
		}
		return true
	}
	for _, s := range statuses {
		if allowed(s, map[*Status]bool{}) {
			result = append(result, s)
		}
	}
	return result, nil
}
