package invitecode

import (
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

const (
	InviteCodeAfterRegisterActionFollowMe = "follow_me"
)

// ExecAfterRegisterAction 执行注册后的动作
// 创建完后如果有用到邀请码,则将邀请码设置为已使用,并且把当前用户的邀请人设置为邀请码的拥有者,
// 如果邀请码设置了使用回调,则调用邀请码的回调函数(如果要验证邮箱,则在邮箱验证通过后调用回调函数)
func ExecAfterRegisterAction(db *gorm.DB, inv *models.InviteCode, user *models.User, account *models.Account) {
	// 如果邀请码并没设置使用回调,则直接返回
	if inv == nil || inv.AfterRegisterAction == "" {
		return
	}

	// 判断是否需要审核,如果需要审核,但是未审核通过,则不执行后续操作
	if systemsettings.GetBool(systemsettings.KeyRegistrationApprovalRequired, true) && user.Status == models.UserStatusUnapproved {
		log.Debug("user status is not norma, user has not approved, skip ExecAfterRegisterAction")
		return
	}

	// 是否需要验证邮箱, 如果需要验证邮箱,但是未验证通过,则不执行后续操作
	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && user.Status == models.UserStatusUnverifiedMail {
		log.Debug("user status is not normal, user has not verified email, skip ExecAfterRegisterAction")
		return
	}

	// 开始执行邀请码的回调函数
	switch inv.AfterRegisterAction {
	case InviteCodeAfterRegisterActionFollowMe:
		followMe(db, inv, user, account)
	default:
		log.Error("unknown after register action: %s, record id %d", inv.AfterRegisterAction, inv.Id)
	}
}

func followMe(db *gorm.DB, inv *models.InviteCode, user *models.User, account *models.Account) {
	// 找出邀请码的拥有者的账号
	ownerAccount := &models.Account{}
	err := db.Where("user_id = ?", inv.UserId).
		Where("bot = ?", false).
		First(ownerAccount).Error

	if err != nil {
		err = errors.WithMessagef(err, "find owner account failed, invite code id %d", inv.Id)
		log.ErrorV(err)
		return
	}

	// 创建关注关系
	follow := &models.Follow{
		AccountId: account.Id,      // 当前用户的账号
		FollowId:  ownerAccount.Id, // 被关注的账号
	}

	err = db.Create(follow).Error
	if err != nil {
		err = errors.WithMessagef(err, "create follow record failed, invite code id %d", inv.Id)
		log.ErrorV(err)
		return
	}
}
