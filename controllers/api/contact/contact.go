package contact

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/matrix"
	"github.com/kasuganosora/community/models"
)

// AddContactRequest 添加联系人请求
type AddContactRequest struct {
	ContactAccountId *uint64 `json:"contact_account_id"` // 本地用户ID
	MatrixUserId     *string `json:"matrix_user_id"`     // Matrix 用户ID
	DisplayName      string  `json:"display_name"`       // 自定义名称
	Message          string  `json:"message"`            // 请求消息
}

// AddContact 添加联系人（本地用户添加 Matrix 用户或本地用户为好友）
func AddContact(c *gin.Context) {
	var req AddContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.ContactAccountId == nil && req.MatrixUserId == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Either contact_account_id or matrix_user_id is required"})
		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// 检查是否已存在
	var existingCount int64
	query := db.GetDB().Model(&models.Contact{}).Where("account_id = ?", currentAccount.Id)
	if req.ContactAccountId != nil {
		query = query.Where("contact_account_id = ?", *req.ContactAccountId)
	}
	if req.MatrixUserId != nil {
		query = query.Where("contact_matrix_id = ?", *req.MatrixUserId)
	}
	query.Count(&existingCount)

	if existingCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Contact already exists"})
		return
	}

	// 确定联系人类型
	contactType := models.ContactTypeLocal
	if req.MatrixUserId != nil {
		contactType = models.ContactTypeMatrix
	}

	contact := &models.Contact{
		AccountId:        currentAccount.Id,
		ContactType:      contactType,
		ContactAccountId: req.ContactAccountId,
		ContactMatrixId:  req.MatrixUserId,
		DisplayName:      req.DisplayName,
		Status:           models.ContactStatusAccepted, // 直接添加为已接受
	}

	if err := db.GetDB().Create(contact).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add contact"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":          contact.Id,
		"type":        contactType,
		"display_name": contact.DisplayName,
	})
}

// GetContacts 获取联系人列表
func GetContacts(c *gin.Context) {
	currentAccount := controllers.MustGetAccount(c)

	var contacts []*models.Contact
	db.GetDB().Where("account_id = ? AND status = ?", currentAccount.Id, models.ContactStatusAccepted).
		Preload("ContactAccount").
		Find(&contacts)

	// 构建响应
	type ContactResponse struct {
		Id               uint64      `json:"id"`
		Type             string      `json:"type"`
		ContactAccountId *uint64     `json:"contact_account_id,omitempty"`
		ContactMatrixId  *string     `json:"contact_matrix_id,omitempty"`
		DisplayName      string      `json:"display_name"`
		ContactAccount   interface{} `json:"contact_account,omitempty"`
	}

	response := make([]ContactResponse, 0, len(contacts))
	for _, contact := range contacts {
		contactType := "local"
		if contact.IsMatrixContact() {
			contactType = "matrix"
		}

		resp := ContactResponse{
			Id:               contact.Id,
			Type:             contactType,
			ContactAccountId: contact.ContactAccountId,
			ContactMatrixId:  contact.ContactMatrixId,
			DisplayName:      contact.DisplayName,
		}

		if contact.ContactAccount != nil {
			resp.ContactAccount = contact.ContactAccount
		}

		response = append(response, resp)
	}

	c.JSON(http.StatusOK, response)
}

// RemoveContact 移除联系人
func RemoveContact(c *gin.Context) {
	contactID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	currentAccount := controllers.MustGetAccount(c)

	result := db.GetDB().Where("id = ? AND account_id = ?", contactID, currentAccount.Id).
		Delete(&models.Contact{})

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Contact removed successfully"})
}

// GetPendingRequests 获取待处理的好友请求
func GetPendingRequests(c *gin.Context) {
	currentAccount := controllers.MustGetAccount(c)

	var requests []*models.ContactRequest
	db.GetDB().Where("to_account_id = ? AND status = ?", currentAccount.Id, models.ContactStatusPending).
		Find(&requests)

	c.JSON(http.StatusOK, requests)
}

// AcceptRequest 接受好友请求
func AcceptRequest(c *gin.Context) {
	requestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request ID"})
		return
	}

	currentAccount := controllers.MustGetAccount(c)

	if !matrix.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not enabled"})
		return
	}

	bridge := matrix.GetBridge()
	if bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not running"})
		return
	}

	contactService := matrix.NewContactService(bridge.GetClient())
	if err := contactService.AcceptContactRequest(requestID, currentAccount.Id); err != nil {
		log.Error("Failed to accept contact request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to accept request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Request accepted"})
}

// RejectRequest 拒绝好友请求
func RejectRequest(c *gin.Context) {
	requestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request ID"})
		return
	}

	currentAccount := controllers.MustGetAccount(c)

	if !matrix.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not enabled"})
		return
	}

	bridge := matrix.GetBridge()
	if bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not running"})
		return
	}

	contactService := matrix.NewContactService(bridge.GetClient())
	if err := contactService.RejectContactRequest(requestID, currentAccount.Id); err != nil {
		log.Error("Failed to reject contact request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Request rejected"})
}

// BlockContact 屏蔽联系人
func BlockContact(c *gin.Context) {
	contactID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	currentAccount := controllers.MustGetAccount(c)

	result := db.GetDB().Model(&models.Contact{}).
		Where("id = ? AND account_id = ?", contactID, currentAccount.Id).
		Update("status", models.ContactStatusBlocked)

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Contact blocked"})
}

// LookupMatrixUser 查找 Matrix 用户（通过本地用户ID获取 Matrix 账号）
func LookupMatrixUser(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid account ID"})
		return
	}

	if !matrix.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not enabled"})
		return
	}

	bridge := matrix.GetBridge()
	if bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not running"})
		return
	}

	contactService := matrix.NewContactService(bridge.GetClient())
	matrixUserID := contactService.GetLocalUserMatrixID(accountID)

	c.JSON(http.StatusOK, gin.H{
		"account_id":      accountID,
		"matrix_user_id":  matrixUserID,
	})
}

// SearchLocalUser 通过 Matrix 用户名查找本地用户
func SearchLocalUser(c *gin.Context) {
	matrixUserID := c.Query("matrix_user_id")
	if matrixUserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "matrix_user_id is required"})
		return
	}

	if !matrix.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not enabled"})
		return
	}

	bridge := matrix.GetBridge()
	if bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not running"})
		return
	}

	contactService := matrix.NewContactService(bridge.GetClient())
	account, err := contactService.LookupLocalAccount(matrixUserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	if account == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"account_id":    account.Id,
		"username":      account.Username,
		"display_name":  account.DisplayName,
		"matrix_user_id": matrixUserID,
	})
}
