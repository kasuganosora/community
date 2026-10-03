package models

import (
	"encoding/json"
	"gorm.io/gorm"
)

// FederationProfileSnapshot includes associations excluded from Account JSON.
// It preserves the committed profile version during delayed delivery/retries.
type FederationProfileSnapshot struct {
	Account Account       `json:"account"`
	Extra   *AccountExtra `json:"extra"`
	Emojis  []*Emoji      `json:"emojis"`
}

// QueueFederationProfile is called in the same transaction as the profile save.
func QueueFederationProfile(tx *gorm.DB, accountID uint64) error {
	if !hasFederationOutbox(tx) {
		return nil
	}
	var account Account
	if err := tx.Preload("Extra").First(&account, accountID).Error; err != nil {
		return err
	}
	if account.Domain != nil || account.IsBan {
		return nil
	}
	var follows []Follow
	if err := tx.Preload("Account").Where("follow_id = ? AND status = ?", accountID, FollowStatusFollowed).Find(&follows).Error; err != nil {
		return err
	}
	recipients := []uint64{}
	for _, follow := range follows {
		a := follow.Account
		if a == nil || a.Domain == nil || a.IsBan {
			continue
		}
		blocked, err := AccountsBlocked(tx, &account, a)
		if err != nil {
			return err
		}
		if blocked {
			continue
		}
		var count int64
		if err := tx.Model(&Block{}).Where("account_id = ? AND type = ? AND target_domain = ?", account.Id, BlockTypeDomain, *a.Domain).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		recipients = append(recipients, a.Id)
	}
	if len(recipients) == 0 {
		return nil
	}
	emojis, err := GetEmojiFromText(tx, AccountEmojiText(&account))
	if err != nil {
		return err
	}
	snapshot := FederationProfileSnapshot{Account: account, Extra: account.Extra, Emojis: make([]*Emoji, 0, len(emojis))}
	for _, e := range emojis {
		snapshot.Emojis = append(snapshot.Emojis, e)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return tx.Create(&FederationOutbox{ObjectID: accountID, AccountID: accountID, Kind: "ProfileUpdate", Payload: string(payload), RecipientIDs: recipients}).Error
}
