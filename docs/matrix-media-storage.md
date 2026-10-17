# Matrix 附件存储与回收

## 来源与代理

新写入的 Matrix 文件使用独立的来源目录；本地磁盘位于 `uploads/` 下，对象存储使用相同的相对 key。

| 用途 | 存储位置 / 行为 |
| --- | --- |
| 本站上传（含异步上传） | `matrix/local/YYYY/MM/DD/…` |
| 本站账号的 Matrix 头像转换结果 | `matrix/local/bridge-avatars/…` |
| 外站账号的头像转换结果 | `matrix/remote/bridge-avatars/…` |
| 必须导入的远端文件、链接预览图 | `matrix/remote/YYYY/MM/DD/…` |
| 外站 Matrix 附件、缩略图 | 验证访问权限后流式代理，不写入磁盘或对象存储 |
| ActivityPub → Matrix 私信附件 | 保存私有 MXC 与来源 URL 的映射，读取时流式代理，不保存附件副本 |
| Matrix → ActivityPub 附件 | 必须向 AP 接收者提供可读取的附件时，保存一份 `media/local/matrix-direct/…` 或 `media/remote/matrix-direct/…`；不再写持久化中间副本 |

本地 / 外站来源标记不改变 MXC 地址或访问权限。来源 URL 不进入客户端 DTO，代理沿用 DNS 校验、HTTPS、大小上限和超时限制，不转发用户 Cookie 或登录凭证。源站不可用时代理读取失败，不会偷偷改为永久缓存。头像规范化转换和跨协议投递所需副本是落盘例外。

旧文件继续从原路径读取，不自动搬迁，不根据文件年龄猜测来源。已有 Community 附件与 Matrix 共用文件时，Matrix 不接管该文件的删除职责。

## 自动回收

启动 Matrix 或网页群组路由后，每 5 分钟执行一次有 1 分钟总时限的维护任务：

- 过期的未发送网页群组上传：租约 24 小时，到期后检查引用并回收。
- 已发送网页群组上传、头像转换结果、显式远端导入和 AP 代理映射：只有受管理的明文记录才参与自动回收。首次确认不再被引用后等待 7 天，再检查引用并删除原图和缩略图。
- 异步上传：清除过期预留、失败记录、已完成的辅助记录和已失效的处理租约；有效的处理租约不会被删除，清除完成记录不删除其附件。

检查范围包括当前事件内容、房间状态、成员头像、房间头像、账号头像/封面、上传租约和其他文件记录。撤回前的签名快照不作为有效引用；同一附件被其他消息引用时仍保留。退出房间本身不触发删除。

原生 Matrix 上传可能用于加密事件，服务器不能可靠枚举其引用，因此不自动回收此类上传，也不批量清理来源不明确的旧记录。尚存的旧网页上传租约可证明其明文来源，过期时会将对应媒体纳入检查，但不会修改旧目录或猜测来源标记。用户有加密消息历史或处于加密房间时，其受管理上传也保守保留。

清理与新消息/头像发布通过数据库行锁协调。存储失败保留待删除记录，后续维护重试。成功后保留不可复用的 MXC 标记，防止并发发送重新引用已删除文件；仍接受远端合法历史事件中的过期 MXC，不因此中断联邦历史导入。每批检查最多 32 条，已检查记录延后调度，避免前排有效文件阻塞后续候选。

## 手动检查与分来源规则

默认只预览，不修改数据库或文件：

```sh
tootctl matrix media-gc
tootctl matrix media-gc --origin remote
tootctl matrix media-gc --origin local --limit 128
```

确认执行，并可按来源指定无引用宽限期（至少 24 小时）：

```sh
tootctl matrix media-gc --origin remote --grace 168h --confirm
```

该命令不是“删除这个目录下超过一定年龄的文件”。仍有有效引用的文件不会因超过宽限期被删；不要直接给这些目录配置不检查引用的对象存储过期规则。

数据库中没有记录的残留文件（例如进程在写入存储后、提交记录前崩溃），继续由现有 `tootctl media remove-orphans` 处理。已支持 `matrix/local/`、`matrix/remote/`，以及旧桥接中间目录 `matrix-media/bridge-remote/`。先预览，确认后才加 `--confirm`；旧目录应在旧版本进程停止后清理：

```sh
tootctl media remove-orphans --prefix matrix/remote/
tootctl media remove-orphans --prefix matrix-media/bridge-remote/
```

Matrix → AP 最终副本受 Community 媒体生命周期管理；无数据库引用的失败投递副本同样由 `remove-orphans` 检查。这里不对有效的 AP 帖子附件进行额外过期删除。
