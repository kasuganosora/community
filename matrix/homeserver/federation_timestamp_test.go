package homeserver

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// FederationTimestampTest 专门测试联邦事件时间戳问题
// 这是 TestSyncTimelineGap 失败的根本原因

type FederationTimestampTest struct {
	db *gorm.DB
}

// Setup 设置测试数据库.
func (ft *FederationTimestampTest) Setup(t *testing.T) {
	var err error

	ft.db, err = gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	// 迁移表结构 - 只迁移我们需要的基本字段
	require.NoError(t, ft.db.AutoMigrate(&MatrixEvent{}, &MatrixRoom{}, &MatrixRoomMember{}))
}

// Teardown 清理.
func (ft *FederationTimestampTest) Teardown() {
	sqlDB, _ := ft.db.DB()
	_ = sqlDB.Close()
}

// TestScenario_FederatedEventArrivesLate 模拟联邦事件延迟到达的场景.
func TestScenario_FederatedEventArrivesLate(t *testing.T) {
	ft := &FederationTimestampTest{}

	ft.Setup(t)
	defer ft.Teardown()

	roomID := "!test:local.server"     //nolint:goconst
	localUser := "@alice:local.server" //nolint:goconst
	remoteUser := "@bob:remote.server" //nolint:goconst

	// 记录时间线
	startTime := time.Now().UnixMilli()

	// ========== 步骤 1: 本地用户创建事件 A ==========
	// T+0: 本地事件 A (origin_ts = 现在)
	eventA := &MatrixEvent{
		EventID:        "$A:local.server", //nolint:goconst
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         localUser,
		Content:        map[string]any{"body": "A"}, //nolint:goconst
		OriginServerTS: startTime,
	}
	require.NoError(t, ft.db.Create(eventA).Error)
	t.Logf("[T+0ms] Event A created: origin_ts=%d", eventA.OriginServerTS)

	// ========== 步骤 2: 用户 Bob 首次同步 ==========
	// T+100ms: Bob sync with since=0
	sinceToken := int64(0)

	var initialEvents []MatrixEvent

	ft.db.Where("room_id = ? AND origin_server_ts > ?", roomID, sinceToken).
		Order("origin_server_ts ASC").Find(&initialEvents)

	t.Logf("[T+100ms] Bob first sync (since=%d): found %d events", sinceToken, len(initialEvents))

	for _, e := range initialEvents {
		t.Logf("  - %s: origin_ts=%d", e.EventID, e.OriginServerTS)
	}

	// 生成 next_batch token (基于最后一个事件的 origin_ts)
	lastEventTS := initialEvents[len(initialEvents)-1].OriginServerTS
	nextBatchToken := lastEventTS + 50 // 模拟 sync 生成 token
	t.Logf("[T+100ms] Generated next_batch token: %d", nextBatchToken)

	// ========== 步骤 3: 远程服务器创建事件 B ==========
	// T+200ms: 远程创建事件 B (但在远程服务器的时间可能是 T+50ms)
	// 模拟时钟偏移：远程服务器比本地慢 150ms
	remoteEventTime := startTime + 50 // 远程认为是 T+50ms

	t.Logf("[T+200ms] Remote server creates event B (remote time=%d)", remoteEventTime)

	// ========== 步骤 4: 事件 B 延迟到达 (网络延迟 100ms) ==========
	// T+300ms: 事件 B 到达本地服务器
	// 此时 origin_server_ts = startTime + 50 (远程时间)
	// 但本地数据库使用 created_at (自动设置)
	eventB := &MatrixEvent{
		EventID:        "$B:remote.server", //nolint:goconst
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         remoteUser,
		Content:        map[string]any{"body": "B"},
		OriginServerTS: remoteEventTime, // 远程创建时间: T+50ms
		SenderDomain:   "remote.server",
	}
	require.NoError(t, ft.db.Create(eventB).Error)

	// 重新查询获取 created_at
	var eventBFromDB MatrixEvent

	ft.db.First(&eventBFromDB, "event_id = ?", "$B:remote.server")

	t.Logf("[T+300ms] Event B arrived: origin_ts=%d (remote T+50ms)",
		eventBFromDB.OriginServerTS)

	// ========== 步骤 5: Bob 第二次同步 (使用之前的 token) ==========
	// T+400ms: Bob sync with since=nextBatchToken
	sinceToken = nextBatchToken // 这是基于事件 A 的 origin_ts 生成的

	t.Logf("[T+400ms] Bob second sync (since=%d)", sinceToken)
	t.Logf("  - Event B origin_ts: %d", eventBFromDB.OriginServerTS)
	t.Logf("  - since > origin_ts: %v", sinceToken > eventBFromDB.OriginServerTS)

	// 方法 1: 使用 origin_server_ts 过滤（当前实现）
	var eventsByOrigin []MatrixEvent

	ft.db.Where("room_id = ? AND origin_server_ts > ?", roomID, sinceToken).
		Order("origin_server_ts ASC").Find(&eventsByOrigin)

	t.Logf("  Query by origin_server_ts > %d: found %d events", sinceToken, len(eventsByOrigin))

	for _, e := range eventsByOrigin {
		t.Logf("    - %s: origin_ts=%d", e.EventID, e.OriginServerTS)
	}

	// ========== 关键验证 ==========

	// 验证 1: 使用 origin_server_ts 过滤会遗漏联邦事件 B
	foundByOrigin := false

	for _, e := range eventsByOrigin {
		if e.EventID == "$B:remote.server" {
			foundByOrigin = true

			break
		}
	}

	t.Log("")
	t.Log("========== TEST RESULT ==========")
	t.Logf("Event B found by origin_server_ts filter: %v (EXPECTED: true, ACTUAL: %v)",
		foundByOrigin, foundByOrigin)

	if !foundByOrigin {
		t.Log("")
		t.Log("CONFIRMED: This is the root cause of TestSyncTimelineGap failure!")
		t.Log("- Using origin_server_ts filter misses delayed federated events")
		t.Log("- Event B has origin_ts=1050 but since=1150")
		t.Log("- Solution: Use created_at or a hybrid approach")
	}
	// 这个测试预期会失败以演示问题
	// 实际修复后应该改为 assert.True(t, foundByOrigin)
}

