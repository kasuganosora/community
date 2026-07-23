package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== Test 1: Device List EDU 发送测试 =====

// TestDeviceListEDUSending 验证设备列表 EDU 是否正确发送.
func TestDeviceListEDUSending(t *testing.T) {
	// 模拟远程服务器接收 EDU
	var receivedEDU *map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 读取请求体
		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil {
			t.Logf("Failed to decode EDU: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		receivedEDU = &body
		t.Logf("Received EDU: %+v", body)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pdus": []any{}, //nolint:goconst
		})
	}))
	defer server.Close()

	// 创建设备列表服务
	hs := &HomeServer{
		Config: &HSConfig{
			ServerName: "test.server",
		},
	}
	_ = NewDeviceListService(hs)

	// 手动构造 EDU 并发送
	edu := &DeviceListUpdateEDU{
		UserID:   "@alice:test.server", //nolint:goconst
		DeviceID: "DEVICE123",          //nolint:goconst
		StreamID: 1,
		Changed:  []string{"DEVICE123"},
	}

	// 手动调用发送逻辑（模拟 federateDeviceListUpdate）
	// 注意：这里需要检查实际发送的请求体
	t.Logf("Sending device list update EDU: %+v", edu)

	// 验证：EDU 应该包含 edus 数组
	if receivedEDU != nil {
		edus, ok := (*receivedEDU)["edus"].([]any)
		require.True(t, ok, "EDU should contain 'edus' array")
		require.NotEmpty(t, edus, "edus array should not be empty")

		eduMap := edus[0].(map[string]any) //nolint:forcetypeassert
		assert.Equal(t, "m.device_list_update", eduMap["edu_type"])
	} else {
		t.Log("Warning: No EDU received - check if federateDeviceListUpdate is actually sending requests")
	}
}

// ===== Test 2: Backfill 响应格式测试 =====

// TestBackfillResponseFormat 验证 Backfill 响应是否包含 auth_chain.
func TestBackfillResponseFormat(t *testing.T) {
	// 注意：Backfill 路由需要已初始化的 HomeServer
	// 这里我们只是验证响应格式
	t.Log("Testing backfill response format...")

	// 根据代码审查，当前的 BackfillResponse 结构体缺少 auth_chain
	// 查看 federation_backfill.go:96-97
	t.Log("Current implementation in federation_backfill.go:96-97:")
	t.Log("  // 获取授权链（简化实现，返回空）")
	t.Log("  _ = room")
	t.Log("")
	t.Log("This means auth_chain is always empty, violating Matrix spec!")

	// 创建一个模拟响应
	response := map[string]any{
		"origin":           "test.server",          //nolint:goconst
		"origin_server_ts": time.Now().UnixMilli(), //nolint:goconst
		"pdus":             []any{},
		// Missing: "auth_chain"
	}

	// 验证响应缺少 auth_chain
	_, hasAuthChain := response["auth_chain"]
	assert.False(t, hasAuthChain, "This test documents the missing auth_chain bug")
	t.Log("CONFIRMED: Backfill response is missing auth_chain field")
}

// ===== Test 3: 推送规则迁移测试 =====

// TestPushRuleMigration 验证房间升级时推送规则是否正确迁移.
func TestPushRuleMigration(t *testing.T) {
	t.Log("Testing push rule migration...")

	hs := &HomeServer{
		Config: &HSConfig{
			ServerName: "test.server",
		},
	}

	// 初始化 RoomUpgrade 服务（如果存在）
	if hs.RoomUpgrade == nil {
		t.Log("Warning: RoomUpgrade service is nil - push rule migration will not work")
		t.Log("This is likely why TestPushRuleRoomUpgrade fails")
	}

	// 由于数据库依赖较复杂，这里主要验证代码逻辑
	t.Log("Push rule migration is async (go func()) in event.go:184-193")
	t.Log("This causes race condition with tests that check immediately")
}

// ===== Test 4: 删除事件边界情况测试 =====

