package models

import (
	"time"

	"gorm.io/gorm"
)

// ContactType 联系人类型
type ContactType int

const (
	ContactTypeLocal  ContactType = iota // 本地用户
	ContactTypeMatrix                    // Matrix 用户
)

// ContactStatus 联系人状态
type ContactStatus int

const (
	ContactStatusPending  ContactStatus = iota // 等待确认
	ContactStatusAccepted                      // 已接受
	ContactStatusBlocked                       // 已屏蔽
)

// Contact 联系人（本地用户与其他用户的联系关系）
// 用于支持 Matrix 用户添加本地用户为好友
type Contact struct {
	Id              uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountId       uint64         `gorm:"index" json:"account_id"`        // 本地用户ID
	ContactType     ContactType    `gorm:"type:tinyint" json:"contact_type"` // 联系人类型
	ContactAccountId *uint64       `gorm:"index" json:"contact_account_id"` // 联系人（本地用户）
	ContactMatrixId *string        `gorm:"type:varchar(255);index" json:"contact_matrix_id"` // 联系人（Matrix用户）
	DisplayName     string         `gorm:"type:varchar(255)" json:"display_name"` // 自定义显示名称
	Status          ContactStatus  `gorm:"type:tinyint;default:0" json:"status"` // 状态
	Notes           string         `gorm:"type:text" json:"notes"`         // 备注
	CreatedAt       *time.Time     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       *time.Time     `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	// 关联
	Account         *Account `gorm:"foreignKey:AccountId" json:"account"`
	ContactAccount  *Account `gorm:"foreignKey:ContactAccountId" json:"contact_account"`
}

func (c *Contact) TableName() string {
	return "contacts"
}

// IsLocalContact 检查是否为本地联系人
func (c *Contact) IsLocalContact() bool {
	return c.ContactType == ContactTypeLocal
}

// IsMatrixContact 检查是否为 Matrix 联系人
func (c *Contact) IsMatrixContact() bool {
	return c.ContactType == ContactTypeMatrix
}

// ContactRequest 好友请求（跨平台）
type ContactRequest struct {
	Id               uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	FromAccountId    *uint64        `gorm:"index" json:"from_account_id"`    // 发起者（本地用户）
	FromMatrixUserId *string        `gorm:"type:varchar(255);index" json:"from_matrix_user_id"` // 发起者（Matrix用户）
	ToAccountId      *uint64        `gorm:"index" json:"to_account_id"`      // 接收者（本地用户）
	ToMatrixUserId   *string        `gorm:"type:varchar(255);index" json:"to_matrix_user_id"` // 接收者（Matrix用户）
	Message          string         `gorm:"type:text" json:"message"`        // 请求消息
	Status           ContactStatus  `gorm:"type:tinyint;default:0" json:"status"` // 状态
	CreatedAt        *time.Time     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        *time.Time     `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

func (cr *ContactRequest) TableName() string {
	return "contact_requests"
}

// IsFromLocal 检查请求是否来自本地用户
func (cr *ContactRequest) IsFromLocal() bool {
	return cr.FromAccountId != nil
}

// IsFromMatrix 检查请求是否来自 Matrix 用户
func (cr *ContactRequest) IsFromMatrix() bool {
	return cr.FromMatrixUserId != nil
}
