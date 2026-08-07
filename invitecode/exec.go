package invitecode

import (
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

const (
	InviteCodeAfterRegisterActionFollowMe = "follow_me"

	// invalidMaxUseSentinel is used to represent an "unlimited" invite code.
	// When MaxUseCount <= 0 the invite code has no usage cap.
	invalidMaxUseSentinel = 0
)

// ErrNoInviteCode indicates that no invite code was provided (empty string).
// Callers can distinguish "not provided" from "invalid" using errors.Is.
var ErrNoInviteCode = errors.New("no invite code provided")

// ExecAfterRegisterAction 执行注册后的动作.
//
// 创建完后如果有用到邀请码,则将邀请码设置为已使用,并且把当前用户的邀请人设置为邀请码的拥有者,
// 如果邀请码设置了使用回调,则调用邀请码的回调函数(如果要验证邮箱,则在邮箱验证通过后调用回调函数).
//
// 注意: 邀请码的使用计数(use_count)应在注册流程中通过 ConsumeInviteCode 以原子方式递增,
// 本函数仅负责执行 "注册后动作" (例如 follow_me). 这样可以保证计数与用户创建处于同一事务/原子边界内.
func ExecAfterRegisterAction(db *gorm.DB, inv *models.InviteCode, user *models.User, account *models.Account) {
	// 防御性检查: 避免空指针解引用
	if inv == nil || inv.AfterRegisterAction == "" {
		return
	}

	// 邀请码不可用则不执行后续操作 (例如已被禁用/用尽/过期).
	// 这里再次校验, 防止在 "checkAndGetInviteCode" 与 "执行回调" 之间邀请码状态发生变化.
	if !inv.IsAvailable() {
		log.Debug("invite code is not available, skip ExecAfterRegisterAction, invite code id %d", inv.Id)
		return
	}

	// 判断是否需要审核,如果需要审核,但是未审核通过,则不执行后续操作
	if systemsettings.GetBool(systemsettings.KeyRegistrationApprovalRequired, true) && user != nil &&
		user.Status == models.UserStatusUnapproved {
		log.Debug("user status is not norma, user has not approved, skip ExecAfterRegisterAction")
		return
	}

	// 是否需要验证邮箱, 如果需要验证邮箱,但是未验证通过,则不执行后续操作
	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && user != nil &&
		user.Status == models.UserStatusUnverifiedMail {
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

// ConsumeInviteCode 以原子方式消费一张邀请码: 在满足可用条件的前提下, 将 use_count 自增 1.
//
// 该函数使用单条 UPDATE 语句配合 WHERE 条件完成 "检查 + 递增", 从而避免 "先读后写" 带来的
// TOCTOU 竞态条件. 多个并发请求同时消费同一张邀请码时, 数据库会串行化这些 UPDATE,
// 只有满足条件的请求才会真正递增计数, RowsAffected 反映是否成功消费.
//
// 成功消费时返回更新后的邀请码记录 (含最新的 use_count) 与 nil; 若邀请码不存在/不可用/已用尽,
// 则返回错误 (错误信息为人类可读描述, 如 "invite code not found" / "invite code is not available").
//
// 该函数会在内部开启一个事务. 若需要在已有事务内消费邀请码 (例如与用户创建处于同一事务,
// 以保证 "消费邀请码" 与 "创建用户" 一起成功或一起回滚), 请使用 ConsumeInviteCodeTx.
func ConsumeInviteCode(db *gorm.DB, code string) (*models.InviteCode, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}

	if code == "" {
		return nil, errors.New("invite code is empty")
	}

	var inv models.InviteCode

	err := db.Transaction(func(tx *gorm.DB) error {
		got, err := consumeInviteCodeInTx(tx, code)
		if err != nil {
			return err
		}

		inv = *got

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &inv, nil
}

// ConsumeInviteCodeTx 在给定的事务 (tx) 上以原子方式消费一张邀请码.
//
// 与 ConsumeInviteCode 不同, 本函数不会自行开启事务, 而是直接在调用方传入的 tx 上执行,
// 以便将 "邀请码计数递增" 与同一事务内的其它操作 (例如创建用户/账号) 绑定在一起:
// 只要事务回滚, use_count 的递增也会随之回滚, 不会出现 "用户没建成功却消耗了名额" 的情况.
//
// 使用示例 (注册流程):
//
//	err := db.Transaction(func(tx *gorm.DB) error {
//	    inv, err := invitecode.ConsumeInviteCodeTx(tx, req.InviteCode)
//	    if err != nil {
//	        return err
//	    }
//	    // ... 使用 inv 创建用户/账号 ...
//	    return nil
//	})
//
// 成功时返回更新后的邀请码记录; 邀请码不存在/不可用/已用尽时返回错误.
// 注意: 当 code 为空字符串时, 返回 (nil, nil), 表示 "没有邀请码可消费" (即未提供邀请码),
// 以便调用方按 "邀请码可选" 的语义处理.
func ConsumeInviteCodeTx(tx *gorm.DB, code string) (*models.InviteCode, error) {
	if tx == nil {
		return nil, errors.New("tx is nil")
	}

	// 未提供邀请码: 返回 (nil, ErrNoInviteCode), 由调用方决定如何处理.
	if code == "" {
		return nil, ErrNoInviteCode
	}

	return consumeInviteCodeInTx(tx, code)
}

// consumeInviteCodeInTx 在事务/连接 tx 上完成 "校验 + 条件递增 use_count" 的核心逻辑.
// 该函数既被 ConsumeInviteCode (内部事务) 使用, 也被 ConsumeInviteCodeTx (外部事务) 使用,
// 保证两条路径的递增语义完全一致.
func consumeInviteCodeInTx(tx *gorm.DB, code string) (*models.InviteCode, error) {
	var inv models.InviteCode

	// 1. 查询邀请码.
	err := tx.Where("code = ?", code).First(&inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invite code not found")
		}

		return nil, errors.WithMessage(err, "query invite code failed")
	}

	// 2. 不可用直接拒绝 (禁用/过期等).
	if !inv.IsAvailable() {
		return nil, errors.New("invite code is not available")
	}

	// 3. 条件递增 use_count.
	//    - 若 MaxUseCount <= 0 表示不限次数, 只要未被禁用/过期即可使用, 直接自增.
	//    - 否则要求 use_count < max_use_count 才允许自增, 防止超额.
	//    注意: 使用绑定参数 (max_use_count) 而非列间比较 (use_count < max_use_count),
	//    以兼容部分 SQL 引擎对列间比较支持不佳的情况.
	query := tx.Model(&models.InviteCode{}).Where("id = ?", inv.Id)
	if inv.MaxUseCount > invalidMaxUseSentinel {
		query = query.Where("use_count < ?", inv.MaxUseCount)
	}

	// 防御: 并发期间邀请码可能被其它请求禁用, 加入 disabled = false 避免消费已禁用邀请码.
	query = query.Where("disabled = ?", false)
	// 过期判断同样下推到 SQL, 避免在此期间邀请码恰好过期却仍被消费.
	// 注意: 使用绑定参数 (now) 而非 SQL 的 CURRENT_TIMESTAMP, 以保证跨数据库
	// (SQLite/MySQL/PostgreSQL) 的时间值比较一致, 避免 ISO8601 与 'YYYY-MM-DD HH:MM:SS'
	// 字符串比较导致的歧义.
	now := time.Now()
	query = query.Where("expire_at IS NULL OR expire_at > ?", now)

	result := query.Update("use_count", gorm.Expr("use_count + 1"))
	if result.Error != nil {
		return nil, errors.WithMessage(result.Error, "increment use_count failed")
	}

	if result.RowsAffected == 0 {
		// 并发竞争中本轮没有抢到名额, 或者邀请码在此期间变为不可用.
		return nil, errors.New("invite code is not available")
	}

	// 4. 回填最新 use_count, 保持调用方拿到的对象与数据库一致.
	inv.UseCount++

	return &inv, nil
}

// followMe 让新注册的用户关注邀请码的拥有者.
//
// 幂等性: 若该关注关系已存在 (account_id, follow_id) 则不会重复创建,
// 以避免对同一用户重复执行回调时产生重复关注记录.
// 任何数据库错误都会被记录但不会 panic, 以保证注册主流程不被打断.
func followMe(db *gorm.DB, inv *models.InviteCode, user *models.User, account *models.Account) { //nolint:unparam // param reserved for future use
	if db == nil {
		log.Error("followMe called with nil db, invite code id %d", inv.Id)
		return
	}

	if account == nil {
		log.Error("followMe called with nil account, invite code id %d, user id %d", inv.Id, safeUserId(user))
		return
	}

	if account.Id == 0 {
		log.Error("followMe called with zero account id, invite code id %d", inv.Id)
		return
	}

	// 找出邀请码拥有者的非机器人账号
	ownerAccount := &models.Account{}

	err := db.Where("user_id = ?", inv.UserId).
		Where("bot = ?", false).
		First(ownerAccount).Error
	if err != nil {
		err = errors.WithMessagef(err, "find owner account failed, invite code id %d", inv.Id)
		log.ErrorV(err)

		return
	}

	// 不能关注自己
	if ownerAccount.Id == account.Id {
		log.Debug("skip followMe: owner is the same as the new account, invite code id %d", inv.Id)
		return
	}

	// 创建关注关系 (幂等: 已存在则不重复创建)
	follow := &models.Follow{
		AccountId: account.Id,      // 当前用户的账号
		FollowId:  ownerAccount.Id, // 被关注的账号
	}

	// 使用 FirstOrCreate 防止重复关注 (例如回调被重复触发).
	// 注意: 这里不设置 Properties, 因为 FirstOrCreate 在记录已存在时不会更新属性.
	result := db.Where("account_id = ? AND follow_id = ?", account.Id, ownerAccount.Id).
		FirstOrCreate(follow)
	if result.Error != nil {
		err = errors.WithMessagef(result.Error, "create follow record failed, invite code id %d", inv.Id)
		log.ErrorV(err)

		return
	}

	if result.RowsAffected == 0 {
		log.Debug("follow relation already exists, skip, invite code id %d", inv.Id)
	}
}

// safeUserId returns the user id for logging without panicking on nil user.
func safeUserId(user *models.User) uint64 {
	if user == nil {
		return 0
	}

	return user.Id
}

// CleanupExpired 软删除已过期或已用尽的邀请码, 用于定期清理.
//
// 注意: 该函数仅做软删除 (依赖 InviteCode 的 gorm.DeletedAt).
// 清理逻辑只删除 "确定不再可用" 的邀请码:
//   - 已过期 (expire_at < now)
//   - 已用尽 (max_use_count > 0 且 use_count >= max_use_count)
//
// 实现说明:
//   - 过期判断使用绑定的时间参数 (?), 跨数据库 (SQLite/MySQL/PostgreSQL) 一致,
//     避免 CURRENT_TIMESTAMP 与存储的时间字符串格式不一致导致比较错误.
//   - 用尽判断 (use_count >= max_use_count) 是列与列的比较. 为了兼容部分嵌入式
//     SQL 引擎对列间比较支持不佳的问题, 这里先取出所有 max_use_count > 0 的候选记录,
//     在 Go 侧判断 use_count >= max_use_count, 再按主键批量软删除. 由于清理是低频
//     的后台任务, 这种方式的开销可以接受, 同时保证了跨数据库的正确性.
//
// 返回被清理的记录数. 清理失败时返回错误.
func CleanupExpired(db *gorm.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("db is nil")
	}

	var total int64

	// 1. 软删除已过期的邀请码 (使用绑定时间参数, 跨数据库一致).
	now := time.Now()

	result := db.Where("expire_at IS NOT NULL AND expire_at < ?", now).
		Delete(&models.InviteCode{})
	if result.Error != nil {
		return total, errors.WithMessage(result.Error, "cleanup expired invite codes failed")
	}

	total += result.RowsAffected

	// 2. 软删除已用尽的邀请码 (max_use_count > 0 且 use_count >= max_use_count).
	//    先取出候选记录, 在 Go 中精确判断, 避免某些 SQL 引擎对列间比较的处理问题.
	var candidates []models.InviteCode

	err := db.Unscoped().
		Where("max_use_count > ?", 0).
		Where("deleted_at IS NULL").
		Find(&candidates).Error
	if err != nil {
		return total, errors.WithMessage(err, "query exhausted invite codes failed")
	}

	exhaustedIDs := make([]int64, 0, len(candidates))
	for i := range candidates {
		c := &candidates[i]
		if c.UseCount >= c.MaxUseCount {
			exhaustedIDs = append(exhaustedIDs, c.Id)
		}
	}

	if len(exhaustedIDs) > 0 {
		result = db.Where("id IN ?", exhaustedIDs).Delete(&models.InviteCode{})
		if result.Error != nil {
			return total, errors.WithMessage(result.Error, "cleanup exhausted invite codes failed")
		}

		total += result.RowsAffected
	}

	return total, nil
}
