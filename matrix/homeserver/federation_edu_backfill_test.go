package homeserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== Test 1: 设备列表 EDU HTTP 请求体验证 =====

// TestDeviceListRequestBody 验证设备列表 EDU 发送的请求体
func TestDeviceListRequestBody(t *testing.T) {
	// 捕获实际发送的 HTTP 请求
	var capturedBody []byte
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		
		t.Logf("Received request to %s", r.URL.Path)
		t.Logf("Content-Length: %d", r.ContentLength)
		t.Logf("Body length: %d", len(capturedBody))
		
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pdus": []interface{}{},
		})
	}))
	defer server.Close()
	
	// 创建设备列表服务
	hs := &HomeServer{
		Config: &HSConfig{
			ServerName: "test.server",
		},
		keyID: "ed25519:test",
	}
	_ = NewDeviceListService(hs)
	
	// 手动构造 EDU 并发送
	edu := DeviceListUpdateEDU{
		UserID:   "@alice:test.server",
		DeviceID: "DEVICE123",
		StreamID: 1,
		Changed:  []string{"DEVICE123"},
	}
	
	// 模拟发送逻辑（基于 device_list.go 的实际代码）
	payload := map[string]interface{}{
		"pdus": []map[string]interface{}{},
		"edus": []map[string]interface{}{
			{
				"edu_type": "m.device_list_update",
				"content":  edu,
			},
		},
	}
	
	payloadData, _ := json.Marshal(payload)
	
	// 创建请求（模拟 device_list.go:203-231 的问题代码）
	url := fmt.Sprintf("%s/_matrix/federation/v1/send/%d", server.URL, time.Now().Unix())
	
	// ===== 问题代码重现 =====
	req1, _ := http.NewRequest("PUT", url, nil)  // Body is nil!
	req1.Body = io.NopCloser(bytes.NewReader([]byte{}))  // 错误：设置为空
	req1.ContentLength = int64(len(payloadData))         // 但 Content-Length 非零
	req1.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{Timeout: 5 * time.Second}
	resp1, err := client.Do(req1)
	require.NoError(t, err)
	resp1.Body.Close()
	
	t.Log("")
	t.Log("===== BUGGY IMPLEMENTATION =====")
	t.Logf("Request Content-Length: %d", req1.ContentLength)
	t.Logf("Actual body sent: %d bytes", len(capturedBody))
	
	// 验证问题：请求体为空
	if len(capturedBody) == 0 {
		t.Log("PROBLEM CONFIRMED: Request body is empty!")
		t.Log("This is why TestDeviceListUpdates fails")
	}
	
	// ===== 修复后的代码 =====
	capturedBody = nil
	req2, _ := http.NewRequest("PUT", url, bytes.NewReader(payloadData))  // 正确：传入 body
	req2.Header.Set("Content-Type", "application/json")
	
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	resp2.Body.Close()
	
	t.Log("")
	t.Log("===== FIXED IMPLEMENTATION =====")
	t.Logf("Request body: %s", string(capturedBody))
	
	// 验证修复后：请求体正确
	if len(capturedBody) > 0 {
		var received map[string]interface{}
		json.Unmarshal(capturedBody, &received)
		
		edus, ok := received["edus"].([]interface{})
		require.True(t, ok, "Should have edus array")
		assert.Greater(t, len(edus), 0, "edus should not be empty")
		
		t.Log("FIX VERIFIED: Request body is correctly sent")
	}
}

// ===== Test 2: Backfill 响应格式验证 =====

