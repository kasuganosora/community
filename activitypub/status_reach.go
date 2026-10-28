package activitypub

import (
	"context"
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// reachInbox resolves one remote inbox. A fetch, timeout, or parse error is
// logged with the actor URL and skipped so the rest of the fanout continues.
func reachInbox(account *models.Account) (string, bool) {
	inboxURL, err := remoteActorInbox(context.Background(), account)
	if err != nil {
		target := account.OriginId
		if target == "" {
			target = fmt.Sprintf("acct:%d", account.Id)
		}
		log.Warn("Federation reach: target %s: %v", target, err)
		return "", false
	}
	return inboxURL, true
}

// StatusReachFinder 状态触达查找器
// 用于确定一个状态应该被分发到哪些 inbox.
type StatusReachFinder struct {
	db     *gorm.DB
	status *models.Status
	unsafe bool // 是否包含被屏蔽/暂停的用户
}

// NewStatusReachFinder 创建状态触达查找器.
func NewStatusReachFinder(status *models.Status, unsafe bool) *StatusReachFinder {
	return &StatusReachFinder{
		db:     db.GetDB(),
		status: status,
		unsafe: unsafe,
	}
}

// GetInboxes 获取所有应该接收该状态的 inbox URL.
func (f *StatusReachFinder) GetInboxes() ([]string, error) {
	var inboxes []string

	seen := make(map[string]bool)

	// 1. 获取粉丝的 inbox
	followerInboxes, err := f.getFollowerInboxes()
	if err != nil {
		return nil, err
	}

	for _, inbox := range followerInboxes {
		if !seen[inbox] {
			inboxes = append(inboxes, inbox)
			seen[inbox] = true
		}
	}

	// 2. 获取被提及用户的 inbox
	mentionInboxes, err := f.getMentionInboxes()
	if err != nil {
		return nil, err
	}

	for _, inbox := range mentionInboxes {
		if !seen[inbox] {
			inboxes = append(inboxes, inbox)
			seen[inbox] = true
		}
	}

	// 3. 如果是公开状态，添加中继的 inbox
	if f.isPublicVisibility() {
		relayInboxes, err := f.getRelayInboxes()
		if err != nil {
			return nil, err
		}

		for _, inbox := range relayInboxes {
			if !seen[inbox] {
				inboxes = append(inboxes, inbox)
				seen[inbox] = true
			}
		}
	}

	return inboxes, nil
}

// getFollowerInboxes 获取粉丝的 inbox URL.
func (f *StatusReachFinder) getFollowerInboxes() ([]string, error) {
	var inboxes []string
	// Direct messages are never distributed to followers.
	if f.status.Visibility == models.StatusVisibilityDirect {
		return inboxes, nil
	}

	// 获取账户的粉丝
	var follows []models.Follow

	err := f.db.Where("follow_id = ? AND status = ?", f.status.AccountId, models.FollowStatusFollowed).
		Preload("Account").
		Find(&follows).Error
	if err != nil {
		return nil, err
	}

	for _, follow := range follows {
		if follow.Account != nil && follow.Account.Domain != nil {
			// 检查是否被屏蔽（如果不是 unsafe 模式）
			if !f.unsafe && f.isAccountBlocked(follow.Account.Id) {
				continue
			}
			// 远程账户，构建 inbox URL。一个失败的 actor 不影响其余粉丝。
			if inboxURL, ok := reachInbox(follow.Account); ok {
				inboxes = append(inboxes, inboxURL)
			}
		}
	}

	return inboxes, nil
}

// getMentionInboxes 获取被提及用户的 inbox URL.
func (f *StatusReachFinder) getMentionInboxes() ([]string, error) { //nolint:unparam // param reserved for future use
	var inboxes []string

	// 获取状态中提及的账户
	mentions := f.getStatusMentions()
	for _, mention := range mentions {
		if mention.Domain != nil {
			if !f.unsafe && f.isAccountBlocked(mention.Id) {
				continue
			}
			// 远程账户，构建 inbox URL。一个失败的 actor 不影响其余提及。
			if inboxURL, ok := reachInbox(mention); ok {
				inboxes = append(inboxes, inboxURL)
			}
		}
	}

	return inboxes, nil
}

// getRelayInboxes 获取中继的 inbox URL.
func (f *StatusReachFinder) getRelayInboxes() ([]string, error) {
	// 只有公开状态才发送到中继
	if f.status.Visibility != models.StatusVisibilityPublic {
		return nil, nil
	}

	// 获取所有已启用的中继
	return GetEnabledRelayInboxes()
}

// isPublicVisibility 检查状态是否是公开可见.
func (f *StatusReachFinder) isPublicVisibility() bool {
	return f.status.Visibility == models.StatusVisibilityPublic || f.status.Visibility == models.StatusVisibilityUnlisted
}

// isAccountBlocked 检查账户是否被屏蔽.
func (f *StatusReachFinder) isAccountBlocked(accountId uint64) bool {
	var block models.Block

	err := f.db.Where("account_id = ? AND target_account_id = ?", f.status.AccountId, accountId).
		First(&block).Error

	return err == nil
}

// getStatusMentions 获取状态中提及的账户.
func (f *StatusReachFinder) getStatusMentions() []*models.Account {
	// Authorized mentions are normalized stored relationships; never reparse
	// arbitrary rendered HTML to invent direct-message recipients.
	if f.status.ToAccounts != nil {
		return f.status.ToAccounts
	}
	var accounts []*models.Account
	if f.status.Id > 0 {
		_ = f.db.Model(f.status).Association("ToAccounts").Find(&accounts)
	}
	return accounts
}

// GetEnabledRelayInboxes 获取所有已启用中继的 inbox URL（全局函数）.
func GetEnabledRelayInboxes() ([]string, error) {
	return models.GetEnabledRelayInboxes(db.GetDB())
}

// AccountReachFinder 账户触达查找器
// 用于确定账户相关的活动应该被分发到哪些 inbox.
type AccountReachFinder struct {
	db      *gorm.DB
	account *models.Account
}

// NewAccountReachFinder 创建账户触达查找器.
func NewAccountReachFinder(account *models.Account) *AccountReachFinder {
	return &AccountReachFinder{
		db:      db.GetDB(),
		account: account,
	}
}

// GetInboxes 获取所有应该接收账户活动的 inbox URL.
func (f *AccountReachFinder) GetInboxes() ([]string, error) {
	var inboxes []string

	seen := make(map[string]bool)

	// 1. 获取粉丝的 inbox
	followerInboxes, err := f.getFollowerInboxes()
	if err != nil {
		return nil, err
	}

	for _, inbox := range followerInboxes {
		if !seen[inbox] {
			inboxes = append(inboxes, inbox)
			seen[inbox] = true
		}
	}

	// 2. 添加中继的 inbox
	relayInboxes, err := f.getRelayInboxes()
	if err != nil {
		return nil, err
	}

	for _, inbox := range relayInboxes {
		if !seen[inbox] {
			inboxes = append(inboxes, inbox)
			seen[inbox] = true
		}
	}

	return inboxes, nil
}

// getFollowerInboxes 获取粉丝的 inbox URL.
func (f *AccountReachFinder) getFollowerInboxes() ([]string, error) {
	var inboxes []string

	var follows []models.Follow

	err := f.db.Where("follow_id = ? AND status = ?", f.account.Id, models.FollowStatusFollowed).
		Preload("Account").
		Find(&follows).Error
	if err != nil {
		return nil, err
	}

	for _, follow := range follows {
		if follow.Account != nil && follow.Account.Domain != nil {
			// 远程账户，构建 inbox URL。一个失败的 actor 不影响其余粉丝。
			if inboxURL, ok := reachInbox(follow.Account); ok {
				inboxes = append(inboxes, inboxURL)
			}
		}
	}

	return inboxes, nil
}

// getRelayInboxes 获取中继的 inbox URL.
func (f *AccountReachFinder) getRelayInboxes() ([]string, error) {
	return GetEnabledRelayInboxes()
}

// DistributeToRelays 将活动分发到所有已启用的中继.
func DistributeToRelays(activity map[string]any, activityID string) error {
	inboxes, err := GetEnabledRelayInboxes()
	if err != nil {
		return err
	}

	if len(inboxes) == 0 {
		return nil
	}

	// 使用投递队列批量发送
	queue := NewDeliveryQueue(nil, "", 0) // 实际使用时需要正确的私钥和 actor ID

	return queue.EnqueueToMany(activityID, inboxes, activity)
}

// IsRequestedThroughRelay 检查活动是否通过中继请求.
func IsRequestedThroughRelay(inboxURL string) bool {
	relay, err := models.GetRelayByInboxURL(db.GetDB(), inboxURL)
	if err != nil {
		return false
	}

	return relay.IsEnabled()
}
