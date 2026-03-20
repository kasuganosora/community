package homeserver

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ===== 基础事件模型 =====

// EventType Matrix 事件类型
type EventType string

const (
	EventTypeRoomCreate             EventType = "m.room.create"
	EventTypeRoomName               EventType = "m.room.name"
	EventTypeRoomTopic              EventType = "m.room.topic"
	EventTypeRoomAvatar             EventType = "m.room.avatar"
	EventTypeRoomMember             EventType = "m.room.member"
	EventTypeRoomJoinRules          EventType = "m.room.join_rules"
	EventTypeRoomPowerLevels        EventType = "m.room.power_levels"
	EventTypeRoomMessage            EventType = "m.room.message"
	EventTypeRoomRedaction          EventType = "m.room.redaction"
	EventTypeRoomHistoryVisibility  EventType = "m.room.history_visibility"
	EventTypeRoomCanonicalAlias     EventType = "m.room.canonical_alias"
	EventTypeRoomAliases            EventType = "m.room.aliases"
	EventTypeRoomGuestAccess        EventType = "m.room.guest_access"
	EventTypeRoomEncryption         EventType = "m.room.encryption"
	EventTypeRoomKey                EventType = "m.room.key"
	EventTypeRoomKeyRequest         EventType = "m.room.key_request"
)

// MembershipState 成员状态
type MembershipState string

const (
	MembershipInvite MembershipState = "invite"
	MembershipJoin   MembershipState = "join"
	MembershipLeave  MembershipState = "leave"
	MembershipBan    MembershipState = "ban"
	MembershipKnock  MembershipState = "knock"
)

// ===== MatrixEvent 事件 =====