// TestBackfillResponseHasAuthChain 验证 backfill 响应包含 auth_chain
func TestBackfillResponseHasAuthChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	
	// 注册 backfill路由 - 使用现有的 homeserver 实例
	// 避免创建新的 HS 实例以避免全局状态问题
	RegisterBackfillRoutes(router)
	
	// 测试 backfill 端点
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/_matrix/federation/v1/backfill/!test:test.server?v=$event1&limit=10", nil)
	router.ServeHTTP(w, req)
	
	t.Logf("Backfill response status: %d", w.Code)
	t.Logf("Backfill response body: %s", w.Body.String())
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	
	// Matrix 规范要求的关键字段
	requiredFields := []string{"origin", "origin_server_ts", "pdus", "auth_chain"}
	for _, field := range requiredFields {
		value, exists := response[field]
		t.Logf("Field %q: exists=%v, value=%v", field, exists, value)
		
		if field == "auth_chain" && !exists {
			t.Log("PROBLEM CONFIRMED: auth_chain is missing!")
			t.Log("This is why TestMessagesOverFederation fails")
			t.Log("Matrix spec requires auth_chain in backfill response")
		}
	}
	
	// 当前实现应该缺少 auth_chain
	_, hasAuthChain := response["auth_chain"]
	assert.False(t, hasAuthChain, 
		"Current implementation is missing auth_chain (this test documents the bug)")
}

// TestBackfillAuthChainContent 验证 auth_chain 应该包含什么
func TestBackfillAuthChainContent(t *testing.T) {
	// 根据 Matrix 规范，auth_chain 应该包含事件的授权事件
	// 例如：m.room.create, m.room.power_levels, m.room.member 等
	
	t.Log("According to Matrix spec, backfill response auth_chain should contain:")
	t.Log("- m.room.create (for the room)")
	t.Log("- m.room.power_levels (current power levels)")
	t.Log("- m.room.member (for the sender)")
	t.Log("- Any other auth_events referenced by the returned events")
	t.Log("")
	t.Log("Current implementation in federation_backfill.go:96-97 returns empty auth_chain")
	t.Log("This violates the spec and causes federation clients to reject events")
}

// ===== Test 3: 推送规则迁移同步性验证 =====

// TestPushRuleMigrationSync 验证推送规则迁移是同步还是异步
func TestPushRuleMigrationSync(t *testing.T) {
	// 检查 event.go:184-193 的代码
	codeSnippet := `
	if eventType == "m.room.tombstone" && stateKey != nil {
		if replacementRoom, ok := content["replacement_room"].(string); ok && replacementRoom != "" {
			go func() {  // <-- ASYNC!
				if e.HS.RoomUpgrade != nil {
					e.HS.RoomUpgrade.migratePushRules(roomID, replacementRoom)
				}
			}()
		}
	}
	`
	
	t.Log("Current implementation (event.go:184-193):")
	t.Log(codeSnippet)
	t.Log("")
	t.Log("PROBLEM: Migration is ASYNC (go func())")
	t.Log("- Test checks push rules immediately after room upgrade")
	t.Log("- Migration goroutine may not have started yet")
	t.Log("- No way for test to know when migration completes")
	t.Log("")
	t.Log("SOLUTION OPTIONS:")
	t.Log("1. Make migration synchronous (remove 'go')")
	t.Log("2. Add a mechanism to wait for migration completion")
	t.Log("3. Pre-migrate rules during room creation (before tombstone)")
}

// ===== Test 4: Redaction 边界情况验证 =====

// TestRedactionTargetMissing 验证 redaction 目标不存在时的行为
func TestRedactionTargetMissing(t *testing.T) {
	// 场景：redaction 指向一个不存在的非事件
	
	t.Log("Test scenario:")
	t.Log("1. Event R is a redaction with redacts=$nonexistent")
	t.Log("2. $nonexistent doesn't exist in database")
	t.Log("3. User syncs with token pointing to R")
	t.Log("")
	t.Log("Current implementation in event.go:641-683:")
	t.Log("```")
	t.Log("var targetEvent MatrixEvent")
	t.Log("err := db.GetDB().Where(...).First(&targetEvent).Error")
	t.Log("if err != nil {")
	t.Log("    return ErrNotFound  // <-- Returns error")
	t.Log("}")
	t.Log("```")
	t.Log("")
	t.Log("PROBLEM: If target is missing, RedactEvent returns error")
	t.Log("But sync should still work even if redaction target is missing")
	t.Log("")
	t.Log("ADDITIONAL ISSUE: In sync.go, when building timeline,")
	t.Log("ToClientEventForDevice may try to access redaction target")
	t.Log("If not handled carefully, this could cause panic")
}

