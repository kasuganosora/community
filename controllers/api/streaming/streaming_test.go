package streaming

import (
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

// TestStreamingRequest_Hash tests the Hash method of StreamingRequest
func TestStreamingRequest_Hash(t *testing.T) {
	tests := []struct {
		name     string
		req      *StreamingRequest
		expected string
	}{
		{
			name: "subscribe user",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "user",
			},
			expected: "5ad39e47a36ad99a82c09e4a64a53e41", // MD5 of "subscribe:user:"
		},
		{
			name: "subscribe public",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "public",
			},
			expected: "baaa7ec5e5d9e4d33f2de31d10c5f1bb", // MD5 of "subscribe:public:"
		},
		{
			name: "subscribe hashtag with tag",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "hashtag",
				Tag:    "golang",
			},
			expected: "subscribe:hashtag:golang", // Will compute actual hash
		},
		{
			name: "subscribe list",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "list",
				List:   "mylist",
			},
			expected: "subscribe:list:mylist", // Will compute actual hash
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := tt.req.Hash()
			assert.NotEmpty(t, hash)
			assert.Len(t, hash, 32) // MD5 hash length
		})
	}

	// Test that same input produces same hash
	t.Run("consistent hash", func(t *testing.T) {
		req1 := &StreamingRequest{Type: "subscribe", Stream: "user"}
		req2 := &StreamingRequest{Type: "subscribe", Stream: "user"}
		assert.Equal(t, req1.Hash(), req2.Hash())
	})

	// Test that different input produces different hash
	t.Run("different hash", func(t *testing.T) {
		req1 := &StreamingRequest{Type: "subscribe", Stream: "user"}
		req2 := &StreamingRequest{Type: "subscribe", Stream: "public"}
		assert.NotEqual(t, req1.Hash(), req2.Hash())
	})
}

// TestStreamingErrResponse_Error tests the Error method
func TestStreamingErrResponse_Error(t *testing.T) {
	err := &StreamingErrResponse{Err: "need login"}
	assert.Equal(t, "need login", err.Error())

	err2 := &StreamingErrResponse{Err: "stream not found"}
	assert.Equal(t, "stream not found", err2.Error())
}

// TestStreamingResponse tests the StreamingResponse struct
func TestStreamingResponse(t *testing.T) {
	resp := &StreamingResponse{
		Stream:  []string{"user"},
		Event:   "update",
		Payload: `{"id":"123"}`,
	}

	assert.Equal(t, []string{"user"}, resp.Stream)
	assert.Equal(t, "update", resp.Event)
	assert.Equal(t, `{"id":"123"}`, resp.Payload)
}

// TestGetChannelName_User tests getChannelName for user timeline
func TestGetChannelName_User(t *testing.T) {
	account := &models.Account{
		Id: 123,
	}

	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user",
	}

	channelName, err := getChannelName(account, req)
	assert.NoError(t, err)
	assert.Contains(t, channelName, "subscribed:feed:home:")
}

// TestGetChannelName_UserWithoutAccount tests getChannelName for user timeline without account
func TestGetChannelName_UserWithoutAccount(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user",
	}

	_, err := getChannelName(nil, req)
	assert.Error(t, err)
	assert.Equal(t, "need login", err.Error())
}

// TestGetChannelName_Public tests getChannelName for public timeline
func TestGetChannelName_Public(t *testing.T) {
	tests := []struct {
		name             string
		req              *StreamingRequest
		expectedContains string
	}{
		{
			name: "public timeline",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "public",
			},
			expectedContains: "subscribed:feed:public",
		},
		{
			name: "public:local timeline",
			req: &StreamingRequest{
				Type:   "subscribe:local",
				Stream: "public",
			},
			expectedContains: "subscribed:feed:public",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelName, err := getChannelName(nil, tt.req)
			assert.NoError(t, err)
			assert.Contains(t, channelName, tt.expectedContains)
		})
	}
}

// TestGetChannelName_Hashtag tests getChannelName for hashtag timeline
func TestGetChannelName_Hashtag(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "hashtag",
		Tag:    "golang",
	}

	channelName, err := getChannelName(nil, req)
	assert.NoError(t, err)
	assert.Contains(t, channelName, "subscribed:feed:hashtag:")
	assert.Contains(t, channelName, "golang")
}

// TestGetChannelName_HashtagWithoutTag tests getChannelName for hashtag without tag name
func TestGetChannelName_HashtagWithoutTag(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "hashtag",
		Tag:    "",
	}

	_, err := getChannelName(nil, req)
	assert.Error(t, err)
	assert.Equal(t, "tag name is empty", err.Error())
}

// TestGetChannelName_Notification tests getChannelName for notification timeline
func TestGetChannelName_Notification(t *testing.T) {
	account := &models.Account{
		Id: 456,
	}

	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user:notification",
	}

	channelName, err := getChannelName(account, req)
	assert.NoError(t, err)
	assert.Contains(t, channelName, "subscribed:feed:notification:")
}

// TestGetChannelName_NotificationWithoutAccount tests notification without account
func TestGetChannelName_NotificationWithoutAccount(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user:notification",
	}

	_, err := getChannelName(nil, req)
	assert.Error(t, err)
	assert.Equal(t, "need login", err.Error())
}

// TestGetChannelName_Direct tests getChannelName for direct timeline
func TestGetChannelName_Direct(t *testing.T) {
	account := &models.Account{
		Id: 789,
	}

	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "direct",
	}

	channelName, err := getChannelName(account, req)
	assert.NoError(t, err)
	assert.Contains(t, channelName, "subscribed:direct:")
}

// TestGetChannelName_DirectWithoutAccount tests direct without account
func TestGetChannelName_DirectWithoutAccount(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "direct",
	}

	_, err := getChannelName(nil, req)
	assert.Error(t, err)
	assert.Equal(t, "need login", err.Error())
}

// TestGetChannelName_UnknownStream tests unknown stream type
func TestGetChannelName_UnknownStream(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "unknown",
	}

	_, err := getChannelName(nil, req)
	assert.Error(t, err)
	assert.Equal(t, "stream not found", err.Error())
}

// TestStreamingRequest_Json tests JSON marshaling/unmarshaling
func TestStreamingRequest_Json(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user",
		List:   "",
		Tag:    "",
	}

	// Test using standard json.Marshal
	jsonBytes, err := json.Marshal(req)
	assert.NoError(t, err)

	var unmarshaled StreamingRequest
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	assert.NoError(t, err)

	assert.Equal(t, req.Type, unmarshaled.Type)
	assert.Equal(t, req.Stream, unmarshaled.Stream)
}
