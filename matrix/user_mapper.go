package matrix

import (
	"fmt"
	"strings"
	"sync"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// UserMapper 用户映射服务
// 负责本地用户与 Matrix 虚拟用户之间的双向映射.
type UserMapper struct {
	client *ASClient
	cache  *userCache
}

type userCache struct {
	sync.RWMutex

	localToMatrix map[uint64]*models.MatrixUser // local_account_id -> MatrixUser
	matrixToLocal map[string]*models.MatrixUser // matrix_user_id -> MatrixUser
}

// NewUserMapper 创建用户映射服务.
func NewUserMapper(client *ASClient) *UserMapper {
	return &UserMapper{
		client: client,
		cache: &userCache{
			localToMatrix: make(map[uint64]*models.MatrixUser),
			matrixToLocal: make(map[string]*models.MatrixUser),
		},
	}
}

// GetOrCreateMatrixUserByLocal 获取或创建本地用户对应的 Matrix 虚拟用户
// 方向：本地用户 -> Matrix.
func (m *UserMapper) GetOrCreateMatrixUserByLocal(localAccountID uint64) (*models.MatrixUser, error) {
	// 1. 先查缓存
	m.cache.RLock()

	if user, ok := m.cache.localToMatrix[localAccountID]; ok {
		m.cache.RUnlock()

		return user, nil
	}

	m.cache.RUnlock()

	// 2. 查数据库
	var matrixUser models.MatrixUser

	err := db.GetDB().Where("local_account_id = ? AND direction = ?", localAccountID, "local").
		First(&matrixUser).Error
	if err == nil {
		m.cacheUser(&matrixUser)

		return &matrixUser, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.Wrap(err, "failed to query matrix user")
	}

	// 3. 不存在则创建新的映射
	return m.createLocalUserMapping(localAccountID)
}

// createLocalUserMapping 为本地用户创建 Matrix 映射.
func (m *UserMapper) createLocalUserMapping(localAccountID uint64) (*models.MatrixUser, error) {
	LoadConfig()

	// 获取本地账户信息
	var account models.Account
	err := db.GetDB().First(&account, localAccountID).Error
	if err != nil {
		return nil, errors.Wrap(err, "failed to find local account")
	}

	// 生成 Matrix localpart: _community_<username>
	matrixLocalpart := GlobalConfig.UserNamespacePrefix + account.Username
	matrixUserID := GlobalConfig.FormatMatrixUserID(matrixLocalpart)

	// 在 Matrix 上注册虚拟用户
	err = m.client.EnsureVirtualUser(matrixLocalpart)
	if err != nil {
		return nil, errors.Wrap(err, "failed to register virtual user on Matrix")
	}

	// 设置用户档案（显示名称和头像）
	displayName := account.DisplayName
	if displayName == "" {
		displayName = account.Username
	}

	err = m.client.SetUserProfile(matrixUserID, displayName, "")
	if err != nil {
		log.Warn("failed to set user profile for %s: %v", matrixUserID, err)
	}

	// 创建数据库记录
	matrixUser := &models.MatrixUser{
		LocalAccountId:  localAccountID,
		MatrixUserID:    matrixUserID,
		MatrixLocalpart: matrixLocalpart,
		MatrixServer:    GlobalConfig.ServerName,
		Direction:       "local",
		DisplayName:     displayName,
	}

	err = db.GetDB().Create(matrixUser).Error
	if err != nil {
		return nil, errors.Wrap(err, "failed to create matrix user mapping")
	}

	m.cacheUser(matrixUser)

	return matrixUser, nil
}

// GetOrCreateLocalUserByMatrix 获取或创建 Matrix 用户对应的本地虚拟账号
// 方向：Matrix -> 本地.
func (m *UserMapper) GetOrCreateLocalUserByMatrix(matrixUserID string) (*models.MatrixUser, error) {
	// 1. 先查缓存
	m.cache.RLock()

	if user, ok := m.cache.matrixToLocal[matrixUserID]; ok {
		m.cache.RUnlock()

		return user, nil
	}

	m.cache.RUnlock()

	// 2. 查数据库
	var matrixUser models.MatrixUser

	err := db.GetDB().Where("matrix_user_id = ? AND direction = ?", matrixUserID, "remote").
		First(&matrixUser).Error
	if err == nil {
		m.cacheUser(&matrixUser)

		return &matrixUser, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.Wrap(err, "failed to query matrix user")
	}

	// 3. 不存在则创建新的映射
	return m.createRemoteUserMapping(matrixUserID)
}

// createRemoteUserMapping 为 Matrix 用户创建本地虚拟账号映射.
func (m *UserMapper) createRemoteUserMapping(matrixUserID string) (*models.MatrixUser, error) {
	LoadConfig()

	// 解析 Matrix 用户 ID
	localpart, server, ok := ParseMatrixUserID(matrixUserID)
	if !ok {
		return nil, fmt.Errorf("invalid matrix user id: %s", matrixUserID)
	}

	// 检查是否是本地用户的映射（避免循环）
	if strings.HasPrefix(localpart, GlobalConfig.UserNamespacePrefix) {
		return nil, errors.New("matrix user is a local mapping, use GetOrCreateMatrixUserByLocal instead")
	}

	// 获取 Matrix 用户档案
	profile, err := m.client.GetUserProfile(matrixUserID)
	if err != nil {
		// 如果获取失败，使用默认值
		profile = &UserProfile{}
	}

	// 生成本地虚拟用户名: matrix_<localpart>_<server>
	// 将 server 中的 . 替换为 _
	safeServer := strings.ReplaceAll(server, ".", "_")
	localUsername := fmt.Sprintf("matrix_%s_%s", localpart, safeServer)

	// 创建本地虚拟账号
	account := &models.Account{
		Username:    localUsername,
		DisplayName: profile.DisplayName,
		Note:        "Matrix user from " + server,
		Domain:      &server, // 标记为远程账号
		OriginId:    matrixUserID,
		Bot:         false,
		Locked:      false,
	}

	tx := db.GetDB().Begin()
	if err := tx.Create(account).Error; err != nil {
		tx.Rollback()

		return nil, errors.Wrap(err, "failed to create local virtual account")
	}

	// 创建映射记录
	matrixUser := &models.MatrixUser{
		LocalAccountId:  account.Id,
		MatrixUserID:    matrixUserID,
		MatrixLocalpart: localpart,
		MatrixServer:    server,
		Direction:       "remote",
		DisplayName:     profile.DisplayName,
		AvatarURL:       profile.AvatarURL,
	}

	if err := tx.Create(matrixUser).Error; err != nil {
		tx.Rollback()

		return nil, errors.Wrap(err, "failed to create matrix user mapping")
	}

	if err := tx.Commit().Error; err != nil {
		return nil, errors.Wrap(err, "failed to commit transaction")
	}

	matrixUser.LocalAccount = account
	m.cacheUser(matrixUser)

	return matrixUser, nil
}

// GetLocalAccountID 获取 Matrix 用户对应的本地账号 ID.
func (m *UserMapper) GetLocalAccountID(matrixUserID string) (uint64, error) {
	matrixUser, err := m.GetOrCreateLocalUserByMatrix(matrixUserID)
	if err != nil {
		return 0, err
	}

	return matrixUser.LocalAccountId, nil
}

// GetMatrixUserID 获取本地用户对应的 Matrix 用户 ID.
func (m *UserMapper) GetMatrixUserID(localAccountID uint64) (string, error) {
	matrixUser, err := m.GetOrCreateMatrixUserByLocal(localAccountID)
	if err != nil {
		return "", err
	}

	return matrixUser.MatrixUserID, nil
}

// IsMatrixUser 检查 Matrix 用户 ID 是否属于本系统的映射.
func (m *UserMapper) IsMatrixUser(matrixUserID string) bool {
	LoadConfig()

	localpart, _, ok := ParseMatrixUserID(matrixUserID)
	if !ok {
		return false
	}

	return strings.HasPrefix(localpart, GlobalConfig.UserNamespacePrefix)
}

// GetLocalAccountFromMatrixUser 从 Matrix 用户 ID 获取本地账号.
func (m *UserMapper) GetLocalAccountFromMatrixUser(matrixUserID string) (*models.Account, error) {
	matrixUser, err := m.GetOrCreateLocalUserByMatrix(matrixUserID)
	if err != nil {
		return nil, err
	}

	var account models.Account
	if err := db.GetDB().First(&account, matrixUser.LocalAccountId).Error; err != nil {
		return nil, errors.Wrap(err, "failed to find local account")
	}

	return &account, nil
}

// UpdateUserProfile 更新 Matrix 用户的档案信息.
func (m *UserMapper) UpdateUserProfile(matrixUserID string, displayName, avatarURL string) error {
	err := db.GetDB().Model(&models.MatrixUser{}).
		Where("matrix_user_id = ?", matrixUserID).
		Updates(map[string]any{
			"display_name": displayName,
			"avatar_url":   avatarURL, //nolint:goconst
		}).Error
	if err != nil {
		return errors.Wrap(err, "failed to update matrix user profile")
	}

	// 更新缓存（需要写锁，因为修改了缓存对象的字段）
	m.cache.Lock()
	if user, ok := m.cache.matrixToLocal[matrixUserID]; ok {
		user.DisplayName = displayName
		user.AvatarURL = avatarURL
	}
	m.cache.Unlock()

	return nil
}

// cacheUser 缓存用户映射.
func (m *UserMapper) cacheUser(user *models.MatrixUser) {
	m.cache.Lock()
	defer m.cache.Unlock()

	if user.IsLocalUser() {
		m.cache.localToMatrix[user.LocalAccountId] = user
	}

	m.cache.matrixToLocal[user.MatrixUserID] = user
}

// ClearCache 清除缓存.
func (m *UserMapper) ClearCache() {
	m.cache.Lock()
	defer m.cache.Unlock()

	m.cache.localToMatrix = make(map[uint64]*models.MatrixUser)
	m.cache.matrixToLocal = make(map[string]*models.MatrixUser)
}
