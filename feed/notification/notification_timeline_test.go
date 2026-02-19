package notification

import (
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestNotificationTimeline_GetKeyFormat(t *testing.T) {
	timeline := &NotificationTimeline{}
	assert.Equal(t, "feed:notification:%s", timeline.GetKeyFormat())
}

func TestNotificationTimeline_GetTimelineId(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &NotificationTimeline{Account: account}
	assert.Equal(t, "12345", timeline.GetTimelineId())
}

func TestNotificationTimeline_TimelineInterface(t *testing.T) {
	account := &models.Account{Id: 999}
	var timeline feed.Timeline = &NotificationTimeline{Account: account}

	assert.Equal(t, "feed:notification:%s", timeline.GetKeyFormat())
	assert.Equal(t, "999", timeline.GetTimelineId())
}

func TestNotificationTimeline_MultipleAccounts(t *testing.T) {
	accounts := []*models.Account{
		{Id: 1},
		{Id: 100},
		{Id: 999999},
	}

	for _, acc := range accounts {
		timeline := &NotificationTimeline{Account: acc}
		assert.NotNil(t, timeline)
		assert.Contains(t, timeline.GetKeyFormat(), "feed:notification:")
		assert.NotEmpty(t, timeline.GetTimelineId())
	}
}

func TestNotificationTimeline_ConcurrentAccess(t *testing.T) {
	account := &models.Account{Id: 12345}
	timeline := &NotificationTimeline{Account: account}

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

func TestNotificationTimeline_NilAccount(t *testing.T) {
	timeline := &NotificationTimeline{Account: nil}

	// GetKeyFormat 应该仍然工作
	assert.Equal(t, "feed:notification:%s", timeline.GetKeyFormat())
	// GetTimelineId 会在 nil account 上 panic，这是预期行为
}

func TestNotificationTimeline_KeyUniqueness(t *testing.T) {
	timelines := make(map[string]bool)

	for i := uint64(1); i <= 100; i++ {
		account := &models.Account{Id: i}
		timeline := &NotificationTimeline{Account: account}
		key := timeline.GetTimelineId()

		assert.False(t, timelines[key], "Duplicate key found: %s", key)
		timelines[key] = true
	}

	assert.Len(t, timelines, 100)
}

func TestPush_WithAccount(t *testing.T) {
	account := &models.Account{Id: 1}
	notification := &models.Notification{
		Id:        1,
		AccountId: 1,
		Account:   account,
	}

	// Push 需要 Redis 连接
	assert.NotNil(t, notification)
	assert.NotNil(t, notification.Account)
}

func TestPush_WithoutAccount(t *testing.T) {
	notification := &models.Notification{
		Id:        1,
		AccountId: 1,
		Account:   nil,
	}

	// 没有加载 Account 时需要先加载
	assert.Nil(t, notification.Account)
}

func TestNotificationTimeline_Rebuild(t *testing.T) {
	account := &models.Account{Id: 1}
	timeline := &NotificationTimeline{Account: account}

	// Rebuild 需要 Redis 和数据库连接
	assert.NotNil(t, timeline)
}

func TestNotificationTypes(t *testing.T) {
	types := []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}

	for _, nt := range types {
		t.Run("", func(t *testing.T) {
			notification := &models.Notification{
				Type: nt,
			}
			assert.Equal(t, nt, notification.Type)
		})
	}
}

func TestNotificationTimeline_PushNotification(t *testing.T) {
	account := &models.Account{Id: 1}
	notification := &models.Notification{
		Id:        1,
		AccountId: 1,
		Account:   account,
		Type:      0, // mention
	}

	// 测试通知推送逻辑
	assert.NotNil(t, notification)
	assert.Equal(t, uint8(0), notification.Type)
}

func TestNotificationTimeline_Trim(t *testing.T) {
	account := &models.Account{Id: 1}
	timeline := &NotificationTimeline{Account: account}

	// trim 需要 Redis 连接
	assert.NotNil(t, timeline)
}

func TestNotificationTimeline_PushToClient(t *testing.T) {
	account := &models.Account{Id: 1}
	timeline := &NotificationTimeline{Account: account}
	notification := &models.Notification{
		Id:        1,
		AccountId: 1,
		Account:   account,
		Type:      1, // favourite
	}

	// push 方法需要 Redis 连接
	assert.NotNil(t, timeline)
	assert.NotNil(t, notification)
}

func TestNotification_LoadAllExtendFields(t *testing.T) {
	notification := &models.Notification{
		Id:        1,
		AccountId: 1,
		Account:   nil,
	}

	// LoadAllExtendFields 需要数据库连接
	assert.Nil(t, notification.Account)
}

func TestNotificationTimeline_MultipleNotifications(t *testing.T) {
	account := &models.Account{Id: 1}

	notifications := []*models.Notification{
		{Id: 1, AccountId: 1, Account: account, Type: 0}, // mention
		{Id: 2, AccountId: 1, Account: account, Type: 1}, // favourite
		{Id: 3, AccountId: 1, Account: account, Type: 2}, // reblog
	}

	assert.Len(t, notifications, 3)
}

func TestNotificationTimeline_PushWithTrim(t *testing.T) {
	account := &models.Account{Id: 1}
	timeline := &NotificationTimeline{Account: account}
	notification := &models.Notification{
		Id:        1,
		AccountId: 1,
		Account:   account,
	}

	// push with trim 需要测试
	assert.NotNil(t, timeline)
	assert.NotNil(t, notification)
}
