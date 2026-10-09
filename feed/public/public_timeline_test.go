package public

import (
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

// setupMockFeedService 安装基于 MockRedisClient 的 FeedService，
// 使 Push/Unpush 等时间线操作无需真实 Redis/DB 即可运行。
// 返回底层 MockRedisClient 以便断言状态。
func setupMockFeedService() *feed.MockRedisClient {
	mockRedis := feed.NewMockRedisClient()
	feed.SetDefaultService(feed.NewFeedService(mockRedis))
	feed.ResetSubscriptionCache()
	return mockRedis
}

// strPtr 返回指向 s 的指针，用于构造远程 status 的 Domain 字段。
func strPtr(s string) *string {
	return &s
}

// ---------------------------------------------------------------------------
// GetKeyFormat
// ---------------------------------------------------------------------------

func TestPublicTimeline_GetKeyFormat(t *testing.T) {
	timeline := &PublicTimeline{}
	assert.Equal(t, "feed:public:%s", timeline.GetKeyFormat())
}

// ---------------------------------------------------------------------------
// GetTimelineId —— 覆盖所有 OnlyMedia/OnlyLocal/OnlyRemote 组合
// ---------------------------------------------------------------------------

func TestPublicTimeline_GetTimelineId(t *testing.T) {
	tests := []struct {
		name     string
		timeline *PublicTimeline
		expected string
	}{
		{"无标志位返回 all", &PublicTimeline{}, "all"},
		{"仅 media", &PublicTimeline{OnlyMedia: true}, "media"},
		{"仅 local", &PublicTimeline{OnlyLocal: true}, "local"},
		{"仅 remote", &PublicTimeline{OnlyRemote: true}, "remote"},
		{"media + local", &PublicTimeline{OnlyMedia: true, OnlyLocal: true}, "media:local"},
		{"media + remote", &PublicTimeline{OnlyMedia: true, OnlyRemote: true}, "media:remote"},
		{"local + remote", &PublicTimeline{OnlyLocal: true, OnlyRemote: true}, "local:remote"},
		{"三个标志位同时设置", &PublicTimeline{OnlyMedia: true, OnlyLocal: true, OnlyRemote: true}, "media:local:remote"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.timeline.GetTimelineId())
		})
	}
}

// ---------------------------------------------------------------------------
// Push —— 可见性过滤
// ---------------------------------------------------------------------------

// 非 public 可见性的 status 不应进入公共时间线。
func TestPush_RejectsNonPublicVisibility(t *testing.T) {
	mockRedis := setupMockFeedService()

	nonPublic := []uint8{
		models.StatusVisibilityUnlisted,
		models.StatusVisibilityPrivate,
		models.StatusVisibilityDirect,
	}

	for _, vis := range nonPublic {
		status := &models.Status{
			Id:         2000 + uint64(vis),
			Visibility: vis,
		}
		err := Push(status, false, true)
		assert.NoError(t, err, "visibility=%d 不应返回错误", vis)
	}

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Empty(t, zSet, "非公开 status 不应被推送到时间线")
}

// public 可见性的 status 应被添加到 "all" 时间线。
func TestPush_PublicStatus_AddsToTimeline(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         2001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, false, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Contains(t, zSet, "2001")
}

// update=true 时使用 "status.update" 方法（pushToClient=false 时不触发客户端推送）。
func TestPush_UpdateFlag(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         2002,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, true, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Contains(t, zSet, "2002")
}

// 推送多条 status 后时间线应包含所有 ID。
func TestPush_MultipleStatuses(t *testing.T) {
	mockRedis := setupMockFeedService()

	for i := range 5 {
		status := &models.Status{
			Id:         uint64(6000 + i),
			AccountId:  1,
			Visibility: models.StatusVisibilityPublic,
		}
		err := Push(status, false, false)
		require.NoError(t, err)
	}

	zSet := mockRedis.GetZSet("feed:public:all")
	for i := range 5 {
		assert.Contains(t, zSet, strconv.FormatUint(uint64(6000+i), 10))
	}
}

// ---------------------------------------------------------------------------
// pushAllChannel —— 通过 Push(pushToClient=true) 测试 local/remote/media 过滤
// ---------------------------------------------------------------------------

