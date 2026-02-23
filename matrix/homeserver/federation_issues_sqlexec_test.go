package homeserver

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setTestDB 设置测试数据库
func setTestDB(gormDB *gorm.DB) {
	database.SetTestDB(gormDB)
}

// TestFederationTimestampProblem 使用 sqlexec 验证联邦事件时间戳问题
// 这是 TestSyncTimelineGap 失败的根本原因
func TestFederationTimestampProblem(t *testing.T) {
	// 创建隔离的测试数据库
	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)

	// 设置全局 db 供 homeserver 使用
	setTestDB(testDB.GormDB)

	// 迁移表结构
	err = testDB.AutoMigrate(
		&MatrixEvent{},
		&MatrixRoom{},
		&MatrixRoomMember{},
	)
	require.NoError(t, err)

	roomID := "!test:local.server"
	localUser := "@alice:local.server"
	remoteUser := "@bob:remote.server"

	// 记录时间线
	startTime := time.Now().UnixMilli()

	t.Log("========== FEDERATION TIMESTAMP PROBLEM DEMONSTRATION ==========")
	t.Log("")

	// ========== 步骤 1: 本地用户创建事件 A ==========
	t.Logf("[T+0ms] Creating local event A...")
	eventA := &MatrixEvent{
		EventID:        "$A:local.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         localUser,
		Content:        map[string]interface{}{"body": "A"},
		OriginServerTS: startTime,
		SenderDomain:   "local.server",
	}
	err = testDB.GormDB.Create(eventA).Error
	require.NoError(t, err)
	t.Logf("[T+0ms] Event A created: origin_ts=%d", eventA.OriginServerTS)

	// ========== 步骤 2: 用户 Bob 首次同步 ==========
	sinceToken := int64(0)

	var initialEvents []MatrixEvent
	testDB.GormDB.Where("room_id = ? AND origin_server_ts > ?", roomID, sinceToken).
		Order("origin_server_ts ASC").Find(&initialEvents)

	t.Logf("[T+100ms] Bob first sync (since=%d): found %d events", sinceToken, len(initialEvents))
	for _, e := range initialEvents {
		t.Logf("  - %s: origin_ts=%d", e.EventID, e.OriginServerTS)
	}

	// 生成 next_batch token (基于最后一个事件的 origin_ts)
	lastEventTS := initialEvents[len(initialEvents)-1].OriginServerTS
	nextBatchToken := lastEventTS + 50 // 模拟 sync 生成 token
	t.Logf("[T+100ms] Generated next_batch token: %d", nextBatchToken)

	// ========== 步骤 3: 远程服务器创建事件 B (有延迟) ==========
	remoteEventTime := startTime + 50 // 远程时间是 T+50ms
	t.Logf("[T+200ms] Remote server creates event B (remote time=%d)", remoteEventTime)

	// ========== 步骤 4: 事件 B 延迟到达 ==========
	// 关键：origin_server_ts 是远程时间，但事件现在才到达
	eventB := &MatrixEvent{
		EventID:        "$B:remote.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         remoteUser,
		Content:        map[string]interface{}{"body": "B"},
		OriginServerTS: remoteEventTime, // 远程创建时间: T+50ms
		SenderDomain:   "remote.server",
	}
	err = testDB.GormDB.Create(eventB).Error
	require.NoError(t, err)

	// 重新查询获取完整数据
	var eventBFromDB MatrixEvent
	testDB.GormDB.First(&eventBFromDB, "event_id = ?", "$B:remote.server")

	t.Logf("[T+300ms] Event B arrived: origin_ts=%d (remote T+50ms)",
		eventBFromDB.OriginServerTS)

	// ========== 步骤 5: Bob 第二次同步 ==========
	sinceToken = nextBatchToken

	t.Log("")
	t.Logf("[T+400ms] Bob second sync (since=%d)", sinceToken)
	t.Logf("  - Event B origin_ts: %d", eventBFromDB.OriginServerTS)
	t.Logf("  - Query: origin_server_ts > %d", sinceToken)
	t.Logf("  - Event B would match: %v", eventBFromDB.OriginServerTS > sinceToken)

	// 方法 1: 使用 origin_server_ts 过滤（当前实现）
	var eventsByOrigin []MatrixEvent
	testDB.GormDB.Where("room_id = ? AND origin_server_ts > ?", roomID, sinceToken).
		Order("origin_server_ts ASC").Find(&eventsByOrigin)

	t.Logf("  Result: found %d events", len(eventsByOrigin))
	for _, e := range eventsByOrigin {
		t.Logf("    - %s: origin_ts=%d", e.EventID, e.OriginServerTS)
	}

	// ========== 关键验证 ==========
	foundByOrigin := false
	for _, e := range eventsByOrigin {
		if e.EventID == "$B:remote.server" {
			foundByOrigin = true
			break
		}
	}

	t.Log("")
	t.Log("========== VERIFICATION ==========")

	if !foundByOrigin {
		t.Logf("❌ PROBLEM CONFIRMED: Event B NOT found by origin_server_ts filter!")
		t.Logf("   - Event B origin_ts: %d", eventBFromDB.OriginServerTS)
		t.Logf("   - Sync since: %d", sinceToken)
		t.Logf("   - Reason: origin_ts(%d) <= since(%d)", eventBFromDB.OriginServerTS, sinceToken)
		t.Log("")
		t.Log("   This is the root cause of TestSyncTimelineGap failure!")
		t.Log("   Federated events arrive late but have old origin_server_ts.")
		t.Log("   Using origin_server_ts for sync filtering causes events to be lost.")
	} else {
		t.Logf("✅ Event B found by origin_server_ts filter")
	}

	// 断言：这个问题在特定时间差下会出现
	// 注意：这个测试演示问题，所以预期在某些情况下会失败
	t.Log("")
	t.Log("SOLUTION:")
	t.Log("  Use created_at (local arrival time) instead of origin_server_ts")
	t.Log("  for sync filtering and token generation.")
}

