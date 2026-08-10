package matrix

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// Event.ToRoomMessageContent
// ====================================================================

func TestEvent_ToRoomMessageContent_NilContent(t *testing.T) {
	e := &Event{Content: nil}
	assert.Nil(t, e.ToRoomMessageContent())
}

func TestEvent_ToRoomMessageContent_EmptyContent(t *testing.T) {
	e := &Event{Content: map[string]any{}}
	c := e.ToRoomMessageContent()
	require.NotNil(t, c)
	assert.Equal(t, "", c.MsgType)
	assert.Equal(t, "", c.Body)
	assert.Nil(t, c.InReplyTo)
}

func TestEvent_ToRoomMessageContent_TextMessage(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"msgtype": "m.text",
			"body":    "Hello world",
		},
	}
	c := e.ToRoomMessageContent()
	require.NotNil(t, c)
	assert.Equal(t, MsgTypeText, c.MsgType)
	assert.Equal(t, "Hello world", c.Body)
	assert.Nil(t, c.InReplyTo)
}

func TestEvent_ToRoomMessageContent_FormattedBody(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"msgtype":        "m.text",
			"body":           "plain text",
			"format":         "org.matrix.custom.html",
			"formatted_body": "<b>bold</b>",
		},
	}
	c := e.ToRoomMessageContent()
	require.NotNil(t, c)
	assert.Equal(t, "org.matrix.custom.html", c.Format)
	assert.Equal(t, "<b>bold</b>", c.FormattedBody)
}

func TestEvent_ToRoomMessageContent_MediaFields(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"msgtype":  "m.image",
			"body":     "image.png",
			"url":      "mxc://example.com/abc",
			"filename": "original_image.png",
		},
	}
	c := e.ToRoomMessageContent()
	require.NotNil(t, c)
	assert.Equal(t, "mxc://example.com/abc", c.URL)
	assert.Equal(t, "original_image.png", c.Filename)
}

func TestEvent_ToRoomMessageContent_WithReply(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"msgtype": "m.text",
			"body":    "reply",
			"m.relates_to": map[string]any{
				"m.in_reply_to": map[string]any{
					"event_id": "$orig:example.com",
				},
			},
		},
	}
	c := e.ToRoomMessageContent()
	require.NotNil(t, c)
	require.NotNil(t, c.InReplyTo)
	assert.Equal(t, "$orig:example.com", c.InReplyTo.InReplyTo.EventID)
}

func TestEvent_ToRoomMessageContent_ReplyMissingEventID(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"msgtype": "m.text",
			"body":    "reply",
			"m.relates_to": map[string]any{
				"m.in_reply_to": map[string]any{},
			},
		},
	}
	c := e.ToRoomMessageContent()
	require.NotNil(t, c)
	require.NotNil(t, c.InReplyTo)
	assert.Equal(t, "", c.InReplyTo.InReplyTo.EventID)
}

func TestEvent_ToRoomMessageContent_ReplyMissingInReplyTo(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"msgtype":      "m.text",
			"body":         "reply",
			"m.relates_to": map[string]any{},
		},
	}
	c := e.ToRoomMessageContent()
	require.NotNil(t, c)
	assert.Nil(t, c.InReplyTo)
}

// ====================================================================
// Event.ToRoomMemberContent
// ====================================================================

func TestEvent_ToRoomMemberContent_NilContent(t *testing.T) {
	e := &Event{Content: nil}
	assert.Nil(t, e.ToRoomMemberContent())
}

func TestEvent_ToRoomMemberContent_EmptyContent(t *testing.T) {
	e := &Event{Content: map[string]any{}}
	c := e.ToRoomMemberContent()
	require.NotNil(t, c)
	assert.Equal(t, "", c.Membership)
}

