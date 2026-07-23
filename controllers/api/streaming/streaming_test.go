package streaming

import (
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamingRequest_Hash tests the Hash method of StreamingRequest.
func TestStreamingRequest_Hash(t *testing.T) {
	tests := []struct {
		name string
		req  *StreamingRequest
	}{
		{
			name: "subscribe user",
			req: &StreamingRequest{
				Type:   "subscribe", //nolint:goconst
				Stream: "user",      //nolint:goconst
			},
		},
		{
			name: "subscribe public",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "public", //nolint:goconst
			},
		},
		{
			name: "subscribe hashtag with tag",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "hashtag", //nolint:goconst
				Tag:    "golang",  //nolint:goconst
			},
		},
		{
			name: "subscribe list",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "list",   //nolint:goconst
				List:   "mylist", //nolint:goconst
			},
		},
		{
			name: "unsubscribe user",
			req: &StreamingRequest{
				Type:   "unsubscribe", //nolint:goconst
				Stream: "user",
			},
		},
		{
			name: "empty request",
			req:  &StreamingRequest{},
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

	t.Run("different type produces different hash", func(t *testing.T) {
		req1 := &StreamingRequest{Type: "subscribe", Stream: "user"}
		req2 := &StreamingRequest{Type: "unsubscribe", Stream: "user"}
		assert.NotEqual(t, req1.Hash(), req2.Hash())
	})
}

// TestStreamingErrorResponse_Error tests the Error method.
func TestStreamingErrorResponse_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *StreamingErrorResponse
		expected string
	}{
		{"need login", &StreamingErrorResponse{Err: "need login"}, "need login"},                   //nolint:goconst
		{"stream not found", &StreamingErrorResponse{Err: "stream not found"}, "stream not found"}, //nolint:goconst
		{"empty error", &StreamingErrorResponse{Err: ""}, ""},
		{"list not found", &StreamingErrorResponse{Err: "list not found"}, "list not found"},          //nolint:goconst
		{"tag name is empty", &StreamingErrorResponse{Err: "tag name is empty"}, "tag name is empty"}, //nolint:goconst
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

// TestStreamingResponse tests the StreamingResponse struct.
func TestStreamingResponse(t *testing.T) {
	t.Run("basic response", func(t *testing.T) {
		resp := &StreamingResponse{
			Stream:  []string{"user"},
			Event:   "update", //nolint:goconst
			Payload: `{"id":"123"}`,
		}

		assert.Equal(t, []string{"user"}, resp.Stream)
		assert.Equal(t, "update", resp.Event)
		assert.JSONEq(t, `{"id":"123"}`, resp.Payload)
	})

	t.Run("multiple streams", func(t *testing.T) {
		resp := &StreamingResponse{
			Stream:  []string{"hashtag", "golang"},
			Event:   "delete", //nolint:goconst
			Payload: "456",
		}

		assert.Len(t, resp.Stream, 2)
		assert.Equal(t, "hashtag", resp.Stream[0])
		assert.Equal(t, "golang", resp.Stream[1])
		assert.Equal(t, "delete", resp.Event)
	})

	t.Run("event types", func(t *testing.T) {
		eventTypes := []string{"update", "delete", "notification", "filters_changed", "conversation", "status.update"} //nolint:goconst
		for _, eventType := range eventTypes {
			resp := &StreamingResponse{Event: eventType}
			assert.Equal(t, eventType, resp.Event)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		resp := &StreamingResponse{}
		assert.Nil(t, resp.Stream)
		assert.Empty(t, resp.Event)
		assert.Empty(t, resp.Payload)
	})
}

// TestStreamingRequest_Json tests JSON marshaling/unmarshaling.
func TestStreamingRequest_Json(t *testing.T) {
	tests := []struct {
		name string
		req  *StreamingRequest
	}{
		{
			name: "subscribe user",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "user",
				List:   "",
				Tag:    "",
			},
		},
		{
			name: "subscribe public",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "public",
			},
		},
		{
			name: "subscribe hashtag",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "hashtag",
				Tag:    "golang",
			},
		},
		{
			name: "subscribe list",
			req: &StreamingRequest{
				Type:   "subscribe",
				Stream: "list",
				List:   "mylist",
			},
		},
		{
			name: "unsubscribe user",
			req: &StreamingRequest{
				Type:   "unsubscribe",
				Stream: "user",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBytes, err := json.Marshal(tt.req)
			require.NoError(t, err)

			var unmarshaled StreamingRequest

			err = json.Unmarshal(jsonBytes, &unmarshaled)
			require.NoError(t, err)

			assert.Equal(t, tt.req.Type, unmarshaled.Type)
			assert.Equal(t, tt.req.Stream, unmarshaled.Stream)
			assert.Equal(t, tt.req.List, unmarshaled.List)
			assert.Equal(t, tt.req.Tag, unmarshaled.Tag)
		})
	}
}

