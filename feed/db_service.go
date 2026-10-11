package feed

import (
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// DBService 封装 feed 相关的数据库操作.
type DBService struct {
	db *gorm.DB
}

// NewDBService 创建 DBService 实例.
func NewDBService(db *gorm.DB) *DBService {
	return &DBService{db: db}
}

// GetAccountFollowersId 获取账号的粉丝 ID 列表.
func (s *DBService) GetAccountFollowersId(accountId uint64) (followers []uint64, err error) {
	followers = make([]uint64, 0)
	err = s.db.Model(&models.Follow{}).Scopes(models.Followed).Where("follow_id = ?", accountId).Pluck("account_id", &followers).Error

	return
}

// GetAccountRecentActiveLocalFollowersId 获取账号最近活跃的本地粉丝 ID 列表.
func (s *DBService) GetAccountRecentActiveLocalFollowersId(accountId uint64) (followers []uint64, err error) {
	followers = make([]uint64, 0)

	// 计算活跃时间阈值
	activeThreshold := time.Now().Add(-10 * 24 * time.Hour)

	// 使用 JOIN 优化，避免 N+1 查询
	err = s.db.Model(&models.Account{}).
		Joins("JOIN follows ON follows.account_id = accounts.id").
		Where("follows.status = ?", models.FollowStatusFollowed).
		Where("follows.follow_id = ? AND accounts.domain IS NULL", accountId).
		Where("accounts.feed_last_seen_at > ? OR accounts.last_activity_at > ? OR accounts.created_at > ?", activeThreshold, activeThreshold, activeThreshold).
		Pluck("accounts.id", &followers).Error

	return
}

// GetAccountFollowingAccounts 获取账号关注的所有账号.
func (s *DBService) GetAccountFollowingAccounts(accountId uint64) (accounts models.Accounts, err error) {
	accounts = make(models.Accounts, 0)

	// 使用 JOIN 优化，避免 2 次查询
	err = s.db.Model(&models.Account{}).
		Joins("JOIN follows ON follows.follow_id = accounts.id").
		Where("follows.status = ?", models.FollowStatusFollowed).
		Where("follows.account_id = ?", accountId).
		Find(&accounts).Error

	return
}

// GetAccountRemoteFollowers 获取账号的远程粉丝.
func (s *DBService) GetAccountRemoteFollowers(accountId uint64) (followers []*models.Account, err error) {
	followers = make([]*models.Account, 0)

	// 使用 JOIN 优化，避免 2 次查询
	err = s.db.Model(&models.Account{}).
		Joins("JOIN follows ON follows.account_id = accounts.id").
		Where("follows.status = ?", models.FollowStatusFollowed).
		Where("follows.follow_id = ? AND accounts.domain IS NOT NULL", accountId).
		Find(&followers).Error

	return
}

// CreateTestFollow 创建测试用的关注关系.
func (s *DBService) CreateTestFollow(accountId, followId uint64) error {
	return s.db.Create(&models.Follow{
		AccountId: accountId,
		FollowId:  followId,
		Status:    0,
	}).Error
}

// CreateTestAccount 创建测试用的账号.
func (s *DBService) CreateTestAccount(account *models.Account) error {
	return s.db.Create(account).Error
}
