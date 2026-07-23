package matrix

import (
	"time"
)

// EventType Matrix 事件类型常量.
const (
	EventTypeRoomMessage   = "m.room.message"
	EventTypeRoomMember    = "m.room.member"
	EventTypeRoomCreate    = "m.room.create"
	EventTypeRoomName      = "m.room.name"
	EventTypeRoomTopic     = "m.room.topic"
	EventTypeRoomAvatar    = "m.room.avatar"
	EventTypeRoomEncrypted = "m.room.encrypted"
)

// MembershipState Matrix 房间成员状态.
const (
	MembershipJoin   = "join"
	MembershipInvite = "invite"
	MembershipLeave  = "leave"
	MembershipBan    = "ban"
	MembershipKnock  = "knock"
)

// MessageType Matrix 消息类型常量.
const (
	MsgTypeText     = "m.text"
	MsgTypeEmote    = "m.emote"
	MsgTypeNotice   = "m.notice"
	MsgTypeImage    = "m.image"
	MsgTypeFile     = "m.file"
	MsgTypeAudio    = "m.audio"
	MsgTypeVideo    = "m.video"
	MsgTypeLocation = "m.location"
)

// Event Matrix 事件结构.
type Event struct {
	Type        string         `json:"type"`
	RoomID      string         `json:"room_id,omitempty"`
	Sender      string         `json:"sender,omitempty"`
	EventID     string         `json:"event_id,omitempty"`
	Content     map[string]any `json:"content"`
	StateKey    *string        `json:"state_key,omitempty"`
	Timestamp   int64          `json:"origin_server_ts,omitempty"`
	PrevContent map[string]any `json:"prev_content,omitempty"`
	Redacts     string         `json:"redacts,omitempty"`
	Unsigned    map[string]any `json:"unsigned,omitempty"`
}

// RoomMessageContent m.room.message 事件内容.
type RoomMessageContent struct {
	MsgType       string `json:"msgtype"`
	Body          string `json:"body"`
	Format        string `json:"format,omitempty"`
	FormattedBody string `json:"formatted_body,omitempty"`
	URL           string `json:"url,omitempty"`          // mxc:// URL for media
	Filename      string `json:"filename,omitempty"`     // Original filename
	InReplyTo     *Reply `json:"m.relates_to,omitempty"` // Reply relation
}

// Reply 回复关系.
type Reply struct {
	InReplyTo struct {
		EventID string `json:"event_id"`
	} `json:"m.in_reply_to"`
}