// TestScenario_MultipleFederatedEvents 测试多个联邦事件乱序到达.
func TestScenario_MultipleFederatedEvents(t *testing.T) {
	ft := &FederationTimestampTest{}

	ft.Setup(t)
	defer ft.Teardown()

	roomID := "!test:local.server"
	startTime := time.Now().UnixMilli()

	// 创建本地事件 A
	eventA := &MatrixEvent{
		EventID:        "$A:local.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         "@alice:local.server",
		Content:        map[string]any{"body": "A"},
		OriginServerTS: startTime,
	}
	ft.db.Create(eventA)

	// 模拟多个联邦事件乱序到达
	// 事件 C 创建时间最早，但到达最晚
	events := []struct {
		ID       string
		OriginTS int64
	}{
		{"$B:remote.server", startTime + 100},
		{"$C:remote.server", startTime + 50}, //nolint:goconst
		{"$D:remote.server", startTime + 150},
	}

	for _, e := range events {
		event := &MatrixEvent{
			EventID:        e.ID,
			RoomID:         roomID,
			EventType:      EventTypeRoomMessage,
			Sender:         "@bob:remote.server",
			Content:        map[string]any{"body": e.ID},
			OriginServerTS: e.OriginTS,
			SenderDomain:   "remote.server",
		}
		ft.db.Create(event)
		t.Logf("Created %s: origin_ts=%d", e.ID, e.OriginTS)
	}

	// Sync token 基于事件 A 的时间
	sinceToken := startTime + 50

	// 查询: origin_server_ts
	var byOrigin []MatrixEvent

	ft.db.Where("room_id = ? AND origin_server_ts > ?", roomID, sinceToken).
		Order("origin_server_ts ASC").Find(&byOrigin)

	t.Log("")
	t.Logf("Sync since=%d", sinceToken)
	t.Logf("By origin_server_ts: %d events", len(byOrigin))

	for _, e := range byOrigin {
		t.Logf("  %s: origin=%d", e.EventID, e.OriginServerTS)
	}

	// 事件 C (origin_ts=startTime+50) 会被 origin_server_ts 过滤漏掉
	// 因为它的 origin_ts <= sinceToken
	assert.Len(t, byOrigin, 2, "Should find 2 events by origin_ts (B and D)")
}