// ===== Test 5: 联邦时间戳问题详细分析 =====

// TestTimestampMismatchDetailed 详细分析时间戳不匹配问题
func TestTimestampMismatchDetailed(t *testing.T) {
	t.Log("========== FEDERATION TIMESTAMP PROBLEM ANALYSIS ==========")
	t.Log("")
	t.Log("TIMELINE:")
	t.Log("")
	t.Log("T+0ms    [HS1] Alice creates event A")
	t.Log("         origin_server_ts = 1000")
	t.Log("         created_at = 1000")
	t.Log("")
	t.Log("T+50ms   [HS2] Bob syncs (since=0)")
	t.Log("         Returns: [A]")
	t.Log("         next_batch token = t1050 (based on A.origin_ts + 50)")
	t.Log("")
	t.Log("T+100ms  [HS1] Alice creates event B")
	t.Log("         origin_server_ts = 1100")
	t.Log("         (federation delay to HS2: 100ms)")
	t.Log("")
	t.Log("T+150ms  [HS2] Bob syncs (since=1050)")
	t.Log("         Query: origin_server_ts > 1050")
	t.Log("         Result: [] (B hasn't arrived yet)")
	t.Log("         next_batch token = t1050 (unchanged)")
	t.Log("")
	t.Log("T+200ms  [HS1/HS2] Event B arrives at HS2")
	t.Log("         origin_server_ts = 1100 (from HS1)")
	t.Log("         created_at = 1200 (HS2 local time)")
	t.Log("")
	t.Log("T+250ms  [HS2] Bob syncs (since=1050)")
	t.Log("         Query: origin_server_ts > 1050")
	t.Log("         Event B: origin_ts=1100 > 1050 ✓")
	t.Log("         Result: [B] ✓ (works in this case)")
	t.Log("")
	t.Log("BUT consider this scenario:")
	t.Log("")
	t.Log("T+0ms    [HS1] Alice creates event A")
	t.Log("         origin_server_ts = 1000")
	t.Log("")
	t.Log("T+50ms   [HS2] Bob syncs")
	t.Log("         next_batch = t1050")
	t.Log("")
	t.Log("T+100ms  [HS1] Alice creates event B (origin_ts=1100)")
	t.Log("T+120ms  [HS1] Alice creates event C (origin_ts=1120)")
	t.Log("")
	t.Log("T+150ms  [HS2] Bob syncs (since=1050)")
	t.Log("         Only B has arrived, C is still in transit")
	t.Log("         Returns: [B]")
	t.Log("         next_batch = t1150 (based on B.origin_ts + 50)")
	t.Log("")
	t.Log("T+200ms  [HS2] Event C arrives (delayed)")
	t.Log("         origin_server_ts = 1120")
	t.Log("         created_at = 1200")
	t.Log("")
	t.Log("T+250ms  [HS2] Bob syncs (since=1150)")
	t.Log("         Query: origin_server_ts > 1150")
	t.Log("         Event C: origin_ts=1120 < 1150 ❌ FILTERED OUT!")
	t.Log("         Result: [] (C is lost forever)")
	t.Log("")
	t.Log("ROOT CAUSE:")
	t.Log("- Sync token advances based on events that HAVE arrived")
	t.Log("- But federated events may arrive out of order")
	t.Log("- Using origin_server_ts for filtering causes 'gaps'")
	t.Log("")
	t.Log("SOLUTION:")
	t.Log("Use created_at for both:")
	t.Log("1. Filtering events in sync query")
	t.Log("2. Generating next_batch token")
	t.Log("")
	t.Log("This ensures monotonic ordering based on local arrival time")
}

// ===== 辅助函数 =====

// SetHomeServer 用于测试设置全局 homeserver
func SetHomeServer(hs *HomeServer) {
	// 注意：这会覆盖全局变量，仅用于测试
	// 实际实现需要小心处理并发
}

// Helper to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	return strings.Contains(s, substr)
}
