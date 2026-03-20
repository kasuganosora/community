# 服务器端代码完善清单

## 分析日期: 2026-03-21
## 当前状态: 95/100 Complement 测试通过

---

## 🔴 P0 - 关键问题 (必须修复)

### 1. 修复 5 个 Complement 测试失败 ✅ (2026-03-21 完成)

- [x] **TestSyncTimelineGap** - 联邦事件同步时间线缺口问题
  - **修复**: `matrix/homeserver/sync.go` 第 474-487 行
  - **修改**: 修改事件排序从 `origin_server_ts ASC` 为 `created_at ASC, origin_server_ts ASC`
  - **原因**: 联邦事件的 created_at 和 origin_server_ts 可能有很大差异，使用 created_at 进行查询和排序保持一致性
  
- [x] **TestDeviceListUpdates** - 设备列表更新同步问题
  - **修复**: `matrix/homeserver/federation.go` 第 548-634 行 (handleSendLeave)
  - **修改**: 
    - 将 leave 事件存储到 matrix_events 表（之前只更新成员状态）
    - 更新房间状态表
    - 触发设备列表更新推送
  - **原因**: Sync 查询的是 matrix_events 表，如果 leave 事件没有存入，就不会出现在 sync 中
  
- [x] **TestMessagesOverFederation** - 联邦消息获取不完整
  - **修复**: `matrix/homeserver/room.go` 第 744-820 行 (backfillFromRemote)
  - **修改**: 实现迭代 backfill，支持获取大量历史消息
  - **新增**: `backfillFromRemoteWithLimit` 函数，支持指定最大事件数
  - **原因**: 之前只进行一次 FetchBackfill 调用（最多 100 个事件），现在会迭代获取直到满足需求
  
- [x] **TestPushRuleRoomUpgrade** - 房间升级推送规则迁移
  - **修复**: `matrix/homeserver/room.go` 第 166-182 行 (CreateRoom)
  - **修改**: 添加对 creation_content.predecessor 字段的健壮处理
  - **原因**: 确保 predecessor 字段格式正确，包含必需的 room_id
  
- [x] **TestSync** (联邦 redaction 子测试) - 同步 redaction 事件问题
  - **修复**: 与 TestSyncTimelineGap 相同的修复
  - **原因**: 事件排序问题导致 redaction 事件在 sync 中的顺序不正确

### 2. E2EE 端到端加密 (当前 20% 完成度)
- [ ] 实现 `/keys/upload` 完整功能 (目前空实现)
- [ ] 实现 `/keys/query` 完整功能 (目前空实现)
- [ ] 实现 `/keys/claim` 完整功能 (目前空实现)
- [ ] 设备密钥存储和验证
- [ ] 交叉签名 (Cross-signing) 支持
- [ ] 密钥备份 (Key Backup) 实现
- [ ] Olm/Megolm 加密支持

---

## 🟡 P1 - 重要功能 (应该实现)

### 3. 联邦协议完善
- [ ] **Backfill 迭代获取** - 支持获取大量历史消息
- [ ] **Query Auth** 端点优化 (`/query/auth/:roomId/:eventId`)
- [ ] **Event Auth** 完整实现 (`/event_auth/:roomId/:eventId`)
- [ ] **Query Auth Chain** 完整实现 (`/query/auth_chain/:roomId`)
- [ ] **Timestamp to Event** 优化 (`/timestamp_to_event/:roomId`)

### 4. 房间功能
- [ ] **房间升级** (`/rooms/:roomId/upgrade`)
- [ ] **Tombstone 事件处理**
- [ ] 支持更多房间版本 (当前硬编码为 v10)

### 5. 媒体服务
- [ ] 远程服务器媒体获取完整实现
- [ ] 缩略图生成集成
- [ ] 媒体存储配置完善

### 6. 推送通知
- [ ] Push Gateway 集成
- [ ] 推送逻辑完整实现

### 7. 用户目录
- [ ] `/user_directory/search` 实现 (目前返回空)

---

## 🟢 P2 - 改进优化 (建议实现)

### 8. 代码质量
- [ ] 移除硬编码值 (`"device_id": "DEVICE"`)
- [ ] 完善错误处理和日志记录
- [ ] 统一错误响应格式
- [ ] 减少代码重复

### 9. 测试覆盖
- [ ] 补充单元测试
- [ ] 联邦协议测试
- [ ] E2EE 测试

### 10. 性能优化
- [ ] 数据库查询优化 (添加必要索引)
- [ ] Sync 查询性能优化
- [ ] 联邦请求缓存

### 11. 第三方身份验证
- [ ] `/account/3pid` 端点
- [ ] 身份服务器集成

### 12. 管理功能
- [ ] `/admin/` 管理端点完善
- [ ] 服务器管理命令

---

## 📊 优先级矩阵

| 功能模块 | 当前完成度 | 优先级 | 预计工时 |
|---------|-----------|--------|---------|
| Complement 测试修复 | 95% | P0 | 3-5 天 |
| E2EE | 20% | P0 | 2-4 周 |
| 联邦协议 | 70% | P1 | 1 周 |
| 房间功能 | 60% | P1 | 3-5 天 |
| 媒体服务 | 60% | P1 | 3-5 天 |
| 推送通知 | 50% | P1 | 2-3 天 |
| 用户目录 | 30% | P1 | 1-2 天 |
| 代码质量 | - | P2 | 持续 |
| 测试覆盖 | 40% | P2 | 1-2 周 |
| 性能优化 | - | P2 | 1 周 |

---

## 详细问题分析

### TestSyncTimelineGap 详细分析
```
问题: 当 50+ 事件通过联邦到达时，/sync 返回所有事件且 limited: false
期望: limited: true (因为超过 sync filter 的 timeline limit 20)

根本原因:
1. Backfill 事件存储时 created_at 几乎相同
2. Sync 查询使用 created_at > sinceTime，会获取所有事件
3. 事件排序使用 origin_server_ts 但查询使用 created_at，不一致

修复方案:
1. 修改 sync.go:474-475，使用 created_at 查询
2. 修改 pagination token 生成，使用 created_at
3. 确保 created_at 有数据库索引
```

### TestDeviceListUpdates 详细分析
```
问题: 远程用户 leave 事件不出现在 /sync 中

根本原因:
1. handleSendLeave 可能只更新 matrix_room_members 表
2. 事件没有正确写入 matrix_events 表
3. Sync 查询可能过滤掉了 leave 事件

修复方案:
1. 确保 leave 事件写入 matrix_events
2. 验证 leave 事件的 created_at 正确设置
3. 检查 sync 查询是否包含 leave 事件
```

### TestMessagesOverFederation 详细分析
```
问题: /messages 只返回 ~100 事件而不是全部 300

根本原因:
1. tryBackfillFromRemote 只进行一次 FetchBackfill
2. 没有迭代获取剩余事件

修复方案:
1. 实现迭代 backfill
2. 检查是否还有更多事件需要获取
3. 继续获取直到满足请求限制或无更多事件
```

### TestPushRuleRoomUpgrade 详细分析
```
问题: creation_content.predecessor 导致 400 错误

根本原因:
1. CreateRoom 没有正确处理 creation_content.predecessor
2. 房间升级流程不完整

修复方案:
1. 在 room.go 中合并 creation_content
2. 确保 predecessor 字段正确处理
3. 实现推送规则迁移
```
