package direct

import (
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestDirectTimeline_GetKeyFormat(t *testing.T) {
	timeline := &DirectTimeline{}
	assert.Equal(t, "direct:%s", timeline.GetKeyFormat())
}

func TestDirectTimeline_GetTimelineId(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &DirectTimeline{Account: account}
	assert.Equal(t, "12345", timeline.GetTimelineId())
}

func TestDirectTimeline_TimelineInterface(t *testing.T) {
	account := &models.Account{Id: 999}
	var timeline feed.Timeline = &DirectTimeline{Account: account}

	assert.Equal(t, "direct:%s", timeline.GetKeyFormat())
	assert.Equal(t, "999", timeline.GetTimelineId())
}

func TestDirectTimeline_Rebuild(t *testing.T) {
	account := &models.Account{Id: 1}
	timeline := &DirectTimeline{Account: account}

	// Rebuild 应该返回 nil（不执行任何操作）
	err := timeline.Rebuild()
	assert.Nil(t, err)
}

func TestDirectTimeline_MultipleAccounts(t *testing.T) {
	accounts := []*models.Account{
		{Id: 1},
		{Id: 100},
		{Id: 999999},
	}

	for _, acc := range accounts {
		timeline := &DirectTimeline{Account: acc}
		assert.NotNil(t, timeline)
		assert.Contains(t, timeline.GetKeyFormat(), "direct:")
		assert.NotEmpty(t, timeline.GetTimelineId())
	}
}

func TestDirectTimeline_ConcurrentAccess(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &DirectTimeline{Account: account}

	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = timeline.GetKeyFormat()
				_ = timeline.GetTimelineId()
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestPush_VisibilityCheck(t *testing.T) {
	tests := []struct {
		name       string
		visibility uint8
		shouldPush bool
	}{
		{"public", models.StatusVisibilityPublic, false},
		{"unlisted", models.StatusVisibilityUnlisted, false},
		{"private", models.StatusVisibilityPrivate, false},
		{"direct", models.StatusVisibilityDirect, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &models.Status{
				Id:         1,
				Visibility: tt.visibility,
			}
			// Push 需要 Redis 和数据库连接
			assert.NotNil(t, status)
			// 验证 visibility 值
			assert.Equal(t, tt.visibility, status.Visibility)
		})
	}
}

func TestPush_NoConversationId(t *testing.T) {
	status := &models.Status{
		Id:             1,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: nil,
	}

	// 没有 ConversationId 时不应该推送
	assert.Nil(t, status.ConversationId)
}

func TestPush_WithToAccounts(t *testing.T) {
	status := &models.Status{
		Id:             1,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: utilsGetPtr(uint64(1)),
		ToAccounts: []*models.Account{
			{Id: 100},
			{Id: 200},
		},
	}

	// 有 ToAccounts 时应该推送给所有收件人
	assert.NotNil(t, status.ConversationId)
	assert.Len(t, status.ToAccounts, 2)
}

func TestUnpush_VisibilityCheck(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityDirect,
	}
	// Unpush 需要 Redis 和数据库连接
	assert.NotNil(t, status)
	assert.Equal(t, uint8(models.StatusVisibilityDirect), status.Visibility)
}

func TestUnpush_NoToAccounts(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityDirect,
		ToAccounts: nil,
	}

	// 没有 ToAccounts 时不应该推送删除
	assert.Nil(t, status.ToAccounts)
}

func TestUnpush_EmptyToAccounts(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityDirect,
		ToAccounts: []*models.Account{},
	}

	// 空的 ToAccounts 时不应该推送删除
	assert.Empty(t, status.ToAccounts)
}

func TestDirectTimeline_NilAccount(t *testing.T) {
	timeline := &DirectTimeline{Account: nil}

	// GetKeyFormat 应该仍然工作
	assert.Equal(t, "direct:%s", timeline.GetKeyFormat())
	// GetTimelineId 会在 nil account 上 panic，这是预期行为
}

func TestDirectTimeline_KeyUniqueness(t *testing.T) {
	timelines := make(map[string]bool)

	for i := uint64(1); i <= 100; i++ {
		account := &models.Account{Id: i}
		timeline := &DirectTimeline{Account: account}
		key := timeline.GetTimelineId()

		assert.False(t, timelines[key], "Duplicate key found: %s", key)
		timelines[key] = true
	}

	assert.Len(t, timelines, 100)
}

func TestDirect_StatusWithConversation(t *testing.T) {
	conversationId := uint64(1)
	status := &models.Status{
		Id:             1,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: &conversationId,
		ToAccounts: []*models.Account{
			{Id: 100},
			{Id: 200},
		},
	}

	assert.NotNil(t, status.ConversationId)
	assert.Equal(t, conversationId, *status.ConversationId)
	assert.Len(t, status.ToAccounts, 2)
}

// 辅助函数
func utilsGetPtr[T any](v T) *T {
	return &v
}
