# Matrix 房间版本

Community 支持稳定房间版本 1–12。客户端可以在 `POST /_matrix/client/v3/createRoom` 中指定 `"room_version": "12"`；联邦 `make_join` 协商也包含版本 12。`/capabilities` 公布支持的版本。

没有指定版本时，创建房间仍沿用现有默认版本 10。已有房间不自动升级；版本决定事件身份与授权规则，不能直接修改数据库中的版本号。

## 版本 12

- 房间 ID 从签名后的创建事件派生，不含服务器域名；创建事件的原始联邦 JSON 不带 `room_id`。
- 创建者及创建事件列出的 `additional_creators` 拥有隐式最高权限，不能在 `m.room.power_levels.users` 中为其指定权限值。旧版本不获得这些特权。
- 撤回目标放在 `content.redacts` 中，收发双方均执行对应版本的授权规则。
- 事件验签、认证链与状态冲突解析使用版本感知的 gomatrixserverlib 实现；v12 使用 State Resolution v2.1。
- 加入没有域名后缀的房间时，使用房间别名或提供 `via` 服务器提示，不能从房间 ID 推断目标服务器。

回归覆盖创建事件身份、远端状态导入、事件重放、创建者和普通成员权限、撤回，以及分叉合并后封禁状态的有效性。公开实例仍可能因邀请规则、封禁或连接失败拒绝加入；支持房间版本不绕过这些限制。

## 协议库补丁

当前锁定的 gomatrixserverlib 上游提交在 v12 状态冲突处理中缺少创建者初始化，且授权回退选错事件。`go.mod` 临时引用仓库内 `third_party/gomatrixserverlib`；来源、最小补丁和恢复上游依赖的条件见其 `COMMUNITY_PATCHES.md`。保留原许可证及测试。