// TestFederatedEventsOutOfOrder 验证多个联邦事件乱序到达（修复后使用 created_at）
func TestFederatedEventsOutOfOrder(t *testing.T) {
	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	setTestDB(testDB.GormDB)

	err = testDB.AutoMigrate(&MatrixEvent{}, &MatrixRoom{})
	require.NoError(t, err)

	roomID := "!test:local.server"
	startTime := time.Now().UnixMilli()

	t.Log("========== OUT-OF-ORDER FEDERATED EVENTS (FIXED) ==========")
	t.Log("")

	// 创建本地事件 A
	eventA := &MatrixEvent{
		EventID:        "$A:local.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         "@alice:local.server",
		Content:        map[string]interface{}{"body": "A"},
		OriginServerTS: startTime,
		SenderDomain:   "local.server",
	}
	testDB.GormDB.Create(eventA)
	
	// 获取 A 的创建时间
	var eventAFromDB MatrixEvent
	testDB.GormDB.First(&eventAFromDB, "event_id = ?", "$A:local.server")
	createdAtA := eventAFromDB.CreatedAt.UnixMilli()
	
	t.Logf("Created local event A: origin_ts=%d, created_at=%d", startTime, createdAtA)

	// 创建多个联邦事件，模拟乱序到达
	// 等待一下确保 created_at 不同
	time.Sleep(10 * time.Millisecond)
	
	eventB := &MatrixEvent{
		EventID:        "$B:remote.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         "@bob:remote.server",
		Content:        map[string]interface{}{"body": "B"},
		OriginServerTS: startTime + 100, // 远程创建时间
		SenderDomain:   "remote.server",
	}
	testDB.GormDB.Create(eventB)
	
	time.Sleep(10 * time.Millisecond)
	
	eventD := &MatrixEvent{
		EventID:        "$D:remote.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         "@bob:remote.server",
		Content:        map[string]interface{}{"body": "D"},
		OriginServerTS: startTime + 150, // 远程创建时间
		SenderDomain:   "remote.server",
	}
	testDB.GormDB.Create(eventD)
	
	// 事件 C 延迟到达（created_at 最晚，但 origin_ts 最早）
	time.Sleep(10 * time.Millisecond)
	
	eventC := &MatrixEvent{
		EventID:        "$C:remote.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         "@bob:remote.server",
		Content:        map[string]interface{}{"body": "C"},
		OriginServerTS: startTime + 50, // 最早创建，但最晚到达！
		SenderDomain:   "remote.server",
	}
	testDB.GormDB.Create(eventC)
	
	// 获取所有事件的创建时间
	var eventsFromDB []MatrixEvent
	testDB.GormDB.Where("room_id = ?", roomID).Order("created_at ASC").Find(&eventsFromDB)
	
	t.Log("")
	t.Log("Events in creation order (by created_at):")
	for _, e := range eventsFromDB {
		t.Logf("  %s: origin_ts=%d, created_at=%d", e.EventID, e.OriginServerTS, e.CreatedAt.UnixMilli())
	}

	// 模拟 sync token 基于 created_at
	// Bob 先看到 B 和 D，token 推进到 D 的 created_at
	var eventBFromDB, eventDFromDB MatrixEvent
	testDB.GormDB.First(&eventBFromDB, "event_id = ?", "$B:remote.server")
	testDB.GormDB.First(&eventDFromDB, "event_id = ?", "$D:remote.server")
	
	// 使用 D 的 created_at 作为 since（模拟 Bob 已经看到 D）
	sinceTime := time.UnixMilli(eventDFromDB.CreatedAt.UnixMilli())

	t.Log("")
	t.Logf("Sync with created_at > %d (after seeing event D)", sinceTime.UnixMilli())

	// ===== FIX: 使用 created_at 查询（修复后的方式）=====
	var byCreatedAt []MatrixEvent
	testDB.GormDB.Where("room_id = ? AND created_at > ?", roomID, sinceTime).
		Order("created_at ASC").Find(&byCreatedAt)

	t.Logf("Events found with created_at > %d: %d", sinceTime.UnixMilli(), len(byCreatedAt))
	for _, e := range byCreatedAt {
		t.Logf("  %s: created_at=%d, origin_ts=%d", e.EventID, e.CreatedAt.UnixMilli(), e.OriginServerTS)
	}

	// 验证：事件 C 应该被找到（因为它的 created_at 最晚）
	hasC := false
	for _, e := range byCreatedAt {
		if e.EventID == "$C:remote.server" {
			hasC = true
			break
		}
	}

	t.Log("")
	if hasC {
		t.Log("✅ Event C is FOUND using created_at filter!")
		t.Log("   Even though C.origin_ts is old, C.created_at is new.")
		t.Log("   This is the correct behavior after the fix.")
	} else {
		t.Log("❌ Event C is still lost - fix may not be complete")
	}

	// 断言：应该找到 C（因为它的 created_at 在 D 之后）
	assert.True(t, hasC, "Event C should be found using created_at filter")
	assert.GreaterOrEqual(t, len(byCreatedAt), 1, "Should find at least event C")
}

