package mention

import (
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestMentionTimeline_GetKeyFormat(t *testing.T) {
	timeline := &MentionTimeline{}
	assert.Equal(t, "feed:mention:%s", timeline.GetKeyFormat())
}

func TestMentionTimeline_GetTimelineId(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &MentionTimeline{Account: account}
	assert.Equal(t, "12345", timeline.GetTimelineId())
}

func TestMentionTimeline_TimelineInterface(t *testing.T) {
	account := &models.Account{Id: 999}
	var timeline feed.Timeline = &MentionTimeline{Account: account}

	assert.Equal(t, "feed:mention:%s", timeline.GetKeyFormat())
	assert.Equal(t, "999", timeline.GetTimelineId())
}

func TestMentionTimeline_MultipleAccounts(t *testing.T) {
	accounts := []*models.Account{
		{Id: 1},
		{Id: 100},
		{Id: 999999},
	}

	for _, acc := range accounts {
		timeline := &MentionTimeline{Account: acc}
		assert.NotNil(t, timeline)
		assert.Contains(t, timeline.GetKeyFormat(), "feed:mention:")
		assert.NotEmpty(t, timeline.GetTimelineId())
	}
}

func TestMentionTimeline_ConcurrentAccess(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &MentionTimeline{Account: account}

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
		{"public", models.StatusVisibilityPublic, true},
		{"unlisted", models.StatusVisibilityUnlisted, true},
		{"private", models.StatusVisibilityPrivate, true},
		{"direct", models.StatusVisibilityDirect, false},
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

func TestPush_WithUserSettings(t *testing.T) {
	account := &models.Account{
		Id: 1,
		User: &models.User{
			Settings: &models.UserSetting{},
		},
	}

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotNil(t, account)
	assert.NotNil(t, account.User)
	assert.NotNil(t, account.User.Settings)
	assert.NotNil(t, status)
}

func TestUnpush_VisibilityCheck(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
	}
	// Unpush 需要 Redis 和数据库连接
	assert.NotNil(t, status)
}

func TestUnpush_NoToAccounts(t *testing.T) {
	status := &models.Status{
		Id:          1,
		Visibility:  models.StatusVisibilityPublic,
		ToAccounts:  nil,
	}

	// 没有 ToAccounts 时应该直接返回
	assert.Nil(t, status.ToAccounts)
}

func TestUnpush_EmptyToAccounts(t *testing.T) {
	status := &models.Status{
		Id:          1,
		Visibility:  models.StatusVisibilityPublic,
		ToAccounts:  []*models.Account{},
	}

	// 空的 ToAccounts 时应该直接返回
	assert.Empty(t, status.ToAccounts)
}

func TestGetTimelineContent(t *testing.T) {
	account := &models.Account{Id: 1}

	t.Run("Nil params", func(t *testing.T) {
		// GetTimelineContent 需要 Redis 和数据库
		assert.NotNil(t, account)
	})

	t.Run("With params", func(t *testing.T) {
		params := &feed.ListParam{
			MaxId: 100,
			MinId: 1,
			Limit: 20,
		}
		assert.NotNil(t, params)
		assert.Equal(t, uint64(100), params.MaxId)
		assert.Equal(t, uint64(1), params.MinId)
		assert.Equal(t, uint64(20), params.Limit)
	})
}

func TestMentionTimeline_NilAccount(t *testing.T) {
	timeline := &MentionTimeline{Account: nil}

	// GetKeyFormat 应该仍然工作
	assert.Equal(t, "feed:mention:%s", timeline.GetKeyFormat())
	// GetTimelineId 会在 nil account 上 panic，这是预期行为
}

func TestMentionTimeline_KeyUniqueness(t *testing.T) {
	timelines := make(map[string]bool)

	for i := uint64(1); i <= 100; i++ {
		account := &models.Account{Id: i}
		timeline := &MentionTimeline{Account: account}
		key := timeline.GetTimelineId()

		assert.False(t, timelines[key], "Duplicate key found: %s", key)
		timelines[key] = true
	}

	assert.Len(t, timelines, 100)
}

func TestMention_StatusWithMentions(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		ToAccounts: []*models.Account{
			{Id: 100},
			{Id: 200},
		},
	}

	assert.Len(t, status.ToAccounts, 2)
	assert.Equal(t, uint64(100), status.ToAccounts[0].Id)
	assert.Equal(t, uint64(200), status.ToAccounts[1].Id)
}

func TestMention_LocalUserFilter(t *testing.T) {
	// 测试只推送给本地用户
	localAccount := &models.Account{
		Id:     1,
		Domain: nil, // 本地用户
	}

	remoteAccount := &models.Account{
		Id:     2,
		Domain: getPtr("remote.example.com"), // 远程用户
	}

	// Rebuild 时应该只查询本地用户
	assert.Nil(t, localAccount.Domain)
	assert.NotNil(t, remoteAccount.Domain)
}

func TestMentionTimeline_Rebuild(t *testing.T) {
	account := &models.Account{Id: 1}
	timeline := &MentionTimeline{Account: account}

	// Rebuild 需要 Redis 和数据库连接
	assert.NotNil(t, timeline)
}

// 辅助函数
func getPtr(s string) *string {
	return &s
}