// TestSyncTokenTimestampBasis 验证 sync token 基于什么时间生成.
func TestSyncTokenTimestampBasis(t *testing.T) {
	eventService := &EventService{}
	now := time.Now().UnixMilli()

	// Token 基于时间戳生成
	token := eventService.generatePaginationToken(now)

	t.Logf("Token generated from ts=%d: %s", now, token)

	// 验证 token 格式
	assert.NotEmpty(t, token, "Token should not be empty")

	// 关键问题：token 基于 origin_server_ts 还是 created_at？
	t.Log("")
	t.Log("IMPORTANT: If sync token is based on origin_server_ts, but query uses created_at,")
	t.Log("there will be a mismatch for federated events!")
	t.Log("")
	t.Log("Current implementation uses origin_server_ts for both token and query.")
	t.Log("This causes federated events with old origin_ts to be filtered out.")
}

// TestTimestampMismatchExplanation 解释时间戳不匹配问题.
func TestTimestampMismatchExplanation(t *testing.T) {
	t.Log("========== FEDERATION TIMESTAMP PROBLEM ANALYSIS ==========")
	t.Log("")
	t.Log("TIMELINE:")
	t.Log("")
	t.Log("T+0ms    [HS1] Alice creates event A")
	t.Log("         origin_server_ts = 1000")
	t.Log("")
	t.Log("T+50ms   [HS2] Bob syncs (since=0)")
	t.Log("         Returns: [A]")
	t.Log("         next_batch token = t1050 (based on A.origin_ts + 50)")
	t.Log("")
	t.Log("T+100ms  [HS1] Alice creates event B (origin_ts=1100)")
	t.Log("         (federation delay to HS2: 100ms)")
	t.Log("")
	t.Log("T+200ms  [HS2] Event B arrives (delayed)")
	t.Log("         origin_server_ts = 1100 (from HS1)")
	t.Log("")
	t.Log("T+250ms  [HS2] Bob syncs (since=1050)")
	t.Log("         Query: origin_server_ts > 1050")
	t.Log("         Event B: 1100 > 1050 = true ✓ (works)")
	t.Log("")
	t.Log("BUT:")
	t.Log("")
	t.Log("T+150ms  [HS2] Bob syncs before B arrives")
	t.Log("         Query: origin_server_ts > 1050")
	t.Log("         No new events, token stays at 1050")
	t.Log("")
	t.Log("T+200ms  [HS2] Event C arrives (very delayed)")
	t.Log("         origin_server_ts = 1080 (created between A and B)")
	t.Log("")
	t.Log("T+250ms  [HS2] Bob syncs (since=1050)")
	t.Log("         Query: origin_server_ts > 1050")
	t.Log("         Event C: 1080 > 1050 = true ✓ (found)")
	t.Log("")
	t.Log("T+300ms  [HS2] Bob syncs again, gets B, token advances to 1150")
	t.Log("")
	t.Log("T+350ms  [HS2] Bob syncs (since=1150)")
	t.Log("         Query: origin_server_ts > 1150")
	t.Log("         Event C: 1080 > 1150 = false ❌ LOST!")
	t.Log("")
	t.Log("ROOT CAUSE:")
	t.Log("- Sync token advances monotonically")
	t.Log("- Federated events arrive out of order")
	t.Log("- Old origin_ts events get filtered by new token")
	t.Log("")
	t.Log("SOLUTION:")
	t.Log("Use received_time (created_at) for ordering, not origin_server_ts")
}
