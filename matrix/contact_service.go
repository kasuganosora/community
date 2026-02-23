package matrix

import (
	"strconv"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// ContactService 联系人服务（支持跨平台好友）
type ContactService struct {
	client *ASClient
}

// NewContactService 创建联系人服务
func NewContactService(client *ASClient) *ContactService {
	return &ContactService{client: client}
}

// LookupLocalAccount 通过 Matrix 用户名查找本地用户
// Matrix 用户可以通过 @_community_username:server.com 格式查找本地用户
// 例如本地用户 alice 的 Matrix ID 为 @_community_alice:server.com
func (cs *ContactService) LookupLocalAccount(matrixUserID string) (*models.Account, error) {
	// 解析 Matrix 用户 ID（使用统一的解析函数）
	localpart, _, ok := ParseMatrixUserID(matrixUserID)
	if !ok {
		return nil, errors.New("invalid Matrix user ID format")
	}

	// 检查是否为本地用户的虚拟 Matrix 账号
	// 格式: _community_username (例如 _community_alice)
	LoadConfig()
	prefix := GlobalConfig.UserNamespacePrefix
	if !strings.HasPrefix(localpart, prefix) {
		return nil, errors.New("not a local user namespace")
	}

	// 提取本地用户名
	username := strings.TrimPrefix(localpart, prefix)
	if username == "" {
		return nil, errors.New("empty username")
	}

	// 查找本地用户
	account := &models.Account{}
	if err := db.GetDB().Where("username = ?", username).First(account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return account, nil
}

// AddContact 添加联系人（本地用户添加 Matrix 用户为好友）
func (cs *ContactService) AddContact(accountID uint64, matrixUserID, displayName string) error {
	contact := &models.Contact{
		AccountId:       accountID,
		ContactType:     models.ContactTypeMatrix,
		ContactMatrixId: &matrixUserID,
		DisplayName:     displayName,
		Status:          models.ContactStatusAccepted,
	}

	if err := db.GetDB().Create(contact).Error; err != nil {
		return errors.Wrap(err, "failed to add contact")
	}

	log.Info("Added Matrix contact %s for account %d", matrixUserID, accountID)
	return nil
}

// CreateContactRequest 创建好友请求（Matrix 用户请求添加本地用户）
func (cs *ContactService) CreateContactRequest(fromMatrixUserID string, toAccountID uint64, message string) (*models.ContactRequest, error) {
	// 检查是否已存在请求
	existing := &models.ContactRequest{}
	err := db.GetDB().Where(
		"from_matrix_user_id = ? AND to_account_id = ? AND status = ?",
		fromMatrixUserID, toAccountID, models.ContactStatusPending,
	).First(existing).Error

	if err == nil {
		return existing, nil // 已存在待处理的请求
	}

	// 创建新请求
	request := &models.ContactRequest{
		FromMatrixUserId: &fromMatrixUserID,
		ToAccountId:      &toAccountID,
		Message:          message,
		Status:           models.ContactStatusPending,
	}

	if err := db.GetDB().Create(request).Error; err != nil {
		return nil, errors.Wrap(err, "failed to create contact request")
	}

	log.Info("Created contact request from Matrix user %s to account %d", fromMatrixUserID, toAccountID)
	return request, nil
}

// AcceptContactRequest 接受好友请求
func (cs *ContactService) AcceptContactRequest(requestID uint64, accountID uint64) error {
	request := &models.ContactRequest{}
	if err := db.GetDB().Where("id = ? AND to_account_id = ?", requestID, accountID).First(request).Error; err != nil {
		return errors.Wrap(err, "request not found")
	}

	// 更新请求状态
	request.Status = models.ContactStatusAccepted
	if err := db.GetDB().Save(request).Error; err != nil {
		return errors.Wrap(err, "failed to update request")
	}

	// 创建双向联系人关系
	// 本地用户 -> Matrix 用户
	if request.FromMatrixUserId != nil {
		contact := &models.Contact{
			AccountId:       accountID,
			ContactType:     models.ContactTypeMatrix,
			ContactMatrixId: request.FromMatrixUserId,
			Status:          models.ContactStatusAccepted,
		}
		if err := db.GetDB().Create(contact).Error; err != nil {
			return errors.Wrap(err, "failed to create contact for accepted request")
		}
	}

	log.Info("Accepted contact request %d", requestID)
	return nil
}

// RejectContactRequest 拒绝好友请求
func (cs *ContactService) RejectContactRequest(requestID uint64, accountID uint64) error {
	result := db.GetDB().Model(&models.ContactRequest{}).
		Where("id = ? AND to_account_id = ?", requestID, accountID).
		Update("status", models.ContactStatusBlocked)

	if result.Error != nil {
		return result.Error
	}

	log.Info("Rejected contact request %d", requestID)
	return nil
}

// GetContacts 获取用户的联系人列表
func (cs *ContactService) GetContacts(accountID uint64) ([]*models.Contact, error) {
	contacts := make([]*models.Contact, 0)
	err := db.GetDB().Where("account_id = ? AND status = ?", accountID, models.ContactStatusAccepted).
		Find(&contacts).Error
	return contacts, err
}

// GetPendingContactRequests 获取待处理的好友请求
func (cs *ContactService) GetPendingContactRequests(accountID uint64) ([]*models.ContactRequest, error) {
	requests := make([]*models.ContactRequest, 0)
	err := db.GetDB().Where("to_account_id = ? AND status = ?", accountID, models.ContactStatusPending).
		Find(&requests).Error
	return requests, err
}

// RemoveContact 移除联系人
func (cs *ContactService) RemoveContact(accountID uint64, contactID uint64) error {
	result := db.GetDB().Where("id = ? AND account_id = ?", contactID, accountID).
		Delete(&models.Contact{})

	if result.Error != nil {
		return result.Error
	}

	log.Info("Removed contact %d for account %d", contactID, accountID)
	return nil
}

// BlockMatrixUser 屏蔽 Matrix 用户
func (cs *ContactService) BlockMatrixUser(accountID uint64, matrixUserID string) error {
	contact := &models.Contact{
		AccountId:       accountID,
		ContactType:     models.ContactTypeMatrix,
		ContactMatrixId: &matrixUserID,
		Status:          models.ContactStatusBlocked,
	}

	// 使用 upsert
	return db.GetDB().Assign(map[string]interface{}{
		"status": models.ContactStatusBlocked,
	}).FirstOrCreate(contact).Error
}

// IsBlocked 检查 Matrix 用户是否被屏蔽
func (cs *ContactService) IsBlocked(accountID uint64, matrixUserID string) (bool, error) {
	var count int64
	err := db.GetDB().Model(&models.Contact{}).
		Where("account_id = ? AND contact_matrix_id = ? AND status = ?",
			accountID, matrixUserID, models.ContactStatusBlocked).
		Count(&count).Error
	return count > 0, err
}

// GetLocalUserMatrixID 获取本地用户的 Matrix ID
// 返回格式: @_community_username:server.com (例如 @_community_alice:matrix.org)
func (cs *ContactService) GetLocalUserMatrixID(accountID uint64) string {
	// 查询用户名
	account := &models.Account{}
	if err := db.GetDB().Select("username").First(account, accountID).Error; err != nil {
		// 如果查询失败，回退到使用 ID
		LoadConfig()
		return GlobalConfig.FormatMatrixUserID(GlobalConfig.UserNamespacePrefix + strconv.FormatUint(accountID, 10))
	}

	LoadConfig()
	return GlobalConfig.FormatMatrixUserID(GlobalConfig.UserNamespacePrefix + account.Username)
}

// GetLocalUserMatrixIDByUsername 通过用户名获取 Matrix ID
// 返回格式: @_community_username:server.com
func (cs *ContactService) GetLocalUserMatrixIDByUsername(username string) string {
	LoadConfig()
	return GlobalConfig.FormatMatrixUserID(GlobalConfig.UserNamespacePrefix + username)
}
