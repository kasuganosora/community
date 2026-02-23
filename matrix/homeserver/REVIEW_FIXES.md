# Federation Fixes Review

## 修复状态总结

### ✅ 已修复

| 问题 | 文件 | 行号 | 修复内容 |
|------|------|------|----------|
| **Sync 时间戳过滤** | sync.go | 476 | `created_at > ?` 替代 `origin_server_ts > ?` |
| **Device List EDU** | device_list.go | 192-213 | 使用 `fedClient.SendTransaction` 正确发送 EDU |
| **Backfill auth_chain** | federation_backfill.go | 95-102 | 添加 `buildRoomAuthChain` 和 `auth_chain` 响应 |
| **推送规则迁移** | event.go | 184-191 | 移除 `go func()`，改为同步执行 |

### ⚠️ 仍有问题

#### 1. Sync Token 生成不一致 (关键！)

**问题**: 查询使用 `created_at`，但 token 生成仍使用 `OriginServerTS`

**位置**:
- sync.go:464 - `generatePaginationToken(events[0].OriginServerTS)`
- sync.go:481 - `generatePaginationToken(events[0].OriginServerTS)`
- sync.go:499 - `generatePaginationToken(events[len(events)-1].OriginServerTS)`

**风险**: 
```
场景:
1. 事件 A: created_at=1000, origin_ts=1000
2. 联邦事件 B: created_at=1200, origin_ts=1100 (延迟到达)
3. 查询 created_at > 1000 => 返回 B
4. Token 基于 B.origin_ts=1100 生成 => "t1150"
5. 新联邦事件 C: created_at=1300, origin_ts=1050 (更延迟)
6. 查询 created_at > 1150 => ???

问题: Token "t1150" 被客户端保存，下次用 1150 作为 since
      但 C 的 created_at=1300 > 1150，应该被返回
      这实际上可能工作...

但反向场景:
1. 事件 A: created_at=1000, origin_ts=1000
2. 联邦事件 B: created_at=1100, origin_ts=1200 (时钟偏移)
3. 查询 created_at > 1000 => 返回 B
4. Token 基于 B.origin_ts=1200 生成 => "t1250"
5. 新事件 C: created_at=1150, origin_ts=1150 (正常到达)
6. 查询 created_at > 1250 => C 被过滤！
   但 C.created_at=1150 < 1250，而 C 实际上应该在 B 之前被处理
```

**建议修复**:
```go
// 第464行
tokenTS := events[0].CreatedAt.UnixMilli()
room.Timeline.PrevBatch = s.generatePaginationToken(tokenTS)

// 第481行
tokenTS := events[0].CreatedAt.UnixMilli()
room.Timeline.PrevBatch = s.generatePaginationToken(tokenTS)

// 第499行
tokenTS := events[len(events)-1].CreatedAt.UnixMilli()
room.Timeline.PrevBatch = s.generatePaginationToken(tokenTS)
```

### ❓ 待确认

#### 2. Redaction 边界情况

**位置**: event.go:641-683

需要确认是否已添加对 redaction 目标不存在时的优雅处理。

### 🔍 代码审查详情

#### Sync 修复 (sync.go:472-477)
```go
// 增量同步：返回 since 之后的所有事件（按时间升序）
// Use created_at (local receive time) for the "since" filter to correctly
// capture federation events whose origin_server_ts may predate our sync token
sinceTime := time.UnixMilli(since)
db.GetDB().Where("room_id = ? AND created_at > ?", roomID, sinceTime).
    Order("origin_server_ts ASC").Limit(timelineLimit + 1).Find(&events)
```
✅ 正确修复了查询过滤

#### Device List 修复 (device_list.go:192-213)
```go
// 之前 (有问题的代码):
// req, _ := http.NewRequest("PUT", url, nil)
// req.Body = io.NopCloser(bytes.NewReader([]byte{}))
// req.ContentLength = int64(len(payloadData))

// 现在 (修复后的代码):
if err := fedClient.SendTransaction(server, txnID, nil, []json.RawMessage{eduBytes}); err != nil {
    log.Error("Failed to send device list update to %s: %v", server, err)
    return
}
```
✅ 正确修复了 EDU 发送

#### Backfill 修复 (federation_backfill.go:94-103)
```go
// Build auth chain from room state events
authChain := buildRoomAuthChain(roomID)
_ = room

c.JSON(http.StatusOK, gin.H{
    "origin":           hs.Config.ServerName,
    "origin_server_ts": time.Now().UnixMilli(),
    "pdus":             pdus,
    "auth_chain":       authChain,  // ✅ 新增
})
```
✅ 正确添加了 auth_chain

#### Push Rule 修复 (event.go:184-191)
```go
// Handle tombstone: migrate push rules to replacement room (synchronous to avoid race)
if eventType == "m.room.tombstone" && stateKey != nil {
    if replacementRoom, ok := content["replacement_room"].(string); ok && replacementRoom != "" {
        if e.HS.RoomUpgrade != nil {
            e.HS.RoomUpgrade.migratePushRules(roomID, replacementRoom)  // ✅ 同步执行
        }
    }
}
```
✅ 正确改为同步执行

## 建议的额外修复

### 1. 统一 Token 生成

修改 sync.go 中所有 `generatePaginationToken` 调用，使用 `CreatedAt`:

```go
// 找到所有使用 OriginServerTS 的地方，改为 CreatedAt
room.Timeline.PrevBatch = s.generatePaginationToken(events[0].CreatedAt.UnixMilli())
```

### 2. 确保 CreatedAt 有索引

检查 models.go:
```go
type MatrixEvent struct {
    // ...
    CreatedAt *time.Time `gorm:"index;autoCreateTime"`  // 确保有 index 标签
}
```

### 3. Redaction 处理

检查 event.go:641-683 是否已添加:
```go
// 优雅处理 redaction 目标不存在
var targetEvent MatrixEvent
if err := db.GetDB().Where("event_id = ?", redactedEventID).First(&targetEvent).Error; err != nil {
    // 目标不存在，返回特定错误而不是 panic
    return nil, ErrNotFound
}
```

## 测试验证

运行测试确认修复:
```bash
go test -v ./matrix/homeserver/ -run "TestFederationTimestampProblem"
go test -v ./matrix/homeserver/ -run "TestFederatedEventsOutOfOrder"
```

## 总结

- ✅ 4/5 个主要问题已修复
- ⚠️ 1 个关键问题仍需修复 (token 生成不一致)
- 🔧 建议统一使用 `created_at` 进行所有 sync 相关的时间戳操作