// TestGetChannelName_User tests getChannelName for user timeline.
func TestGetChannelName_User(t *testing.T) {
	account := &models.Account{Id: 123}

	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user",
	}

	channelName, err := getChannelName(account, req)
	require.NoError(t, err)
	assert.Contains(t, channelName, "subscribed:feed:home:")
	assert.Contains(t, channelName, "123")
}

// TestGetChannelName_UserWithoutAccount tests getChannelName for user timeline without account.
func TestGetChannelName_UserWithoutAccount(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user",
	}

	_, err := getChannelName(nil, req)
	require.Error(t, err)
	assert.Equal(t, "need login", err.Error())
}

// TestGetChannelName_Public tests getChannelName for public timeline.
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
			expectedContains: "subscribed:feed:public", //nolint:goconst
		},
		{
			name: "public:local timeline",
			req: &StreamingRequest{
				Type:   "subscribe:local",
				Stream: "public",
			},
			expectedContains: "subscribed:feed:public",
		},
		{
			name: "public:remote timeline",
			req: &StreamingRequest{
				Type:   "subscribe:remote",
				Stream: "public",
			},
			expectedContains: "subscribed:feed:public",
		},
		{
			name: "public:media timeline",
			req: &StreamingRequest{
				Type:   "subscribe:media",
				Stream: "public",
			},
			expectedContains: "subscribed:feed:public",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelName, err := getChannelName(nil, tt.req)
			require.NoError(t, err)
			assert.Contains(t, channelName, tt.expectedContains)
		})
	}
}

// TestGetChannelName_Hashtag tests getChannelName for hashtag timeline.
func TestGetChannelName_Hashtag(t *testing.T) {
	tests := []struct {
		name string
		req  *StreamingRequest
		tag  string
	}{
		{"golang tag", &StreamingRequest{Type: "subscribe", Stream: "hashtag", Tag: "golang"}, "golang"},
		{"test tag", &StreamingRequest{Type: "subscribe", Stream: "hashtag", Tag: "test"}, "test"},
		{"unicode tag", &StreamingRequest{Type: "subscribe", Stream: "hashtag", Tag: "日本語"}, "日本語"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelName, err := getChannelName(nil, tt.req)
			require.NoError(t, err)
			assert.Contains(t, channelName, "subscribed:feed:hashtag:")
			assert.Contains(t, channelName, tt.tag)
		})
	}
}

// TestGetChannelName_HashtagWithoutTag tests getChannelName for hashtag without tag name.
func TestGetChannelName_HashtagWithoutTag(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "hashtag",
		Tag:    "",
	}

	_, err := getChannelName(nil, req)
	require.Error(t, err)
	assert.Equal(t, "tag name is empty", err.Error())
}

// TestGetChannelName_Notification tests getChannelName for notification timeline.
func TestGetChannelName_Notification(t *testing.T) {
	account := &models.Account{Id: 456}

	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user:notification", //nolint:goconst
	}

	channelName, err := getChannelName(account, req)
	require.NoError(t, err)
	assert.Contains(t, channelName, "subscribed:feed:notification:")
	assert.Contains(t, channelName, "456")
}

// TestGetChannelName_NotificationWithoutAccount tests notification without account.
func TestGetChannelName_NotificationWithoutAccount(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user:notification",
	}

	_, err := getChannelName(nil, req)
	require.Error(t, err)
	assert.Equal(t, "need login", err.Error())
}