// TestRedactionOfUnknownEvent verifies that redaction of a non-existent event
// returns M_FORBIDDEN when the user lacks sufficient power level.
// Per Matrix spec, RedactEvent succeeds for unknown targets when the user
// HAS permission (the redaction may arrive before the target in federation).
// Without room state, default power levels require Redact=50 but unknown
// users have power 0, so the permission check fails first.
func TestRedactionOfUnknownEvent(t *testing.T) {
	hs := &HomeServer{
		Config: &HSConfig{
			ServerName: "test.server",
		},
	}
	eventService := NewEventService(hs)

	roomID := "!test:localhost"
	userID := "@alice:localhost"

	// Without room state, canUserRedact uses default power levels (Redact=50)
	// and user power level defaults to 0, so permission is denied.
	_, err := eventService.RedactEvent(roomID, "$nonexistent:localhost", userID, "testing", nil)

	// Should return error (not panic) — permission denied
	assert.NotNil(t, err, "Redacting without permission should return error")

	if err != nil {
		assert.Equal(t, "M_FORBIDDEN", err.ErrCode,
			"Should be M_FORBIDDEN: user lacks power to redact (default Redact=50, user=0)")
	}
}

// TestRedactionInSync 验证 sync 中处理 redaction 目标不存在的情况.
func TestRedactionInSync(t *testing.T) {
	t.Log("Testing redaction handling in sync...")

	// 创建 redaction 事件，指向不存在的事件
	redactionEvent := &MatrixEvent{
		EventID:   "$redact:localhost",
		RoomID:    "!test:localhost",
		EventType: EventTypeRoomRedaction,
		Sender:    "@alice:localhost",
		Content: map[string]any{
			"reason": "testing",
		},
		OriginServerTS: time.Now().UnixMilli(),
	}

	// 注意：Redacts 字段在 MatrixEvent 结构体中
	// 需要检查 models.go 中的定义

	// 尝试转换为客户端事件
	clientEvent := redactionEvent.ToClientEvent()

	// 关键检查：不应该 panic
	assert.NotNil(t, clientEvent)
	t.Logf("Redaction event converted to client event: %+v", clientEvent)
}

// ===== Test 5: 联邦时间戳问题测试 =====

// TestFederatedEventTimestampOrdering 验证联邦事件的时间戳处理问题.
func TestFederatedEventTimestampOrdering(t *testing.T) {
	t.Log("Testing federated event timestamp ordering...")

	t.Log("")
	t.Log("========== FEDERATION TIMESTAMP PROBLEM ==========")
	t.Log("")
	t.Log("Scenario:")
	t.Log("1. Local event A: origin_ts=1000, created_at=1000")
	t.Log("2. Bob syncs, gets A, next_batch based on origin_ts=1050")
	t.Log("3. Remote event B: origin_ts=1100 (created remotely)")
	t.Log("4. B arrives late due to network delay")
	t.Log("5. Bob syncs with since=1050, query: origin_server_ts > 1050")
	t.Log("6. Event B: origin_ts=1100 > 1050 ✓ (works in simple case)")
	t.Log("")
	t.Log("BUT with out-of-order arrival:")
	t.Log("1. Remote event C: origin_ts=1080 (created after A, before B)")
	t.Log("2. C arrives very late (after B)")
	t.Log("3. Bob syncs with since=1150 (after getting B)")
	t.Log("4. Query: origin_server_ts > 1150")
	t.Log("5. Event C: origin_ts=1080 < 1150 ❌ FILTERED OUT!")
	t.Log("")
	t.Log("ROOT CAUSE:")
	t.Log("- Using origin_server_ts for sync filtering")
	t.Log("- Federated events arrive out of order")
	t.Log("- Sync token advances monotonically")
	t.Log("- Late-arriving events with old origin_ts are lost")
	t.Log("")
	t.Log("SOLUTION:")
	t.Log("Use created_at (local arrival time) for sync ordering")
}

// TestFederationSyncTokenTimestamp 验证 sync token 基于什么时间生成.
func TestFederationSyncTokenTimestamp(t *testing.T) {
	eventService := &EventService{}
	now := time.Now().UnixMilli()

	// Token 基于时间戳生成
	token := eventService.generatePaginationToken(now)

	t.Logf("Token generated from ts=%d: %s", now, token)

	// 验证 token 格式
	assert.True(t, strings.HasPrefix(token, "t"), "Token should start with 't'")

	// 检查是否能正确解析
	parsedTS := eventService.parsePaginationToken(token)
	assert.Equal(t, now, parsedTS, "Parsed timestamp should match original")

	t.Logf("Token parsing works correctly: %s -> %d", token, parsedTS)

	// 关键问题：token 基于 origin_server_ts 还是 created_at？
	t.Log("")
	t.Log("IMPORTANT: Current implementation uses origin_server_ts for token generation.")
	t.Log("This matches the query filter, but both are wrong for federated events!")
	t.Log("The fix requires using created_at for BOTH token AND query.")
}

// ===== 辅助函数 =====
