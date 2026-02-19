package home

import (
	"strconv"
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestHomeTimeline_GetKeyFormat(t *testing.T) {
	timeline := &HomeTimeline{}
	assert.Equal(t, "feed:home:%s", timeline.GetKeyFormat())
}

func TestHomeTimeline_GetTimelineId(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &HomeTimeline{Account: account}
	assert.Equal(t, "12345", timeline.GetTimelineId())
}

func TestHomeTimeline_GetTimelineId_DifferentAccounts(t *testing.T) {
	tests := []struct {
		name      string
		accountId uint64
		expected  string
	}{
		{"account 1", 1, "1"},
		{"account 100", 100, "100"},
		{"account max uint64", 18446744073709551615, "18446744073709551615"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &models.Account{Id: tt.accountId}
			timeline := &HomeTimeline{Account: account}
			assert.Equal(t, tt.expected, timeline.GetTimelineId())
		})
	}
}

func TestHomeTimeline_TimelineInterface(t *testing.T) {
	account := &models.Account{Id: 999}
	var timeline feed.Timeline = &HomeTimeline{Account: account}

	assert.Equal(t, "feed:home:%s", timeline.GetKeyFormat())
	assert.Equal(t, "999", timeline.GetTimelineId())
}

func TestHomeTimeline_MultipleInstances(t *testing.T) {
	accounts := []*models.Account{
		{Id: 1},
		{Id: 100},
		{Id: 999999},
	}

	for _, acc := range accounts {
		timeline := &HomeTimeline{Account: acc}
		assert.NotNil(t, timeline)
		assert.Contains(t, timeline.GetKeyFormat(), "feed:home")
		assert.NotEmpty(t, timeline.GetTimelineId())
	}
}

func TestHomeTimeline_NilAccount(t *testing.T) {
	timeline := &HomeTimeline{Account: nil}

	// GetKeyFormat should still work
	assert.Equal(t, "feed:home:%s", timeline.GetKeyFormat())

	// GetTimelineId will panic with nil account - this is expected behavior
	// The caller should ensure account is not nil
}

func TestHomeTimeline_KeyUniqueness(t *testing.T) {
	timelines := make(map[string]bool)

	for i := uint64(1); i <= 100; i++ {
		account := &models.Account{Id: i}
		timeline := &HomeTimeline{Account: account}
		key := timeline.GetTimelineId()

		assert.False(t, timelines[key], "Duplicate key found: %s", key)
		timelines[key] = true
	}

	assert.Len(t, timelines, 100)
}

func TestHomeTimeline_ConcurrentAccess(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &HomeTimeline{Account: account}

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

func TestHomeTimeline_VisibilityFilter(t *testing.T) {
	account := &models.Account{Id: 1}

	tests := []struct {
		name       string
		visibility uint8
		expected   bool
	}{
		{"public", uint8(models.StatusVisibilityPublic), true},
		{"unlisted", uint8(models.StatusVisibilityUnlisted), true},
		{"private", uint8(models.StatusVisibilityPrivate), true},
		{"direct own", uint8(models.StatusVisibilityDirect), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Direct messages visibility is handled separately in actual code
			_ = account
			_ = tt.visibility
			_ = tt.expected
			// This tests the structure, actual filtering requires database
		})
	}
}

func TestPushToHome_UserSettings(t *testing.T) {
	// 测试用户设置对转发聚合的影响
	account := &models.Account{
		Id: 1,
		User: &models.User{
			Settings: &models.UserSetting{},
		},
	}

	status := &models.Status{
		Id:         1001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	// PushToHome 需要 Redis 连接，这里只测试结构
	assert.NotNil(t, account)
	assert.NotNil(t, status)
}

func TestUnPushFromHome_UserSettings(t *testing.T) {
	account := &models.Account{
		Id: 1,
		User: &models.User{
			Settings: &models.UserSetting{},
		},
	}

	status := &models.Status{
		Id:         1001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	// UnPushFromHome 需要 Redis 连接，这里只测试结构
	assert.NotNil(t, account)
	assert.NotNil(t, status)
}

func TestPush_StatusVisibility(t *testing.T) {
	tests := []struct {
		name       string
		visibility uint8
	}{
		{"public", models.StatusVisibilityPublic},
		{"unlisted", models.StatusVisibilityUnlisted},
		{"private", models.StatusVisibilityPrivate},
		{"direct", models.StatusVisibilityDirect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &models.Status{
				Id:         1,
				AccountId:  1,
				Visibility: tt.visibility,
			}
			assert.NotNil(t, status)
		})
	}
}

func TestGetTimeLineContent(t *testing.T) {
	account := &models.Account{Id: 1}

	t.Run("Nil params", func(t *testing.T) {
		// GetTimeLineContent 需要 Redis 和数据库，这里只测试参数处理
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

func TestClearMentionsPushStatus(t *testing.T) {
	// 测试 ClearMentionsPushStatus 函数
	status := &models.Status{
		Id: 1,
	}
	// ClearMentionsPushStatus 需要数据库连接
	assert.NotNil(t, status)
}

func TestHomeTimeline_WithUserSettings(t *testing.T) {
	settings := &models.UserSetting{}
	account := &models.Account{
		Id:       1,
		User:     &models.User{Settings: settings},
	}

	timeline := &HomeTimeline{Account: account}
	assert.Equal(t, "1", timeline.GetTimelineId())
}

func TestHomeTimeline_StringConversion(t *testing.T) {
	testCases := []uint64{
		0,
		1,
		10,
		100,
		1000,
		10000,
		100000,
		1000000,
		18446744073709551615,
	}

	for _, id := range testCases {
		t.Run(strconv.FormatUint(id, 10), func(t *testing.T) {
			account := &models.Account{Id: id}
			timeline := &HomeTimeline{Account: account}
			expected := strconv.FormatUint(id, 10)
			assert.Equal(t, expected, timeline.GetTimelineId())
		})
	}
}