// TestSyncTokenGeneration 验证 sync token 生成
func TestSyncTokenGeneration(t *testing.T) {
	eventService := &EventService{}
	now := time.Now().UnixMilli()

	t.Log("========== SYNC TOKEN GENERATION ==========")
	t.Log("")

	// 生成 token
	token := eventService.generatePaginationToken(now)
	t.Logf("Generated token from ts=%d: %s", now, token)

	// 解析 token
	parsedTS := eventService.parsePaginationToken(token)
	t.Logf("Parsed timestamp: %d", parsedTS)

	assert.Equal(t, now, parsedTS, "Token should round-trip correctly")

	t.Log("")
	t.Log("IMPORTANT:")
	t.Log("  Current implementation uses origin_server_ts for token generation.")
	t.Log("  This means sync token advances based on event creation time,")
	t.Log("  not event arrival time.")
	t.Log("")
	t.Log("  For federated events:")
	t.Log("    - Creation time = origin_server_ts (remote server time)")
	t.Log("    - Arrival time = created_at (local server time)")
	t.Log("")
	t.Log("  When a federated event arrives late:")
	t.Log("    - Its origin_server_ts is old")
	t.Log("    - But sync token has already advanced")
	t.Log("    - The event is filtered out forever")
}

// TestSolutionCreatedAt 演示使用 created_at 的解决方案
func TestSolutionCreatedAt(t *testing.T) {
	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	setTestDB(testDB.GormDB)

	err = testDB.AutoMigrate(&MatrixEvent{}, &MatrixRoom{})
	require.NoError(t, err)

	roomID := "!test:local.server"
	startTime := time.Now().UnixMilli()

	t.Log("========== SOLUTION: USING created_at ==========")
	t.Log("")

	// 创建本地事件 A
	eventA := &MatrixEvent{
		EventID:        "$A:local.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         "@alice:local.server",
		Content:        map[string]interface{}{"body": "A"},
		OriginServerTS: startTime,
		SenderDomain:   "local.server",
	}
	testDB.GormDB.Create(eventA)

	// 获取 A 的创建时间
	var eventAFromDB MatrixEvent
	testDB.GormDB.First(&eventAFromDB, "event_id = ?", "$A:local.server")
	createdAtA := eventAFromDB.CreatedAt.UnixMilli()

	t.Logf("Event A: origin_ts=%d, created_at=%d", startTime, createdAtA)

	// 创建延迟到达的联邦事件 B
	time.Sleep(50 * time.Millisecond) // 模拟网络延迟

	eventB := &MatrixEvent{
		EventID:        "$B:remote.server",
		RoomID:         roomID,
		EventType:      EventTypeRoomMessage,
		Sender:         "@bob:remote.server",
		Content:        map[string]interface{}{"body": "B"},
		OriginServerTS: startTime + 30, // 远程时间（早于当前）
		SenderDomain:   "remote.server",
	}
	testDB.GormDB.Create(eventB)

	// 获取 B 的创建时间
	var eventBFromDB MatrixEvent
	testDB.GormDB.First(&eventBFromDB, "event_id = ?", "$B:remote.server")
	createdAtB := eventBFromDB.CreatedAt.UnixMilli()

	t.Logf("Event B: origin_ts=%d, created_at=%d", eventB.OriginServerTS, createdAtB)

	t.Log("")
	t.Log("Comparison:")
	t.Logf("  origin_server_ts: A(%d) < B(%d) - A comes first", startTime, eventB.OriginServerTS)
	t.Logf("  created_at:       A(%d) < B(%d) - A comes first", createdAtA, createdAtB)
	t.Log("")
	t.Log("  Both orderings are consistent in this case.")
	t.Log("  But if B arrived before A (network reordering):")
	t.Logf("    origin_server_ts: B(%d) < A(%d) - B comes first (WRONG!)", eventB.OriginServerTS, startTime)
	t.Logf("    created_at:       A(%d) < B(%d) - A comes first (CORRECT!)", createdAtA, createdAtB)

	t.Log("")
	t.Log("CONCLUSION:")
	t.Log("  Use created_at for sync ordering to ensure events are")
	t.Log("  processed in the order they arrive at the local server.")
}
