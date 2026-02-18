package feed

import (
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// DBService 封装 feed 相关的数据库操作
type DBService struct {
	db *gorm.DB
}

// NewDBService 创建 DBService 实例
func NewDBService(db *gorm.DB) *DBService {
	return &DBService{db: db}
}

// GetAccountFollowersId 获取账号的粉丝 ID 列表
func (s *DBService) GetAccountFollowersId(accountId uint64) (followers []uint64, err error) {
	followers = make([]uint64, 0)
	err = s.db.Model(&models.Follow{}).Where("follow_id = ?", accountId).Pluck("account_id", &followers).Error
	return
}

// GetAccountRecentActiveLocalFollowersId 获取账号最近活跃的本地粉丝 ID 列表
func (s *DBService) GetAccountRecentActiveLocalFollowersId(accountId uint64) (followers []uint64, err error) {
	followers, err = s.GetAccountFollowersId(accountId)
	if err != nil {
		return
	}

	// 计算活跃时间阈值
	activeThreshold := time.Now().Add(-10 * 24 * time.Hour)

	err = s.db.Model(&models.Account{}).
		Where("id in (?) and last_activity_at > ? and domain is null", followers, activeThreshold).
		Pluck("id", &followers).Error

	return
}

// GetAccountFollowingAccounts 获取账号关注的所有账号
func (s *DBService) GetAccountFollowingAccounts(accountId uint64) (accounts models.Accounts, err error) {
	followingIds := make([]uint64, 0)
	err = s.db.Model(&models.Follow{}).Where("account_id = ?", accountId).Pluck("follow_id", &followingIds).Error
	if err != nil {
		return
	}

	accounts = make(models.Accounts, 0)
	if len(followingIds) == 0 {
		return
	}

	err = s.db.Where("id in (?)", followingIds).Find(&accounts).Error
	return
}

// GetAccountRemoteFollowers 获取账号的远程粉丝
func (s *DBService) GetAccountRemoteFollowers(accountId uint64) (followers []*models.Account, err error) {
	followersId, err := s.GetAccountFollowersId(accountId)
	if err != nil {
		return
	}

	followers = make([]*models.Account, 0)
	if len(followersId) == 0 {
		return
	}

	err = s.db.Model(&models.Account{}).Where("id in (?) and domain is not null", followersId).Find(&followers).Error
	return
}

// CreateTestFollow 创建测试用的关注关系
func (s *DBService) CreateTestFollow(accountId, followId uint64) error {
	return s.db.Create(&models.Follow{
		AccountId: accountId,
		FollowId:  followId,
		Status:    0,
	}).Error
}

// CreateTestAccount 创建测试用的账号
func (s *DBService) CreateTestAccount(account *models.Account) error {
	return s.db.Create(account).Error
}
