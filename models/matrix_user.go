package models

import (
	"time"

	"gorm.io/gorm"
)

// MatrixUser Matrix 用户映射模型
// 存储本地用户与 Matrix 虚拟用户的映射关系
type MatrixUser struct {
	Id uint64 `gorm:"primaryKey;autoIncrement" json:"id"`

	// 本地用户 ID（如果是本地用户映射到 Matrix）
	LocalAccountId uint64 `gorm:"index;not null" json:"local_account_id"`

	// Matrix 用户 ID (完整格式：@localpart:domain)
	MatrixUserID string `gorm:"type:varchar(255);uniqueIndex;not null" json:"matrix_user_id"`

	// Matrix 用户名部分（不含域名）
	MatrixLocalpart string `gorm:"type:varchar(128);index;not null" json:"matrix_localpart"`

	// Matrix 服务器域名
	MatrixServer string `gorm:"type:varchar(128);index;not null" json:"matrix_server"`

	// 用户方向：local->matrix 或 matrix->local
	// "local" 表示本地用户映射到 Matrix
	// "remote" 表示 Matrix 用户映射到本地虚拟账号
	Direction string `gorm:"type:varchar(16);index;not null;default:'local'" json:"direction"`

	// Matrix 用户档案缓存
	DisplayName string `gorm:"type:varchar(128)" json:"display_name"`
	AvatarURL   string `gorm:"type:varchar(512)" json:"avatar_url"`

	// 时间戳
	CreatedAt *time.Time     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time     `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	// 关联
	LocalAccount *Account `gorm:"foreignKey:LocalAccountId;references:Id" json:"local_account,omitempty"`
}

func (MatrixUser) TableName() string {
	return "matrix_users"
}

// IsLocalUser 判断是否为本地用户映射到 Matrix
func (m *MatrixUser) IsLocalUser() bool {
	return m.Direction == "local"
}

// IsRemoteUser 判断是否为 Matrix 用户映射到本地
func (m *MatrixUser) IsRemoteUser() bool {
	return m.Direction == "remote"
}

// MatrixRoom Matrix 房间映射模型
// 存储 Matrix 房间与本地 Conversation 的映射关系
type MatrixRoom struct {
	Id uint64 `gorm:"primaryKey;autoIncrement" json:"id"`

	// 本地会话 ID
	ConversationId uint64 `gorm:"uniqueIndex;not null" json:"conversation_id"`

	// Matrix 房间 ID (完整格式：!opaque_id:domain)
	MatrixRoomID string `gorm:"type:varchar(255);uniqueIndex;not null" json:"matrix_room_id"`

	// Matrix 房间名部分
	MatrixRoomLocalpart string `gorm:"type:varchar(128);index" json:"matrix_room_localpart"`

	// Matrix 服务器域名
	MatrixServer string `gorm:"type:varchar(128);index;not null" json:"matrix_server"`

	// 房间是否为私信房间
	IsDirect bool `gorm:"default:true;not null" json:"is_direct"`

	// 房间名称（如果有）
	RoomName string `gorm:"type:varchar(255)" json:"room_name"`

	// 最后同步的事件 ID
	LastEventID string `gorm:"type:varchar(255)" json:"last_event_id"`

	// 最后同步时间
	LastSyncAt *time.Time `gorm:"type:datetime" json:"last_sync_at"`

	// 时间戳
	CreatedAt *time.Time     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time     `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	// 关联
	Conversation *Conversation `gorm:"foreignKey:ConversationId;references:Id" json:"conversation,omitempty"`
}

func (MatrixRoom) TableName() string {
	return "matrix_rooms"
}

// UpdateLastSync 更新最后同步时间和事件 ID
func (m *MatrixRoom) UpdateLastSync(eventID string) {
	now := time.Now()
	m.LastEventID = eventID
	m.LastSyncAt = &now
}

// MatrixMessage Matrix 消息映射模型
// 存储 Matrix 事件与本地 Status 的映射关系（用于避免重复同步）
type MatrixMessage struct {
	Id uint64 `gorm:"primaryKey;autoIncrement" json:"id"`

	// 本地 Status ID
	StatusId uint64 `gorm:"uniqueIndex;not null" json:"status_id"`

	// Matrix 事件 ID
	MatrixEventID string `gorm:"type:varchar(255);uniqueIndex;not null" json:"matrix_event_id"`

	// Matrix 房间 ID
	MatrixRoomID string `gorm:"type:varchar(255);index;not null" json:"matrix_room_id"`

	// Matrix 发送者 ID
	MatrixSenderID string `gorm:"type:varchar(255);index;not null" json:"matrix_sender_id"`

	// 同步方向：matrix->local 或 local->matrix
	Direction string `gorm:"type:varchar(16);index;not null" json:"direction"`

	// 时间戳
	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`

	// 关联
	Status *Status `gorm:"foreignKey:StatusId;references:Id" json:"status,omitempty"`
}

func (MatrixMessage) TableName() string {
	return "matrix_messages"
}
