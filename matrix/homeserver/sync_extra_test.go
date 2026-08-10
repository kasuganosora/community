package homeserver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== matchEventType tests =====

func TestMatchEventType_Wildcard(t *testing.T) {
	assert.True(t, matchEventType("m.room.message", "*"))
	assert.True(t, matchEventType("anything", "*"))
	assert.True(t, matchEventType("", "*"))
}

func TestMatchEventType_ExactMatch(t *testing.T) {
	assert.True(t, matchEventType("m.room.message", "m.room.message"))
	assert.False(t, matchEventType("m.room.message", "m.room.member"))
	assert.False(t, matchEventType("m.room.message", "m.room"))
}

func TestMatchEventType_EmptyPattern(t *testing.T) {
	assert.False(t, matchEventType("m.room.message", ""))
	assert.True(t, matchEventType("", ""))
}

// ===== filterEvents tests =====

func TestFilterEvents_NilFilter(t *testing.T) {
	events := []*ClientEvent{
		{Type: "m.room.message"},
		{Type: "m.room.member"},
	}
	result := filterEvents(events, nil)
	assert.Len(t, result, 2, "nil filter should return all events")
}

func TestFilterEvents_TypesFilter(t *testing.T) {
	events := []*ClientEvent{
		{Type: "m.room.message"},
		{Type: "m.room.member"},
		{Type: "m.room.message"},
	}
	ef := &parsedEventFilter{Types: []string{"m.room.message"}}
	result := filterEvents(events, ef)
	assert.Len(t, result, 2)
	for _, e := range result {
		assert.Equal(t, "m.room.message", e.Type)
	}
}

func TestFilterEvents_TypesFilterWithWildcard(t *testing.T) {
	events := []*ClientEvent{
		{Type: "m.room.message"},
		{Type: "m.room.member"},
	}
	ef := &parsedEventFilter{Types: []string{"*"}}
	result := filterEvents(events, ef)
	assert.Len(t, result, 2)
}

func TestFilterEvents_NotTypesFilter(t *testing.T) {
	events := []*ClientEvent{
		{Type: "m.room.message"},
		{Type: "m.room.member"},
		{Type: "m.room.message"},
	}
	ef := &parsedEventFilter{NotTypes: []string{"m.room.message"}}
	result := filterEvents(events, ef)
	assert.Len(t, result, 1)
	assert.Equal(t, "m.room.member", result[0].Type)
}

func TestFilterEvents_TypesAndNotTypesCombined(t *testing.T) {
	events := []*ClientEvent{
		{Type: "m.room.message"},
		{Type: "m.room.member"},
		{Type: "m.reaction"},
	}
	ef := &parsedEventFilter{
		Types:    []string{"*"},          // match all
		NotTypes: []string{"m.reaction"}, // but exclude reactions
	}
	result := filterEvents(events, ef)
	assert.Len(t, result, 2)
	for _, e := range result {
		assert.NotEqual(t, "m.reaction", e.Type)
	}
}

func TestFilterEvents_EmptyEvents(t *testing.T) {
	ef := &parsedEventFilter{Types: []string{"m.room.message"}}
	result := filterEvents(nil, ef)
	assert.Empty(t, result)
}

func TestFilterEvents_NoMatch(t *testing.T) {
	events := []*ClientEvent{{Type: "m.room.message"}}
	ef := &parsedEventFilter{Types: []string{"m.room.member"}}
	result := filterEvents(events, ef)
	assert.Empty(t, result)
}

// ===== markTypingChanged / getLastTypingChange tests =====

func TestMarkTypingChanged_AndgetLastTypingChange(t *testing.T) {
	roomID := "!typingtest:server.local"
	// Initially zero (or whatever was set by prior tests).
	before := getLastTypingChange(roomID)

	markTypingChanged(roomID)
	after := getLastTypingChange(roomID)
	assert.True(t, after >= before, "typing change timestamp should not decrease")
	assert.True(t, after > 0, "typing change timestamp should be set")
}

func TestGetLastTypingChange_UnknownRoom(t *testing.T) {
	// An unknown room returns 0 (zero value of int64).
	ts := getLastTypingChange("!never-typed:server.local")
	// Note: this could be non-zero if a prior test set it, but for a unique
	// room it should be 0.
	assert.Equal(t, int64(0), ts)
}

// ===== extractRelatesToMap tests =====

func TestExtractRelatesToMap_NilContent(t *testing.T) {
	result := extractRelatesToMap(nil)
	assert.Nil(t, result)
}

func TestExtractRelatesToMap_NoRelatesTo(t *testing.T) {
	content := map[string]any{"body": "hello"}
	result := extractRelatesToMap(content)
	assert.Nil(t, result)
}

