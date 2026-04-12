package models

import (
	"strconv"
	"time"

	"gorm.io/gorm"
)

// GroupType 群组类型
type GroupType int

const (
	GroupTypeLocal   GroupType = iota // 纯本地群组
	GroupTypeBridged                  // 与 Matrix 桥接的群组
	GroupTypeMatrix                   // 来源于 Matrix 的群组
)

// Group 群组（支持与 Matrix 房间桥接）
type Group struct {
	Id           uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerId      uint64         `gorm:"index" json:"owner_id"`                                // 创建者（本地用户ID）
	Name         string         `gorm:"type:varchar(255)" json:"name"`                        // 群组名称
	Description  string         `gorm:"type:text" json:"description"`                         // 群组描述
	Avatar       string         `gorm:"type:varchar(512)" json:"avatar"`                      // 群组头像URL
	Type         GroupType      `gorm:"type:tinyint;default:0" json:"type"`                   // 群组类型
	Visibility   string         `gorm:"type:varchar(20);default:'private'" json:"visibility"` // public/private
	MatrixRoomId *string        `gorm:"type:varchar(255);index" json:"matrix_room_id"`        // 关联的 Matrix 房间ID
	CreatedAt    *time.Time     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    *time.Time     `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	// 关联
	Owner    *Account        `gorm:"foreignKey:OwnerId" json:"owner"`
	Members  []*GroupMember  `gorm:"foreignKey:GroupId" json:"members"`
	Messages []*GroupMessage `gorm:"foreignKey:GroupId" json:"messages"`
}

func (g *Group) TableName() string {
	return "groups"
}

// IsBridged 检查是否与 Matrix 桥接
func (g *Group) IsBridged() bool {
	return g.MatrixRoomId != nil && *g.MatrixRoomId != ""
}

// GroupMemberRole 成员角色
type GroupMemberRole int

const (
	GroupMemberRoleMember GroupMemberRole = iota // 普通成员
	GroupMemberRoleAdmin                         // 管理员
	GroupMemberRoleOwner                         // 所有者
)

// GroupMember 群组成员（支持本地用户和 Matrix 用户）
type GroupMember struct {
	Id              uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	GroupId         uint64          `gorm:"index" json:"group_id"`
	AccountId       *uint64         `gorm:"index" json:"account_id"`                       // 本地用户ID（可为空）
	MatrixUserId    *string         `gorm:"type:varchar(255);index" json:"matrix_user_id"` // Matrix 用户ID（可为空）
	DisplayName     string          `gorm:"type:varchar(255)" json:"display_name"`         // 成员显示名称
	Role            GroupMemberRole `gorm:"type:tinyint;default:0" json:"role"`            // 成员角色
	InvitedBy       *uint64         `gorm:"index" json:"invited_by"`                       // 邀请者（本地用户ID）
	MatrixInvitedBy *string         `gorm:"type:varchar(255)" json:"matrix_invited_by"`    // Matrix 邀请者ID
	JoinedAt        *time.Time      `json:"joined_at"`
	CreatedAt       *time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       *time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt       gorm.DeletedAt  `gorm:"index" json:"deleted_at"`

	// 关联
	Group   *Group   `gorm:"foreignKey:GroupId" json:"group"`
	Account *Account `gorm:"foreignKey:AccountId" json:"account"`
}

func (gm *GroupMember) TableName() string {
	return "group_members"
}

// IsLocalUser 检查是否为本地用户
func (gm *GroupMember) IsLocalUser() bool {
	return gm.AccountId != nil
}

// IsMatrixUser 检查是否为 Matrix 用户
func (gm *GroupMember) IsMatrixUser() bool {
	return gm.MatrixUserId != nil
}

// GetUserIdentifier 获取用户标识符
func (gm *GroupMember) GetUserIdentifier() string {
	if gm.AccountId != nil {
		return strconv.FormatUint(*gm.AccountId, 10)
	}
	if gm.MatrixUserId != nil {
		return *gm.MatrixUserId
	}
	return ""
}

// GroupMessage 群组消息
type GroupMessage struct {
	Id             uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	GroupId        uint64         `gorm:"index" json:"group_id"`
	SenderId       *uint64        `gorm:"index" json:"sender_id"`                          // 本地发送者ID
	MatrixSenderId *string        `gorm:"type:varchar(255);index" json:"matrix_sender_id"` // Matrix 发送者ID
	Content        string         `gorm:"type:text" json:"content"`                        // 消息内容
	MatrixEventId  *string        `gorm:"type:varchar(255);index" json:"matrix_event_id"`  // Matrix 事件ID（用于去重）
	CreatedAt      *time.Time     `gorm:"autoCreateTime;index" json:"created_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	// 关联
	Group  *Group   `gorm:"foreignKey:GroupId" json:"group"`
	Sender *Account `gorm:"foreignKey:SenderId" json:"sender"`
}

func (gm *GroupMessage) TableName() string {
	return "group_messages"
}

// IsFromLocal 检查是否来自本地用户
func (gm *GroupMessage) IsFromLocal() bool {
	return gm.SenderId != nil
}

// IsFromMatrix 检查是否来自 Matrix
func (gm *GroupMessage) IsFromMatrix() bool {
	return gm.MatrixSenderId != nil
}
