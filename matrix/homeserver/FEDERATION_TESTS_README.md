# Federation Issues Test Suite

这个测试套件专门用于诊断和验证 5 个失败的联邦测试问题。

## 测试文件说明

| 文件 | 用途 |
|------|------|
| `federation_issues_test.go` | 5 个失败测试的基本验证 |
| `federation_timestamp_test.go` | 联邦时间戳问题的深度测试 |
| `federation_edu_backfill_test.go` | EDU 和 Backfill 格式验证 |
| `diagnose_federation_issues.go` | 可独立运行的诊断程序 |

## 快速诊断

### 方法 1: 运行诊断程序（无需数据库）

```bash
cd d:\code\community
go run matrix/homeserver/diagnose_federation_issues.go
```

这会输出所有 5 个问题的详细分析和修复建议。

### 方法 2: 运行单元测试

```bash
# 运行所有联邦问题测试
cd d:\code\community
go test -v ./matrix/homeserver/ -run "Federation|DeviceList|Backfill|Timestamp|PushRule|Redaction"

# 只运行时间戳问题测试（最关键）
go test -v ./matrix/homeserver/ -run "TestScenario_FederatedEventArrivesLate"

# 只运行设备列表测试
go test -v ./matrix/homeserver/ -run "TestDeviceListRequestBody"
```

## 5 个失败测试的分析

### 1. TestDeviceListUpdates

**根本原因**: `device_list.go:203-231` HTTP 请求体为空

**验证测试**: `TestDeviceListRequestBody`

**关键发现**:
```go
// 问题代码
req, _ := http.NewRequest("PUT", url, nil)  // Body 是 nil
req.Body = io.NopCloser(bytes.NewReader([]byte{}))  // 错误！
```

**修复**: 使用 `bytes.NewReader(payloadData)` 创建请求体

---

### 2. TestMessagesOverFederation

**根本原因**: `federation_backfill.go:96-97` 返回空的 auth_chain

**验证测试**: `TestBackfillResponseHasAuthChain`

**关键发现**:
```go
// 问题代码
_ = room  // auth_chain 被忽略！

// 响应缺少 auth_chain
return BackfillResponse{
    PDUs: pdus,
    // Missing: AuthChain
}
```

**修复**: 查询并返回每个 PDU 的 auth_events

---

### 3. TestPushRuleRoomUpgrade

**根本原因**: `event.go:184-193` 推送规则迁移是异步的

**验证测试**: `TestPushRuleMigrationSync`

**关键发现**:
```go
// 问题代码
go func() {  // 异步执行
    e.HS.RoomUpgrade.migratePushRules(...)
}()
```

**修复**: 移除 `go` 关键字使迁移同步，或添加完成通知机制

---

### 4. TestSync (redaction subtest)

**根本原因**: Redaction 目标不存在时的错误处理

**验证测试**: `TestRedactionOfUnknownEvent`, `TestRedactionInSync`

**关键发现**:
- `RedactEvent` 在目标不存在时返回错误
- Sync 处理 redaction 时可能 panic

**修复**: 优雅处理缺失的 redaction 目标

---

### 5. TestSyncTimelineGap ⭐ 最重要

**根本原因**: Sync 使用 `origin_server_ts` 过滤，但联邦事件可能延迟到达

**验证测试**: `TestScenario_FederatedEventArrivesLate`, `TestScenario_MultipleFederatedEvents`

**关键发现**:

```
时间线示例:

T+0:   本地创建事件 A (origin_ts=1000)
T+50:  Bob syncs, token=t1050
T+100: 远程创建事件 B (origin_ts=1100)
T+150: Bob syncs, 只收到 A, token=t1150 (基于 A)
T+200: 事件 B 延迟到达 (origin_ts=1100, created_at=1200)
T+250: Bob syncs (since=1150)
       查询: origin_server_ts > 1150
       事件 B: 1100 > 1150 = false ❌ 被过滤！
```

**修复**: 同时改用 `created_at` 进行过滤和 token 生成

```go
// sync.go:474-475
// 从：
db.Where("room_id = ? AND origin_server_ts > ?", roomID, since)

// 改为：
db.Where("room_id = ? AND created_at > ?", roomID, time.UnixMilli(since))
```

**注意**: 需要确保 `created_at` 字段有索引！

```go
// models.go 中添加索引
type MatrixEvent struct {
    // ...
    CreatedAt *time.Time `gorm:"index"`  // 添加索引
}
```

---

## 修复优先级

| 优先级 | 测试 | 复杂度 | 影响 |
|--------|------|--------|------|
| 🔴 P0 | TestSyncTimelineGap | 中 | 联邦同步完整性 |
| 🔴 P0 | TestDeviceListUpdates | 低 | 设备列表同步 |
| 🟡 P1 | TestMessagesOverFederation | 中 | 历史消息获取 |
| 🟡 P1 | TestPushRuleRoomUpgrade | 中 | 房间升级体验 |
| 🟢 P2 | TestSync | 低 | 边缘情况容错 |

## 调试技巧

### 查看实际 SQL 查询

在测试中添加 GORM 日志：

```go
func setupTestDB(t *testing.T) {
    db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),  // 打印 SQL
    })
    // ...
}
```

### 检查索引使用情况

```go
// 在测试中添加
var explain []map[string]interface{}
db.Raw("EXPLAIN QUERY PLAN SELECT * FROM matrix_events WHERE created_at > ?", 
    time.Now()).Scan(&explain)
t.Logf("Query plan: %+v", explain)
```

### 模拟联邦延迟

```go
// 在测试中模拟延迟到达
event := &MatrixEvent{
    OriginServerTS: time.Now().UnixMilli() - 5000,  // 5秒前创建
    CreatedAt:      time.Now(),                      // 刚刚到达
}
```

## 相关代码位置速查

| 问题 | 文件 | 行号 |
|------|------|------|
| 设备列表 EDU | device_list.go | 192-245 |
| Backfill auth_chain | federation_backfill.go | 45-104 |
| 推送规则迁移 | event.go | 184-193 |
| Redaction 处理 | event.go | 641-683 |
| Sync 时间戳过滤 | sync.go | 474-475 |
| Token 生成 | event.go | 822-828 |
| 事件模型 | models.go | 48-74 |

## 运行完整测试

```bash
# 运行所有联邦相关测试
go test -v ./matrix/homeserver/ -run "Federation|DeviceList|Backfill|Timestamp|PushRule|Redaction|Scenario" 2>&1 | tee federation_test.log

# 分析结果
grep -E "PASS|FAIL|PROBLEM|CONFIRMED|SOLUTION" federation_test.log
```