// 本地 status（Domain==nil, Account!=nil）配合 pushToClient=true。
func TestPush_ClientPush_LocalStatus(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         3001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
		// Domain 为 nil → 本地
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

// 远程 status（Domain!=nil, Account!=nil）配合 pushToClient=true。
func TestPush_ClientPush_RemoteStatus(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         3002,
		AccountId:  2,
		Visibility: models.StatusVisibilityPublic,
		Domain:     strPtr("example.com"),
		Account:    &models.Account{Id: 2},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

// 带附件的 status 配合 pushToClient=true（覆盖 OnlyMedia 时间线分支）。
func TestPush_ClientPush_MediaStatus(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         3003,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
		Attachments: models.Attachments{
			{Id: 1, Type: models.MediaTypeImage},
		},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

// Account 为 nil 时 local/remote 时间线分支应被跳过。
func TestPush_ClientPush_NilAccount(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         3004,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		// Account 为 nil → local 和 remote 时间线均跳过
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

// 远程带附件的 status（覆盖 OnlyRemote+OnlyMedia 组合分支）。
func TestPush_ClientPush_RemoteMediaStatus(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         3005,
		AccountId:  3,
		Visibility: models.StatusVisibilityPublic,
		Domain:     strPtr("remote.example.com"),
		Account:    &models.Account{Id: 3},
		Attachments: models.Attachments{
			{Id: 2, Type: models.MediaTypeVideo},
		},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

// 有订阅者时 PushToClient 会被调用（覆盖推送路径）。
func TestPush_ClientPush_WithSubscribers(t *testing.T) {
	mockRedis := setupMockFeedService()

	// 模拟 "all" 时间线有订阅者
	mockRedis.SetChannel("subscribed:feed:public:all", true)

	status := &models.Status{
		Id:         3006,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
		Attachments: models.Attachments{
			{Id: 3, Type: models.MediaTypeImage},
		},
	}

	err := Push(status, false, true)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Contains(t, zSet, "3006")
}

// ---------------------------------------------------------------------------
// Unpush
// ---------------------------------------------------------------------------

// Unpush 应从时间线移除 status。
func TestUnpush_RemovesFromTimeline(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         4001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}
	err := Push(status, false, false)
	require.NoError(t, err)
	assert.Contains(t, mockRedis.GetZSet("feed:public:all"), "4001")

	err = Unpush(status, false)
	require.NoError(t, err)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.NotContains(t, zSet, "4001")
}

// Unpush 配合 pushToClient=true（覆盖 pushAllChannel "delete" 路径）。
func TestUnpush_WithClientPush(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         4002,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
	}

	err := Unpush(status, true)
	require.NoError(t, err)
}

// Unpush 非 public status 配合 pushToClient=true，
// pushAllChannel 应因可见性检查提前返回。
func TestUnpush_NonPublic_WithClientPush(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         4003,
		AccountId:  1,
		Visibility: models.StatusVisibilityPrivate,
	}

	err := Unpush(status, true)
	require.NoError(t, err)
}

// Unpush 不存在的 status 不应报错。
func TestUnpush_NonExistentStatus(t *testing.T) {
	setupMockFeedService()

	status := &models.Status{
		Id:         99999,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Unpush(status, false)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// 事件处理器（init 中注册的 "status"/"*" handler）
// ---------------------------------------------------------------------------

// nil Data 不应 panic。
func TestEventHandler_NilData(t *testing.T) {
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: nil})
	})
}

// after_create 事件应将 public status 推送到时间线。
func TestEventHandler_AfterCreate_PublicStatus(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         5001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	event.Trigger("status", event.Event{Name: "after_create", Data: status})
	time.Sleep(200 * time.Millisecond) // 等待 handler 中的 goroutine 完成

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Contains(t, zSet, "5001")
}

// A post imported late (backfilled) never enters the shared timeline, on
// create or on update, while a live post next to it still does.
func TestEventHandler_BackfilledStatusSkipped(t *testing.T) {
	mockRedis := setupMockFeedService()

	old := &models.Status{Id: 5010, AccountId: 1, Visibility: models.StatusVisibilityPublic}
	old.MarkBackfilled()
	live := &models.Status{Id: 5011, AccountId: 1, Visibility: models.StatusVisibilityPublic}

	for _, name := range []string{"after_create", "after_update"} {
		event.Trigger("status", event.Event{Name: name, Data: old})
	}
	event.Trigger("status", event.Event{Name: "after_create", Data: live})
	time.Sleep(200 * time.Millisecond)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Contains(t, zSet, "5011")
	assert.NotContains(t, zSet, "5010")
}

// 非 public status 的事件应被跳过。
func TestEventHandler_NonPublicStatus_Skipped(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         5002,
		AccountId:  1,
		Visibility: models.StatusVisibilityPrivate,
	}

	event.Trigger("status", event.Event{Name: "after_create", Data: status})
	time.Sleep(200 * time.Millisecond)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Empty(t, zSet, "非公开 status 不应出现在时间线")
}

// after_delete 事件应从时间线移除 status。
func TestEventHandler_AfterDelete(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         5003,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}
	require.NoError(t, Push(status, false, false))
	assert.Contains(t, mockRedis.GetZSet("feed:public:all"), "5003")

	event.Trigger("status", event.Event{Name: "after_delete", Data: status})
	time.Sleep(200 * time.Millisecond)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.NotContains(t, zSet, "5003")
}

// after_update 事件应先删除再重新推送 status。
func TestEventHandler_AfterUpdate(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         5004,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	event.Trigger("status", event.Event{Name: "after_update", Data: status})
	time.Sleep(200 * time.Millisecond)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.Contains(t, zSet, "5004")
}

// before_update 事件应从时间线移除 status。
func TestEventHandler_BeforeUpdate(t *testing.T) {
	mockRedis := setupMockFeedService()

	status := &models.Status{
		Id:         5005,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}
	require.NoError(t, Push(status, false, false))
	assert.Contains(t, mockRedis.GetZSet("feed:public:all"), "5005")

	event.Trigger("status", event.Event{Name: "before_update", Data: status})
	time.Sleep(200 * time.Millisecond)

	zSet := mockRedis.GetZSet("feed:public:all")
	assert.NotContains(t, zSet, "5005")
}

// ---------------------------------------------------------------------------
// PublicTimeline 实现 feed.Timeline 接口验证
// ---------------------------------------------------------------------------

func TestPublicTimeline_ImplementsTimelineInterface(t *testing.T) {
	var _ feed.Timeline = (*PublicTimeline)(nil)
}