// MatrixEvent Matrix 事件（核心数据结构）
type MatrixEvent struct {
	EventID        string                 `gorm:"primaryKey;type:varchar(255)" json:"event_id"`
	RoomID         string                 `gorm:"index;type:varchar(255);not null" json:"room_id"`
	EventType      EventType              `gorm:"type:varchar(64);not null" json:"type"`
	Sender         string                 `gorm:"type:varchar(255);not null" json:"sender"`
	StateKey       *string                `gorm:"type:varchar(255)" json:"state_key"`
	Content        map[string]interface{} `gorm:"type:text;serializer:json" json:"content"`
	PrevEvents     []string               `gorm:"type:json;serializer:json" json:"prev_events"`
	AuthEvents     []string               `gorm:"type:json;serializer:json" json:"auth_events"`
	Depth          int64                  `gorm:"not null" json:"depth"`
	OriginServerTS int64                  `gorm:"not null;index" json:"origin_server_ts"`
	Redacts        *string                `gorm:"type:varchar(255)" json:"redacts"`
	SenderDomain   string                 `gorm:"type:varchar(128)" json:"sender_domain"`

	// 签名（联邦用）
	Signatures map[string]map[string]string `gorm:"type:json;serializer:json" json:"signatures,omitempty"`

	// 客户端事务 ID（用于 unsigned.transaction_id）
	TxnID    *string `gorm:"type:varchar(255);index" json:"-"`
	SenderDeviceID string `gorm:"type:varchar(255)" json:"-"`

	// 关联
	Room *MatrixRoom `gorm:"foreignKey:RoomID;references:RoomID" json:"-"`

	CreatedAt *time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (MatrixEvent) TableName() string {
	return "matrix_events"
}

func (e *MatrixEvent) BeforeCreate(tx *gorm.DB) error {
	if e.EventID == "" {
		e.EventID = "$" + uuid.New().String()
	}
	if e.OriginServerTS == 0 {
		e.OriginServerTS = time.Now().UnixMilli()
	}
	return nil
}

// IsStateEvent 判断是否为状态事件
func (e *MatrixEvent) IsStateEvent() bool {
	return e.StateKey != nil
}

// ToClientEvent 转换为客户端事件格式
func (e *MatrixEvent) ToClientEvent() *ClientEvent {
	return e.ToClientEventForDevice("")
}

// ToClientEventForDevice 转换为客户端事件格式，并为指定设备包含 transaction_id
func (e *MatrixEvent) ToClientEventForDevice(deviceID string) *ClientEvent {
	ce := &ClientEvent{
		EventID:        e.EventID,
		RoomID:         e.RoomID,
		Sender:         e.Sender,
		OriginServerTS: e.OriginServerTS,
		Type:           string(e.EventType),
		StateKey:       e.StateKey,
		Content:        e.Content,
		Redacts:        e.Redacts,
	}
	// Include transaction_id in unsigned only for the device that sent the event
	if e.TxnID != nil && *e.TxnID != "" && deviceID != "" && e.SenderDeviceID == deviceID {
		ce.Unsigned = map[string]interface{}{
			"transaction_id": *e.TxnID,
		}
	}
	return ce
}

// ClientEvent 客户端事件格式
type ClientEvent struct {
	EventID        string                 `json:"event_id,omitempty"`
	RoomID         string                 `json:"room_id,omitempty"`
	Sender         string                 `json:"sender"`
	OriginServerTS int64                  `json:"origin_server_ts"`
	Type           string                 `json:"type"`
	StateKey       *string                `json:"state_key,omitempty"`
	Content        map[string]interface{} `json:"content"`
	Redacts        *string                `json:"redacts,omitempty"`
	Unsigned       map[string]interface{} `json:"unsigned,omitempty"`
}

// ===== MatrixRoom 房间状态 =====

// MatrixRoom 房间状态（当前状态快照）
type MatrixRoom struct {
	RoomID            string     `gorm:"primaryKey;type:varchar(255)" json:"room_id"`
	RoomVersion       string     `gorm:"type:varchar(16);default:'10'" json:"room_version"`
	RoomType          *string    `gorm:"type:varchar(32)" json:"room_type"`
	Creator           string     `gorm:"type:varchar(255)" json:"creator"`
	Name              *string    `gorm:"type:varchar(255)" json:"name"`
	Topic             *string    `gorm:"type:varchar(512)" json:"topic"`
	AvatarURL         *string    `gorm:"type:varchar(512)" json:"avatar_url"`
	JoinRule          string     `gorm:"type:varchar(32);default:'invite'" json:"join_rule"`
	Visibility        string     `gorm:"type:varchar(16);default:'private'" json:"visibility"`
	HistoryVisibility string     `gorm:"type:varchar(32);default:'shared'" json:"history_visibility"`
	GuestAccess       string     `gorm:"type:varchar(32);default:'forbidden'" json:"guest_access"`
	IsDirect          bool       `gorm:"default:false" json:"is_direct"`
	IsEncrypted       bool       `gorm:"default:false" json:"is_encrypted"`
	CurrentStateID    *string    `gorm:"type:varchar(255)" json:"current_state_id"`

	// 联邦相关
	LastEventID *string    `gorm:"type:varchar(255)" json:"last_event_id"`
	LastEventTS *int64     `json:"last_event_ts"`

	// 关联本地会话（可选）
	ConversationID *uint64 `gorm:"index" json:"conversation_id"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (MatrixRoom) TableName() string {
	return "matrix_rooms_hs"
}

// ===== MatrixRoomState 状态键值对 =====

// MatrixRoomState 房间状态（当前状态表）
type MatrixRoomState struct {
	ID        uint64                 `gorm:"primaryKey;autoIncrement" json:"id"`
	RoomID    string                 `gorm:"index;type:varchar(255);not null" json:"room_id"`
	EventType EventType              `gorm:"type:varchar(64);not null" json:"type"`
	StateKey  string                 `gorm:"type:varchar(255);not null" json:"state_key"`
	Content   map[string]interface{} `gorm:"type:text;serializer:json" json:"content"`
	EventID   string                 `gorm:"type:varchar(255)" json:"event_id"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (MatrixRoomState) TableName() string {
	return "matrix_room_state"
}

// ===== MatrixRoomMember 房间成员 =====

// MatrixRoomMember 房间成员状态
type MatrixRoomMember struct {
	ID          uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	RoomID      string          `gorm:"index;type:varchar(255);not null" json:"room_id"`
	UserID      string          `gorm:"index;type:varchar(255);not null" json:"user_id"`
	Membership  MembershipState `gorm:"type:varchar(16);not null" json:"membership"`
	PowerLevel  int             `gorm:"default:0" json:"power_level"`
	DisplayName *string         `gorm:"type:varchar(128)" json:"display_name"`
	AvatarURL   *string         `gorm:"type:varchar(512)" json:"avatar_url"`
	IsDirect    *bool           `json:"is_direct,omitempty"`
	EventID     string          `gorm:"type:varchar(255)" json:"event_id"`
	Forgotten   bool            `gorm:"default:false" json:"forgotten"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (MatrixRoomMember) TableName() string {
	return "matrix_room_members"
}

// ===== MatrixUserSession 用户会话 =====

// MatrixUserSession Matrix 用户会话
type MatrixUserSession struct {
	ID           uint64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       string  `gorm:"index;type:varchar(255);not null" json:"user_id"`
	AccessToken  string  `gorm:"uniqueIndex;type:varchar(255);not null" json:"access_token"`
	RefreshToken *string `gorm:"type:varchar(255)" json:"refresh_token"`
	DeviceID     string  `gorm:"index;type:varchar(255);not null" json:"device_id"`
	DisplayName  *string `gorm:"type:varchar(128)" json:"display_name"`
	LastActiveTS *int64  `json:"last_active_ts"`

	// 关联本地用户
	LocalAccountID *uint64 `gorm:"index" json:"local_account_id"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (MatrixUserSession) TableName() string {
	return "matrix_user_sessions"
}

// ===== MatrixToDeviceToDevice 消息 =====

// MatrixToDeviceMessage To-Device 消息
type MatrixToDeviceMessage struct {
	ID           uint64                 `gorm:"primaryKey;autoIncrement" json:"id"`
	Sender       string                 `gorm:"index;type:varchar(255);not null" json:"sender"`
	SenderDevice string                 `gorm:"type:varchar(255)" json:"sender_device"`
	TargetUser   string                 `gorm:"index;type:varchar(255);not null" json:"target_user"`
	TargetDevice string                 `gorm:"type:varchar(255)" json:"target_device"`
	EventType    string                 `gorm:"type:varchar(64);not null" json:"type"`
	Content      map[string]interface{} `gorm:"type:text;serializer:json" json:"content"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (MatrixToDeviceMessage) TableName() string {
	return "matrix_to_device_messages"
}

// ===== MatrixReceipt 回执 =====

// MatrixReceipt 回执
type MatrixReceipt struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	RoomID      string `gorm:"index;type:varchar(255);not null" json:"room_id"`
	EventID     string `gorm:"index;type:varchar(255);not null" json:"event_id"`
	UserID      string `gorm:"index;type:varchar(255);not null" json:"user_id"`
	ReceiptType string `gorm:"type:varchar(32);not null" json:"receipt_type"` // m.read, m.read.private
	ThreadID    string `gorm:"type:varchar(255)" json:"thread_id,omitempty"` // thread_id for threaded receipts
	TS          int64  `json:"ts"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (MatrixReceipt) TableName() string {
	return "matrix_receipts"
}

// ===== MatrixAccountData 账户数据 =====

// MatrixAccountData 账户数据
type MatrixAccountData struct {
	ID       uint64                 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID   string                 `gorm:"index;type:varchar(255);not null" json:"user_id"`
	RoomID   *string                `gorm:"index;type:varchar(255)" json:"room_id"`
	DataType string                 `gorm:"type:varchar(64);not null" json:"type"`
	Content  map[string]interface{} `gorm:"type:text;serializer:json" json:"content"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (MatrixAccountData) TableName() string {
	return "matrix_account_data"
}

// ===== MatrixTyping 输入状态 =====

// MatrixTyping 输入状态
type MatrixTyping struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RoomID    string    `gorm:"index;type:varchar(255);not null" json:"room_id"`
	UserID    string    `gorm:"index;type:varchar(255);not null" json:"user_id"`
	Timeout   int64     `json:"timeout"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (MatrixTyping) TableName() string {
	return "matrix_typing"
}

// ===== MatrixFilter 过滤器 =====

// MatrixFilter 用户过滤器
type MatrixFilter struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID   string `gorm:"index;type:varchar(255);not null" json:"user_id"`
	FilterID string `gorm:"uniqueIndex;type:varchar(64);not null" json:"filter_id"`
	Filter   string `gorm:"type:text" json:"filter"` // JSON

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (MatrixFilter) TableName() string {
	return "matrix_filters"
}

// ===== MatrixPresence 在线状态 =====

// MatrixPresence 用户在线状态
type MatrixPresence struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       string `gorm:"uniqueIndex;type:varchar(255);not null" json:"user_id"`
	Presence     string `gorm:"type:varchar(32);not null;default:'offline'" json:"presence"` // online, unavailable, offline
	StatusMsg    *string `gorm:"type:varchar(255)" json:"status_msg"`
	LastActiveTS int64  `json:"last_active_ts"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (MatrixPresence) TableName() string {
	return "matrix_presence"
}

// ===== MatrixCrossSigningKey 交叉签名密钥 =====

// MatrixCrossSigningKey 交叉签名密钥存储
type MatrixCrossSigningKey struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID   string `gorm:"index;type:varchar(255);not null" json:"user_id"`
	KeyType  string `gorm:"type:varchar(32);not null" json:"key_type"` // master, self_signing, user_signing
	KeyData  string `gorm:"type:text;not null" json:"key_data"`        // JSON 格式的密钥数据
	IsActive bool   `gorm:"default:true" json:"is_active"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (MatrixCrossSigningKey) TableName() string {
	return "matrix_cross_signing_keys"
}

// ===== MatrixRoomTag 房间标签 =====

// MatrixRoomTag 房间标签
type MatrixRoomTag struct {
	ID      uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID  string `gorm:"index;type:varchar(255);not null" json:"user_id"`
	RoomID  string `gorm:"index;type:varchar(255);not null" json:"room_id"`
	Tag     string `gorm:"type:varchar(128);not null" json:"tag"`
	Content string `gorm:"type:text" json:"content"` // JSON

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (MatrixRoomTag) TableName() string {
	return "matrix_room_tags"
}

// ===== MatrixFederationTxn 联邦事务 =====

// MatrixFederationTxn 联邦事务记录（用于幂等处理）
type MatrixFederationTxn struct {
	ID        uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	TxnID     string          `gorm:"index;type:varchar(255);not null" json:"txn_id"`
	Origin    string          `gorm:"index;type:varchar(255);not null" json:"origin"` // 发送服务器名
	PDUs      json.RawMessage `gorm:"type:json" json:"pdus"`                          // PDU 列表
	EDUs      json.RawMessage `gorm:"type:json" json:"edus"`                          // EDU 列表
	Processed bool            `gorm:"default:false" json:"processed"`
	Error     *string         `gorm:"type:text" json:"error,omitempty"`

	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (MatrixFederationTxn) TableName() string {
	return "matrix_federation_txns"
}