func TestExtractRelatesToMap_MapValue(t *testing.T) {
	content := map[string]any{
		"m.relates_to": map[string]any{"rel_type": "m.thread", "event_id": "$root:server"},
	}
	result := extractRelatesToMap(content)
	require.NotNil(t, result)
	assert.Equal(t, "m.thread", result["rel_type"])
	assert.Equal(t, "$root:server", result["event_id"])
}

func TestExtractRelatesToMap_RawMessageValue(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"rel_type": "m.annotation", "event_id": "$target:server"})
	content := map[string]any{
		"m.relates_to": json.RawMessage(raw),
	}
	result := extractRelatesToMap(content)
	require.NotNil(t, result)
	assert.Equal(t, "m.annotation", result["rel_type"])
}

func TestExtractRelatesToMap_StringValue(t *testing.T) {
	content := map[string]any{
		"m.relates_to": `{"rel_type":"m.replace"}`,
	}
	result := extractRelatesToMap(content)
	require.NotNil(t, result)
	assert.Equal(t, "m.replace", result["rel_type"])
}

func TestExtractRelatesToMap_ByteSliceValue(t *testing.T) {
	content := map[string]any{
		"m.relates_to": []byte(`{"rel_type":"m.thread"}`),
	}
	result := extractRelatesToMap(content)
	require.NotNil(t, result)
	assert.Equal(t, "m.thread", result["rel_type"])
}

func TestExtractRelatesToMap_InvalidString(t *testing.T) {
	content := map[string]any{
		"m.relates_to": "not-json",
	}
	result := extractRelatesToMap(content)
	assert.Nil(t, result)
}

// ===== isThreadEvent tests =====

func TestIsThreadEvent_ThreadReply(t *testing.T) {
	evt := &MatrixEvent{
		Content: map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.thread", "event_id": "$root:server"},
		},
	}
	isThread, threadID := isThreadEvent(evt)
	assert.True(t, isThread)
	assert.Equal(t, "$root:server", threadID)
}

func TestIsThreadEvent_NotThread(t *testing.T) {
	evt := &MatrixEvent{
		Content: map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": "$target:server"},
		},
	}
	isThread, _ := isThreadEvent(evt)
	assert.False(t, isThread)
}

func TestIsThreadEvent_NoRelatesTo(t *testing.T) {
	evt := &MatrixEvent{
		Content: map[string]any{"body": "hello"},
	}
	isThread, _ := isThreadEvent(evt)
	assert.False(t, isThread)
}

func TestIsThreadEvent_EmptyEventID(t *testing.T) {
	evt := &MatrixEvent{
		Content: map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.thread"},
		},
	}
	isThread, threadID := isThreadEvent(evt)
	assert.False(t, isThread, "thread with empty event_id should not be a thread event")
	assert.Equal(t, "", threadID)
}

// ===== isAnnotation tests =====

func TestIsAnnotation_True(t *testing.T) {
	evt := &MatrixEvent{
		Content: map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": "$target:server"},
		},
	}
	assert.True(t, isAnnotation(evt))
}

func TestIsAnnotation_FalseNotAnnotation(t *testing.T) {
	evt := &MatrixEvent{
		Content: map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.thread"},
		},
	}
	assert.False(t, isAnnotation(evt))
}

func TestIsAnnotation_NoRelatesTo(t *testing.T) {
	evt := &MatrixEvent{
		Content: map[string]any{"body": "hello"},
	}
	assert.False(t, isAnnotation(evt))
}

// ===== shouldNotify tests =====

func TestShouldNotify_MessageEvent(t *testing.T) {
	evt := &MatrixEvent{
		EventType: EventTypeRoomMessage,
		Content:   map[string]any{"body": "hi"},
	}
	assert.True(t, shouldNotify(evt))
}

func TestShouldNotify_EncryptedEvent(t *testing.T) {
	evt := &MatrixEvent{
		EventType: "m.room.encrypted",
		Content:   map[string]any{"ciphertext": "abc"},
	}
	assert.True(t, shouldNotify(evt))
}

func TestShouldNotify_NonMessageEvent(t *testing.T) {
	evt := &MatrixEvent{
		EventType: "m.room.member",
		Content:   map[string]any{"membership": "join"},
	}
	assert.False(t, shouldNotify(evt))
}

func TestShouldNotify_AnnotationExcluded(t *testing.T) {
	evt := &MatrixEvent{
		EventType: EventTypeRoomMessage,
		Content: map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.annotation"},
		},
	}
	assert.False(t, shouldNotify(evt), "annotations should not notify even if message type")
}
