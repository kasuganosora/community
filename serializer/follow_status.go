package serializer

import (
	"strconv"
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

/*
*

	{
	    "id": "110472445918843421",
	    "following": true,
	    "showing_reblogs": true,
	    "notifying": false,
	    "languages": null,
	    "followed_by": false,
	    "blocking": false,
	    "blocked_by": false,
	    "muting": false,
	    "muting_notifications": false,
	    "requested": false,
	    "requested_by": false,
	    "domain_blocking": false,
	    "endorsed": false,
	    "note": ""
	}
*/

type FollowStatus struct {
	FollowedMessage     string   `json:"followed_message,omitempty"`
	Id                  string   `json:"id"`
	Following           bool     `json:"following"`
	ShowingReblogs      bool     `json:"showing_reblogs"`
	Notifying           bool     `json:"notifying"`
	Languages           []string `json:"languages"`
	FollowedBy          bool     `json:"followed_by"`
	Blocking            bool     `json:"blocking"`
	BlockedBy           bool     `json:"blocked_by"`
	Muting              bool     `json:"muting"`
	MutingNotifications bool     `json:"muting_notifications"`
	MutingExpiresAt     *string  `json:"muting_expires_at"`
	Requested           bool     `json:"requested"`
	RequestedBy         bool     `json:"requested_by"`
	DomainBlocking      bool     `json:"domain_blocking"`
	Endorsed            bool     `json:"endorsed"`
	Note                string   `json:"note"`
}

func ToFollowStatus(account *models.Account, follow *models.Follow) (ret *FollowStatus) {
	ret = &FollowStatus{
		Id:                  strconv.FormatUint(account.Id, 10),
		Notifying:           false,
		Languages:           nil,
		FollowedBy:          false,
		Blocking:            false,
		BlockedBy:           false,
		Muting:              false,
		MutingNotifications: false,
		Requested:           false,
		RequestedBy:         false,
		DomainBlocking:      false,
		Endorsed:            false,
		Note:                "",
	}

	if follow != nil {
		ret.Following = follow.Status == models.FollowStatusFollowed
		ret.Requested = follow.Status == models.FollowStatusRequested
		if ret.Following {
			ret.FollowedMessage = account.FollowedMessage
		}

		ret.ShowingReblogs = follow.ShowsReblogs()
		ret.Notifying, _ = follow.Properties[models.FollowPropertyKeyNotify].(bool)
		ret.Languages = follow.Languages()
	}

	return
}

// LoadRelationships serves both the batch read and single-account mutations.
func LoadRelationships(conn *gorm.DB, viewerID uint64, targets []*models.Account) (map[uint64]*FollowStatus, error) {
	result := make(map[uint64]*FollowStatus, len(targets))
	if len(targets) == 0 {
		return result, nil
	}
	ids := make([]uint64, 0, len(targets))
	byID := make(map[uint64]*models.Account, len(targets))
	domains := make([]string, 0)
	for _, target := range targets {
		ids = append(ids, target.Id)
		byID[target.Id] = target
		result[target.Id] = ToFollowStatus(target, nil)
		if target.Domain != nil {
			domains = append(domains, *target.Domain)
		}
	}

	var follows []models.Follow
	if err := conn.Where("(account_id = ? AND follow_id IN ?) OR (follow_id = ? AND account_id IN ?)", viewerID, ids, viewerID, ids).Find(&follows).Error; err != nil {
		return nil, err
	}
	for _, follow := range follows {
		if follow.AccountId == viewerID {
			result[follow.FollowId] = ToFollowStatus(byID[follow.FollowId], &follow)
		}
	}
	for _, follow := range follows {
		if follow.FollowId == viewerID {
			row := result[follow.AccountId]
			row.FollowedBy = follow.Status == models.FollowStatusFollowed
			row.RequestedBy = follow.Status == models.FollowStatusRequested
		}
	}

	var blocks []models.Block
	if err := conn.Where("type = ? AND ((account_id = ? AND target_account_id IN ?) OR (target_account_id = ? AND account_id IN ?))",
		models.BlockTypeAccount, viewerID, ids, viewerID, ids).Find(&blocks).Error; err != nil {
		return nil, err
	}
	for _, block := range blocks {
		if block.AccountId == viewerID {
			result[block.TargetAccountId].Blocking = true
		} else {
			result[block.AccountId].BlockedBy = true
		}
	}
	if len(domains) > 0 {
		var blockedDomains []string
		if err := conn.Model(&models.Block{}).Where("account_id = ? AND type = ? AND target_domain IN ?", viewerID, models.BlockTypeDomain, domains).
			Pluck("target_domain", &blockedDomains).Error; err != nil {
			return nil, err
		}
		blocked := make(map[string]bool, len(blockedDomains))
		for _, domain := range blockedDomains {
			blocked[domain] = true
		}
		for _, target := range targets {
			if target.Domain != nil {
				result[target.Id].DomainBlocking = blocked[*target.Domain]
			}
		}
	}

	var mutes []models.MuteAccount
	if err := conn.Where("account_id = ? AND target_id IN ? AND (expired_at IS NULL OR expired_at > ?)", viewerID, ids, time.Now()).Find(&mutes).Error; err != nil {
		return nil, err
	}
	for _, mute := range mutes {
		row := result[mute.TargetId]
		row.Muting = true
		row.MutingNotifications = mute.Notifications
		if mute.ExpiredAt != nil {
			row.MutingExpiresAt = new(mute.ExpiredAt.UTC().Format(time.RFC3339))
		}
	}

	var endorsed []uint64
	if err := conn.Model(&models.Endorsement{}).Where("account_id = ? AND target_id IN ?", viewerID, ids).Pluck("target_id", &endorsed).Error; err != nil {
		return nil, err
	}
	for _, id := range endorsed {
		result[id].Endorsed = true
	}

	return result, nil
}