func TestEvent_ToRoomMemberContent_Join(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"membership":  "join",
			"displayname": "Alice",
			"avatar_url":  "mxc://example.com/alice",
			"reason":      "joining",
		},
	}
	c := e.ToRoomMemberContent()
	require.NotNil(t, c)
	assert.Equal(t, MembershipJoin, c.Membership)
	assert.Equal(t, "Alice", c.DisplayName)
	assert.Equal(t, "mxc://example.com/alice", c.AvatarURL)
	assert.Equal(t, "joining", c.Reason)
}

func TestEvent_ToRoomMemberContent_Leave(t *testing.T) {
	e := &Event{
		Content: map[string]any{
			"membership": "leave",
			"reason":     "leaving the room",
		},
	}
	c := e.ToRoomMemberContent()
	require.NotNil(t, c)
	assert.Equal(t, MembershipLeave, c.Membership)
	assert.Equal(t, "leaving the room", c.Reason)
}

// ====================================================================
// Event.GetTimestamp
// ====================================================================

func TestEvent_GetTimestamp_Set(t *testing.T) {
	// 2023-01-01 00:00:00 UTC in millis = 1672531200000
	e := &Event{Timestamp: 1672531200000}
	ts := e.GetTimestamp()
	assert.Equal(t, int64(1672531200000), ts.UnixMilli())
}

func TestEvent_GetTimestamp_Zero(t *testing.T) {
	e := &Event{Timestamp: 0}
	before := time.Now()
	ts := e.GetTimestamp()
	after := time.Now()
	// Timestamp not set, returns current time — must be within the test window.
	assert.False(t, ts.Before(before.Add(-1*time.Second)))
	assert.False(t, ts.After(after.Add(1*time.Second)))
}

// ====================================================================
// Constants sanity
// ====================================================================

func TestEventTypeConstants(t *testing.T) {
	assert.Equal(t, "m.room.message", EventTypeRoomMessage)
	assert.Equal(t, "m.room.member", EventTypeRoomMember)
	assert.Equal(t, "m.room.create", EventTypeRoomCreate)
	assert.Equal(t, "m.room.name", EventTypeRoomName)
	assert.Equal(t, "m.room.topic", EventTypeRoomTopic)
	assert.Equal(t, "m.room.avatar", EventTypeRoomAvatar)
	assert.Equal(t, "m.room.encrypted", EventTypeRoomEncrypted)
}

func TestMembershipStateConstants(t *testing.T) {
	assert.Equal(t, "join", MembershipJoin)
	assert.Equal(t, "invite", MembershipInvite)
	assert.Equal(t, "leave", MembershipLeave)
	assert.Equal(t, "ban", MembershipBan)
	assert.Equal(t, "knock", MembershipKnock)
}

func TestMessageTypeConstants(t *testing.T) {
	assert.Equal(t, "m.text", MsgTypeText)
	assert.Equal(t, "m.emote", MsgTypeEmote)
	assert.Equal(t, "m.notice", MsgTypeNotice)
	assert.Equal(t, "m.image", MsgTypeImage)
	assert.Equal(t, "m.file", MsgTypeFile)
	assert.Equal(t, "m.audio", MsgTypeAudio)
	assert.Equal(t, "m.video", MsgTypeVideo)
	assert.Equal(t, "m.location", MsgTypeLocation)
}

// ====================================================================
// ptrTo helper
// ====================================================================

func TestPtrTo(t *testing.T) {
	s := "hello"
	p := ptrTo(s)
	require.NotNil(t, p)
	assert.Equal(t, "hello", *p)

	p2 := ptrTo("")
	require.NotNil(t, p2)
	assert.Equal(t, "", *p2)
}

// ====================================================================
// generateTxnID
// ====================================================================

func TestGenerateTxnID(t *testing.T) {
	id1 := generateTxnID()
	id2 := generateTxnID()
	assert.Contains(t, id1, "community_")
	assert.Contains(t, id2, "community_")
	// UUIDs should make them unique.
	assert.NotEqual(t, id1, id2)
}