// TestGetChannelName_Direct tests getChannelName for direct timeline.
func TestGetChannelName_Direct(t *testing.T) {
	account := &models.Account{Id: 789}

	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "direct", //nolint:goconst
	}

	channelName, err := getChannelName(account, req)
	require.NoError(t, err)
	assert.Contains(t, channelName, "subscribed:direct:")
	assert.Contains(t, channelName, "789")
}

// TestGetChannelName_DirectWithoutAccount tests direct without account.
func TestGetChannelName_DirectWithoutAccount(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "direct",
	}

	_, err := getChannelName(nil, req)
	require.Error(t, err)
	assert.Equal(t, "need login", err.Error())
}

// TestGetChannelName_UnknownStream tests unknown stream type.
func TestGetChannelName_UnknownStream(t *testing.T) {
	unknownStreams := []string{"unknown", "invalid", "random", ""}
	for _, stream := range unknownStreams {
		t.Run("stream: "+stream, func(t *testing.T) {
			req := &StreamingRequest{
				Type:   "subscribe",
				Stream: stream,
			}

			_, err := getChannelName(nil, req)
			require.Error(t, err)
			assert.Equal(t, "stream not found", err.Error())
		})
	}
}

// TestStreamingRequest_AllFields tests all fields of StreamingRequest.
func TestStreamingRequest_AllFields(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "hashtag",
		List:   "mylist",
		Tag:    "golang",
	}

	assert.Equal(t, "subscribe", req.Type)
	assert.Equal(t, "hashtag", req.Stream)
	assert.Equal(t, "mylist", req.List)
	assert.Equal(t, "golang", req.Tag)
}

// TestStreamingResponse_AllFields tests all fields of StreamingResponse.
func TestStreamingResponse_AllFields(t *testing.T) {
	resp := &StreamingResponse{
		Stream:  []string{"user", "notification"},
		Event:   "notification",
		Payload: `{"type":"mention"}`,
	}

	assert.Len(t, resp.Stream, 2)
	assert.Equal(t, "notification", resp.Event)
	assert.Contains(t, resp.Payload, "mention")
}

// TestGetChannelName_List tests getChannelName for list timeline.
func TestGetChannelName_List(t *testing.T) {
	// Note: list requires database lookup, so we test the error case
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "list",
		List:   "",
	}

	_, err := getChannelName(nil, req)
	require.Error(t, err)
	assert.Equal(t, "list name is empty", err.Error())
}

// TestStreamingRequest_ConcurrentAccess tests concurrent access to request.
func TestStreamingRequest_ConcurrentAccess(t *testing.T) {
	req := &StreamingRequest{
		Type:   "subscribe",
		Stream: "user",
	}

	done := make(chan bool)

	for range 10 {
		go func() {
			for range 100 {
				_ = req.Hash()
			}

			done <- true
		}()
	}

	for range 10 {
		<-done
	}
}

// TestGetChannelName_DifferentAccountIds tests with different account IDs.
func TestGetChannelName_DifferentAccountIds(t *testing.T) {
	accountIds := []uint64{1, 100, 999999, 18446744073709551615}

	for _, id := range accountIds {
		t.Run("", func(t *testing.T) {
			account := &models.Account{Id: id}
			req := &StreamingRequest{
				Type:   "subscribe",
				Stream: "user",
			}

			channelName, err := getChannelName(account, req)
			require.NoError(t, err)
			assert.Contains(t, channelName, "subscribed:feed:home:")
		})
	}
}

// TestStreamingResponse_JsonSerialization tests JSON serialization.
func TestStreamingResponse_JsonSerialization(t *testing.T) {
	resp := &StreamingResponse{
		Stream:  []string{"user"},
		Event:   "update",
		Payload: `{"id":"123"}`,
	}

	jsonBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	var unmarshaled StreamingResponse

	err = json.Unmarshal(jsonBytes, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, resp.Stream, unmarshaled.Stream)
	assert.Equal(t, resp.Event, unmarshaled.Event)
	assert.Equal(t, resp.Payload, unmarshaled.Payload)
}

// TestStreamingErrorResponse_ImplementsError tests that StreamingErrorResponse implements error interface.
func TestStreamingErrorResponse_ImplementsError(t *testing.T) {
	var err error = &StreamingErrorResponse{Err: "test error"}

	assert.Equal(t, "test error", err.Error())
}