// RoomMemberContent m.room.member 事件内容.
type RoomMemberContent struct {
	Membership  string `json:"membership"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	DisplayName string `json:"displayname,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// RoomCreateContent m.room.create 事件内容.
type RoomCreateContent struct {
	Creator  string `json:"creator"`
	Preset   string `json:"preset,omitempty"` // private_chat, public_chat, trusted_private_chat
	IsDirect bool   `json:"is_direct,omitempty"`
}

// Transaction AS Transaction 请求体.
type Transaction struct {
	Events []Event `json:"events"`
}

// RoomInfo Matrix 房间信息.
type RoomInfo struct {
	RoomID      string            `json:"room_id"`
	Name        string            `json:"name,omitempty"`
	Topic       string            `json:"topic,omitempty"`
	AvatarURL   string            `json:"avatar_url,omitempty"`
	IsDirect    bool              `json:"is_direct,omitempty"`
	Members     map[string]string `json:"members,omitempty"` // user_id -> membership
	CreateEvent *Event            `json:"create_event,omitempty"`
}

// UserProfile Matrix 用户档案.
type UserProfile struct {
	DisplayName string `json:"displayname,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// SyncResponse Client-Server API /sync 响应.
type SyncResponse struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join   map[string]JoinedRoom  `json:"join,omitempty"`
		Invite map[string]InvitedRoom `json:"invite,omitempty"`
		Leave  map[string]LeftRoom    `json:"leave,omitempty"`
	} `json:"rooms,omitempty"` //nolint:modernize // intentional for API compatibility
	Presence struct {
		Events []Event `json:"events,omitempty"`
	} `json:"presence,omitempty"` //nolint:modernize // intentional for API compatibility
}

// JoinedRoom 已加入的房间.
type JoinedRoom struct {
	State struct {
		Events []Event `json:"events,omitempty"`
	} `json:"state"`
	Timeline struct {
		Events    []Event `json:"events,omitempty"`
		Limited   bool    `json:"limited,omitempty"`
		PrevBatch string  `json:"prev_batch,omitempty"`
	} `json:"timeline"`
	Ephemeral struct {
		Events []Event `json:"events,omitempty"`
	} `json:"ephemeral"`
}

// InvitedRoom 被邀请的房间.
type InvitedRoom struct {
	InviteState struct {
		Events []Event `json:"events,omitempty"`
	} `json:"invite_state"`
}

// LeftRoom 已离开的房间.
type LeftRoom struct {
	State struct {
		Events []Event `json:"events,omitempty"`
	} `json:"state"`
	Timeline struct {
		Events    []Event `json:"events,omitempty"`
		Limited   bool    `json:"limited,omitempty"`
		PrevBatch string  `json:"prev_batch,omitempty"`
	} `json:"timeline"`
}

// ErrorResponse Matrix 错误响应.
type ErrorResponse struct {
	ErrCode string `json:"errcode"`
	Error   string `json:"error"`
}

// RoomCreateRequest 创建房间请求.
type RoomCreateRequest struct {
	Preset          string         `json:"preset,omitempty"`
	Name            string         `json:"name,omitempty"`
	Topic           string         `json:"topic,omitempty"`
	IsDirect        bool           `json:"is_direct,omitempty"`
	Invite          []string       `json:"invite,omitempty"`
	InitialState    []Event        `json:"initial_state,omitempty"`
	CreationContent map[string]any `json:"creation_content,omitempty"`
}

// RoomCreateResponse 创建房间响应.
type RoomCreateResponse struct {
	RoomID string `json:"room_id"`
}

// SendEventResponse 发送事件响应.
type SendEventResponse struct {
	EventID string `json:"event_id"`
}

// ToRoomMessageContent 将 Event.Content 转换为 RoomMessageContent.
func (e *Event) ToRoomMessageContent() *RoomMessageContent {
	if e.Content == nil {
		return nil
	}

	content := &RoomMessageContent{}

	if v, ok := e.Content["msgtype"].(string); ok {
		content.MsgType = v
	}

	if v, ok := e.Content["body"].(string); ok {
		content.Body = v
	}

	if v, ok := e.Content["format"].(string); ok {
		content.Format = v
	}

	if v, ok := e.Content["formatted_body"].(string); ok {
		content.FormattedBody = v
	}

	if v, ok := e.Content["url"].(string); ok {
		content.URL = v
	}

	if v, ok := e.Content["filename"].(string); ok {
		content.Filename = v
	}

	// 解析回复关系
	if relatesTo, ok := e.Content["m.relates_to"].(map[string]any); ok {
		if inReplyTo, ok := relatesTo["m.in_reply_to"].(map[string]any); ok {
			content.InReplyTo = &Reply{}
			if eventID, ok := inReplyTo["event_id"].(string); ok {
				content.InReplyTo.InReplyTo.EventID = eventID
			}
		}
	}

	return content
}

// ToRoomMemberContent 将 Event.Content 转换为 RoomMemberContent.
func (e *Event) ToRoomMemberContent() *RoomMemberContent {
	if e.Content == nil {
		return nil
	}

	content := &RoomMemberContent{}

	if v, ok := e.Content["membership"].(string); ok {
		content.Membership = v
	}

	if v, ok := e.Content["avatar_url"].(string); ok {
		content.AvatarURL = v
	}

	if v, ok := e.Content["displayname"].(string); ok {
		content.DisplayName = v
	}

	if v, ok := e.Content["reason"].(string); ok {
		content.Reason = v
	}

	return content
}

// GetTimestamp 获取事件时间戳，如果未设置则返回当前时间.
func (e *Event) GetTimestamp() time.Time {
	if e.Timestamp > 0 {
		return time.UnixMilli(e.Timestamp)
	}

	return time.Now()
}
