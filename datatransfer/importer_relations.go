package datatransfer

import (
	"errors"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func (i *importer) importRelations() error {
	for _, r := range i.bundle.Relations {
		sourceID := r.Kind + "\n" + r.AccountID + "\n" + r.TargetID + "\n" + r.TargetDomain
		if i.personal() && r.Kind != "domain_block" && i.ids["account"][r.TargetID] == 0 {
			if err := i.preserve("relation", sourceID, r); err != nil {
				return err
			}
			continue
		}
		_, err := i.apply("relation", sourceID, r, func(tx *gorm.DB) (uint64, error) {
			owner, err := i.ref("account", r.AccountID)
			if err != nil {
				return 0, err
			}
			var target uint64
			if r.Kind != "domain_block" {
				target, err = i.ref("account", r.TargetID)
				if err != nil {
					return 0, err
				}
				if target == owner {
					return 0, errors.New("self relationship is not allowed")
				}
			}
			switch r.Kind {
			case "follow", "follow_request":
				state := models.FollowStatusFollowed
				if r.Kind == "follow_request" {
					state = models.FollowStatusRequested
				}
				if i.personal() {
					var targetAccount models.Account
					if err = tx.First(&targetAccount, target).Error; err != nil {
						return 0, err
					}
					if targetAccount.Domain != nil || targetAccount.Locked {
						state = models.FollowStatusRequested
					}
				}
				f := models.Follow{AccountId: owner, FollowId: target, Status: state, CreatedAt: timePointer(r.CreatedAt), Properties: map[string]any{"reblogs": r.ShowReblogs, "notify": r.Notify, "activity_uri": r.ActivityURI, "languages": r.Languages}}
				var existing models.Follow
				if err = tx.Where("account_id = ? AND follow_id = ?", owner, target).First(&existing).Error; err == nil {
					if !i.personal() && existing.Status != state {
						return 0, errors.New("existing relationship state conflict")
					}
					return existing.Id, nil
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return 0, err
				}
				if i.personal() {
					delete(f.Properties, "activity_uri")
					err = tx.Session(&gorm.Session{SkipHooks: false}).Create(&f).Error
				} else {
					err = tx.Create(&f).Error
				}
				if err != nil {
					return 0, err
				}
				if state == models.FollowStatusFollowed && !i.personal() {
					if err = tx.Model(&models.AccountExtra{}).Where("account_id = ?", owner).UpdateColumn("following_count", gorm.Expr("following_count + 1")).Error; err != nil {
						return 0, err
					}
					if err = tx.Model(&models.AccountExtra{}).Where("account_id = ?", target).UpdateColumn("followers_count", gorm.Expr("followers_count + 1")).Error; err != nil {
						return 0, err
					}
				}
				return f.Id, nil
			case "block", "domain_block":
				block := models.Block{AccountId: owner, TargetAccountId: target, CreatedAt: timePointer(r.CreatedAt)}
				if r.Kind == "domain_block" {
					block.Type = models.BlockTypeDomain
					block.TargetDomain = r.TargetDomain
				}
				err = tx.Create(&block).Error
				return block.Id, err
			case "mute":
				mute := models.MuteAccount{AccountId: owner, TargetId: target, CreatedAt: timePointer(r.CreatedAt), ExpiredAt: r.ExpiresAt}
				err = tx.Create(&mute).Error
				return mute.Id, err
			}
			return 0, errors.New("unsupported relationship")
		})
		if err != nil {
			return err
		}
	}
	return nil
}
