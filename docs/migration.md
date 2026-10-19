# Misskey / Mastodon → Community 整站迁移（运维手册）

本文是同域名整站迁移的运维手册，总结了 **maid.lat 实战演练**（Misskey，4.57M 条笔记，其中本地用户 9536 个）中踩过的坑。
功能范围与权限模型见 [data-migration.md](data-migration.md)，导入后核对见 [migration-verify.md](migration-verify.md)。

## 推荐方式：直连模式（direct mode）

```sh
community-transfer --source misskey \
  --source-dsn "host=10.0.0.5 port=5432 dbname=misskey user=community_export" --sslmode require \
  --source-base-url https://maid.lat --id-method aidx --source-version 2025.4.0 \
  --object-storage-mode reference --object-base-url https://files.maid.lat \
  --work-dir /srv/maid-migration --apply --report /srv/maid-migration/report.json
```

`community-transfer` 只读地连接源 PostgreSQL，直接写入 Community 数据库，**不生成、也不加载 `bundle.json`**。
目标库由 Community 自身的环境变量（`.env` / `DB_*` / `APP_BASE_URL`）决定，与服务进程一致。

| 参数 | 说明 |
| --- | --- |
| `--source misskey\|mastodon` | 源平台 |
| `--source-dsn` | libpq 连接串。**不要写密码**，用 `PGPASSFILE` / `~/.pgpass` |
| `--sslmode` | DSN 中没有 `sslmode` 时追加（`disable` / `require` / `verify-full` …） |
| `--source-base-url` | 源站公开 origin，必须等于目标 `APP_BASE_URL` |
| `--id-method` | Misskey `config.id`。历史上切换过 ID 格式也没关系，每个 ID 单独识别（见下文） |
| `--work-dir` | 私有工作目录（0700）：读取检查点（`state/`）、spool（`spool.sqlite3`）、复制的媒体（`out-*/`）。`--apply` 成功后自动擦除其中含明文凭据的文件 |
| `--keep-secret-spool` | 调试用：`--apply` 成功后不擦除含明文凭据（含 TOTP secret）的 spool/state，并打印警告 |
| `--fetch-only` | 第一阶段：源站继续在线，批量读取到 `--work-dir` 后停止 |
| `--final-sync` | 第二阶段：源站停写后，补读新增/修改/删除的行，然后导入 |
| `--batch-size 5000` | 每页行数 = 每个只读事务的大小（keyset 分页） |
| `--max-rows-per-sec` / `--batch-sleep` | 对在线源站限速 |
| `--include-remote-cache` | 同时迁移远端内容缓存（默认只迁本地数据及其引用的远端实体） |
| `--chunk-size 1000` | 每个目标事务导入的记录数 |
| `--reuse-spool` | 不再读源库，直接用现有 spool 继续导入（断点续传） |
| `--apply` | 省略时只做预检（validate），不写业务数据 |

Docker 镜像已包含 `python3`、`psql` 和读取脚本：

```sh
docker compose exec community ./community-transfer --source misskey --source-dsn "..." ...
```

### 安全性

- 源库每个读取事务都带 `default_transaction_read_only=on`、`statement_timeout`、`lock_timeout=5s`，只发 `SELECT`，按主键 keyset 分页，不会长时间持有快照或锁。
- 仍然建议给迁移单独建只读角色，并在**隔离网络**里演练：

  ```sql
  CREATE ROLE community_export LOGIN PASSWORD '…';
  ALTER ROLE community_export SET default_transaction_read_only = on;
  GRANT CONNECT ON DATABASE misskey TO community_export;
  GRANT USAGE ON SCHEMA public TO community_export;
  GRANT SELECT ON ALL TABLES IN SCHEMA public TO community_export;
  ```

- 导入是历史数据导入：不会向联邦发送任何活动（`federation_outbox` 保持为空）。演练时目标实例仍使用 `APP_BASE_URL=https://maid.lat`，**务必阻断其出站网络**，否则启动后的正常后台任务可能以 maid.lat 的身份对外请求。

### 两阶段迁移（停机时间最短）

```sh
# 源站在线时，可多次运行；中断后原命令重跑即从检查点继续
community-transfer ... --work-dir /srv/maid-migration --fetch-only --max-rows-per-sec 20000

# 源站停写（维护模式）后
community-transfer ... --work-dir /srv/maid-migration --final-sync --apply --report report.json
```

`--final-sync` 读取 `bulk_started - 5 分钟` 以后更新的行，并删除源站已删除的行，然后导入。

### 断点续传与幂等

- 读源阶段：状态目录逐页检查点，中断后原命令重跑即可。
- 导入阶段：每个 chunk 一个事务。`data_transfer_receipts` 记录 *源 ID → 目标 ID*，所以 chunk 可以重复执行：已提交的记录只计入 `skipped`。中断后加 `--reuse-spool` 重跑，从 `checkpoint:<stage>` 继续。
- 对已完成的迁移再次执行也是安全的（全部 `skipped`）。注意：chunk 会把它引用的账号/媒体/表情一起带上（已提交的会被跳过），因此报告里的 `skipped` 数会大于实际行数，以 `created` 和数据库计数为准。

### 进度输出

stderr 每隔几秒输出一行，同样的数据驱动安装向导的进度条：

```
progress stage=fetch/note done=1250000/4570000 (27.4%) speed=18250/s eta=3m2s
progress stage=spool done=370000 speed=52000/s
progress stage=import/posts done=187340 total=412880 pct=45.4 speed=1312/s eta=2m52s
```

阶段顺序：`emojis → accounts → credentials → keys → media → posts → polls → relations → lists → reactions → bookmarks → poll_votes → records`。

### 实现选择（为什么不是纯 Go）

直连模式复用了 `scripts/migration/export_misskey.py` / `export_mastodon_db.py` 的读取层：它已经实现了源库 schema 映射、keyset 分页、检查点、`--final-sync` 和限速，并在 maid.lat 上验证过。新增的 `--emit ndjson` 让转换后的记录通过管道逐条流给 `community-transfer`，后者写入磁盘上的 SQLite spool，再分块导入。

- 读取状态目录（`state/staging.sqlite3`）就是 staging；没有任何环节把整个源库或整个 bundle 放进内存。
- 两种平台的映射只有一份实现（Python），向导和命令行共用，避免 Go/Python 两份映射逐渐不一致。
- 代价：需要 `python3` 和 `psql`（镜像里已有）。纯 Go 读取器可以以后替换 Python 那一段，spool/分块导入不受影响。

### 内存

| 场景 | 峰值 RSS |
| --- | --- |
| 旧路径：整份 `bundle.json` 读入内存（合成 250 MB bundle，374k 条记录） | 1.26 GiB |
| 新路径：同一 bundle 流式 spool + 分块导入（`--stream`，默认） | 81 MiB |
| 直连模式，同一合成源库（读取 + 转换 + 导入，4m08s；进程树中单个进程的最大值） | 111 MiB |

两种路径导入结果逐表一致（accounts / statuses / follows / media / receipts / preserved records）。流式路径慢约 1/3（3m54s vs 2m54s），换来内存不随实例规模增长。
复现：`psql -v remote_notes=200000 -v local_notes=100000 -f scripts/migration/testdata/synthetic_misskey.sql`，导出后分别用 `--stream=false` 和默认参数导入，并用 `/usr/bin/time -v` 记录。

### 已有 bundle 的导入

`community-transfer --scope site --input <export-dir>` 现在默认流式读取 `bundle.json`（`--stream`），不再受 `--max-json-bytes` 限制。zip 包、个人备份和 `--stream=false` 仍走旧的内存路径。

### 环境变量与 Redis

`community-transfer` 读取与实例相同的配置（`ENV_FILE` 指向的 `.env`，进程环境变量优先）：

| 变量 | 是否必需 | 说明 |
|---|---|---|
| `DB_CONNECTION` / `DB_DSN` | 必需 | 目标数据库 |
| `APP_BASE_URL` | 必需 | **导入期间必须等于源站 origin**（例如 `https://maid.lat`，含 `https://`，不带路径和末尾 `/`）。整站迁移保留原有 Actor / 帖子 URI，预检会以 `origin` 错误拒绝不一致的值；不要用演练机的临时域名或 `http://127.0.0.1` |
| `ENCRYPTED_KEY` | 必需 | 与目标实例相同的 32 字节密钥（导入的私钥等用它加密）；导入后不能再换 |
| `REDIS_0_HOST` / `REDIS_0_PORT`（默认 6379）/ `REDIS_0_PASSWORD` / `REDIS_0_DB`（默认 0） | 可选 | 目标实例的 Redis。只用于导入后清除过期的时间线缓存 |
| `REDIS_1_HOST` … | 可选 | 额外的 Redis 实例，导入不使用 |
| `REDIS_FEED_CLUSTER_ADDRS` | 可选 | 若实例的 feed 使用 Redis Cluster，填相同的值，缓存清理会走集群 |
| `COMMUNITY_DEFAULT_REDIS_HOST` | 可选 | `REDIS_0_HOST` 为空时的后备值（Docker 镜像使用） |
| `TRANSFER_DEBUG=1` | 可选 | 打印完整错误链和崩溃堆栈 |

**Redis 不是必需的。** 没有配置时，`--apply` 会打印一行提示并继续导入，只是跳过缓存清理；导入后重启实例或运行 `tootctl feeds build --all` 即可。演练用的空库不需要 Redis。如果某个代码路径仍然意外地需要 Redis，程序会给出明确错误（而不是 `panic: no redis config`），并提示设置上面的变量；请附上 `TRANSFER_DEBUG=1` 的输出报告。

## 二步验证（TOTP）与敏感文件

### 映射

Misskey `user_profile` 的二步验证会导入到 Community 自己的 TOTP（RFC 6238，SHA1，6 位，30 秒，±1 步）：

| Misskey | Community |
|---|---|
| `twoFactorEnabled = true` + `twoFactorSecret`（base32） | `user_two_factors.secret`，用 `ENCRYPTED_KEY` 加密保存；用户原来的验证器 App 直接可用 |
| `twoFactorBackupSecret`（明文列表） | `user_backup_codes`，bcrypt 哈希，每个只能用一次 |
| `securityKeysAvailable` | 不在 bundle 里：导入后运行 `tootctl passkeys import-misskey`（警告 `security_keys_separate`） |
| `twoFactorEnabled = false`（残留的 secret） | 忽略，账号不启用 2FA |

仍然报 `unsupported_auth`（阻断）的情况，消息开头是原因代码：

| 原因代码 | 含义 |
|---|---|
| `passwordless_without_totp` | 只用安全密钥无密码登录、没有 TOTP。Misskey 的密钥不保证是可发现的 passkey，导入后会退化成只有密码 |
| `invalid_totp_secret` | 启用了 2FA 但 secret 缺失或不是 base32 |
| `invalid_backup_codes` | 备用码不是字符串列表 |
| `unknown_credential_extension` | credential 里有导入器不认识的扩展字段 |
| `two_factor_without_adapter` | 只有"启用了 2FA"标记、没有可导入的 secret（例如 Mastodon：secret 需要 Rails 的加密密钥） |

`ENCRYPTED_KEY` 导入后不能再换，否则 TOTP secret 无法解密，这些用户将无法登录（只能用 `tootctl two-factor reset` 重置）。

### 脱敏

- TOTP secret 和备用码不会出现在日志、`report-*.json`、stdout 或错误消息里：报告只写原因代码；credential 的回执摘要基于脱敏后的值；导入器错误只说"two-factor secret could not be stored"。
- 私有归档（`data_transfer_preserved_records`）里的原始 `misskey:user_profile` 行在写入前把 `twoFactorSecret` / `twoFactorBackupSecret` / `twoFactorTempSecret` 替换为 `"[redacted]"`（回执摘要仍按原值计算，所以旧导入的重跑不会冲突）。
- **2FA 支持之前做过的导入**，归档里可能有明文 secret：运行 `tootctl two-factor scrub-preserved --dry-run` 查看数量，再去掉 `--dry-run` 执行（只改这些字段，可重复执行）。
- `export_misskey.py` 的错误输出同样会把 `twoFactor*Secret` 的值替换成 `***`。

### 工作目录与 spool

工作目录里的密钥材料全部由目标的 `ENCRYPTED_KEY` 派生的密钥加密（密钥只在环境变量里，不在工作目录里）：

- **spool（`spool.sqlite3*`）**：`credentials`（密码哈希、TOTP secret、备用码）、`keys`（本地 Actor 签名私钥）和 `records`（完整的源表行：`user_keypair`、`app` 的 OAuth secret、`access_token`、webhook secret、`meta` 里的对象存储 / SMTP / captcha / DeepL 等密钥……）三个分区整条记录用 AES-256-GCM 封装。旧版本留下的明文 spool（例如失败的演练，含 33 个本地私钥）在下次打开时原地加密，然后 `secure_delete` + `VACUUM` + 截断 WAL，不留旧明文页。
- **读取状态（`state/staging.sqlite3`）**：导出器需要用 `json_extract` 查询 id / 引用列，所以按字段加密：字段名像 `*secret*`、`*password*`、`privateKey`、`*authKey`、`*apiKey`、`*accessKey`、`smtpPass`、`token`、`hash`、`auth`、`code` 的非空字符串 / 数组 / 对象值替换成 `enc1:…`（SHAKE256 密钥流 + HMAC-SHA256，Python 标准库即可实现）。导出器写入时直接加密；`community-transfer` 每次启动时把旧版本留下的明文行原地加密并 `VACUUM`（包括 `--reuse-spool` 时）。
- `ENCRYPTED_KEY` 变了以后 `--reuse-spool` / 续传会报错，需要恢复原值或清空工作目录重新读取。没有设置 `ENCRYPTED_KEY` 时无法加密，程序会打印 `WARNING`。
- spool 中的私有原始记录（`records`）在写入前就把 `twoFactor*Secret` 替换为 `"[redacted]"`。
- **失败或中断时也会清理**（预检失败、导入出错、Ctrl-C/SIGTERM）：擦除 `out-*/.spool`、`out-*/bundle.json*`；spool 已完整时还会擦除 `state/`（之后用 `--reuse-spool` 从加密 spool 续跑；不带 `--reuse-spool` 则从头读取源库）。源库还没读完时 `state/` 是续传检查点，会保留并打印 `NOTE`，读完后的下一次失败或成功都会擦除它。`--fetch-only` 和 `--keep-secret-spool` 不做这一步。

- `--work-dir` 以 `0700` 创建（已存在时也会收紧为 `0700`），其中的文件为 `0600`（spool 在 SQLite 打开前以 `0600` 创建；导出器以 `umask 077` 运行；每个阶段后再收紧一次）。
- **`--apply` 成功后自动擦除**这些含密文件：先用零覆盖、`fsync`，再删除；复制的媒体（`out-*/` 下的其余文件）保留。之后如果还要 `--final-sync`，会从头读取源库（回执保证幂等，只是更慢）。两阶段迁移的正常顺序（`--fetch-only` → `--final-sync --apply`）不受影响。
- 调试时加 `--keep-secret-spool` 保留这些文件，程序会打印 `WARNING`；调试完请手动删除 `spool.sqlite3*`、`state/`、`out-*/.spool`。
- 流式导入现成 bundle 时，临时 spool 同样会被擦除；**作为输入的 `bundle.json` 不会被自动删除**，导入成功后会打印 `NOTICE` 提醒用 `shred -u` 之类的方式清除它和所有副本。
- 覆盖擦除是尽力而为：写时复制文件系统、SSD 磨损均衡和快照可能保留旧数据块，工作目录应放在加密磁盘上，并且不要放进会被备份/快照的路径。

### 运维命令

```sh
tootctl two-factor status alice        # 是否启用、剩余备用码
tootctl two-factor reset alice         # 关闭 2FA 并删除备用码（先核实身份）
tootctl two-factor scrub-preserved     # 清除旧导入归档里的明文 secret
```

管理后台「账号」详情页也有两步验证状态和「重置两步验证」按钮。

## maid.lat 演练得到的教训

1. **默认只迁本地数据。** maid.lat 有 4.57M 条笔记，绝大多数是远端缓存。默认只导出本地用户的数据和它们引用的远端实体；被裁掉的引用记为 `remote_reference_trimmed`（info）。只有确实需要远端缓存时才加 `--include-remote-cache`，内存和时间都会大很多。
2. **ID 格式会混用。** maid.lat 早期笔记是 10 位 `aid`，后来切换成 16 位 `aidx`；旧导出器按单一 `config.id` 解码，产生了 8739 条阻断性 `invalid_source_time`。现在每个 ID 单独识别（aid / aidx / meid / meidg / ulid / objectid，`--id-method` 只用于消除 meid/objectid 这类歧义），并要求解出的时间落在 2000 年至今之间。
3. **媒体尺寸可能是小数。** Misskey 的 `drive_file.properties` 里有 `width: 1665.517` 这种值，旧导入器因为 `int` 字段解析失败而拒绝整个 bundle。现在 `size` / `width` / `height` 接受小数和数字字符串并四舍五入。
4. **对象存储 URL 必须保持在线。** `--object-storage-mode reference` 不复制文件，导入后的附件、头像、表情仍指向原 bucket 的公开 URL。迁移完成后**不能删除 bucket、不能改它的域名或访问策略**；要换存储，先用 `copy` 模式（或之后用 `tootctl media` 迁移）把文件搬过来。
5. **不迁移的内容（用户需要知道）：**
   - 登录会话、OAuth 应用与访问令牌：所有客户端需要重新登录 / 重新授权。
   - TOTP 二步验证（MFA）：**会迁移**，见下文「二步验证（TOTP）与敏感文件」。
   - **通行密钥 / 安全密钥（passkeys / WebAuthn）：不迁移，用户必须在新站重新注册。** `community-transfer` 不导入任何安全密钥；迁移公告里要提前告知用户：迁移后先用密码（以及已迁移的 TOTP）登录，再到「设置 → 安全」重新添加安全密钥 / 通行密钥。只用安全密钥、没有 TOTP 的账号会在预检中报 `passwordless_without_totp`。预检对有安全密钥的账号给出 `security_keys_separate` 警告，可据此统计需要通知的用户。（实验性的 `tootctl passkeys import-misskey` 仍然存在，但不属于本手册的支持流程，不能保证导入的密钥可用；不要以它代替通知用户重新注册。）
   - **引用（quote）笔记**（maid.lat：317 条）和**频道笔记**（5 条）只进入私有归档（archive-only），不作为公开帖子发布。回复它们的笔记照常发布，但不带回复链（帖子属性 `import_reply_parent_archived` 记录原父帖 ID）；转发它们的笔记同样只归档。
   - **账号级可见性策略**：Misskey 的 `requireSigninToViewContents`（需要登录才能查看）会导入为 Community 的账号设置「需要登录才能查看我的帖子」（`accounts.require_signin_to_view`），不再阻断预检。开启后：未登录访客只能看到资料（名字、头像、简介），帖子相关 API 返回 401 `SIGNIN_REQUIRED`，时间线 / 标签 / 搜索 / 热门结果中剔除其帖子；未签名的 ActivityPub 拉取（帖子、outbox、followers/following/featured）返回 401，远端服务器的签名请求照常放行；Actor 文档和公钥保持公开，投递给关注者不变。`makeNotesFollowersOnlyBefore` / `makeNotesHiddenBefore`（把某个时间点之前的笔记自动改为仅关注者可见 / 隐藏；正数是 Unix 秒，负数是"距今 N 秒"）仍没有对应功能，设置了的本地账号在预检中报 `unsupported_privacy_policy`（阻断），报告写明字段和值，例如 `makeNotesHiddenBefore=-2592000`。
   - 使用目标站不存在的自定义表情（例如本地迁移范围之外的远端表情）的回应会归档，而不是让整个事务失败。
6. **核对脚本以前会误报通过。** `verify_misskey_import.py` 现在：空目标库直接 FAIL；逐个检查每个本地用户是否存在；实例 Actor 的公钥必须等于 Misskey `instance.actor` 的公钥；连接失败显示真实错误（不再是 `HTTP 0`）。
7. **旧导出器的隐藏错误。** 失败时会打印阶段、异常类型和 psql 的原始错误（密码已脱敏），`--verbose` 打印 traceback；psql 警告（例如 PGPASSFILE 权限不安全）也会显示。
8. **预检没发现列宽不够。** 第二次演练（224k 行、预检 0 错误）在真正导入自定义表情时失败：`value too long for type character varying(36)`——`emojis.shortcode` 是 `varchar(36)`，257 个远端表情名超长，最长 113（Misskey 允许 128）。现在：
   - 迁移 `ImportColumnWidthsScript` 加宽 `emojis.shortcode`→255、`statuses.origin_uri`/`origin_url`→2048、`media_attachments.remote_url`→2048、`media_attachments.content_type`→128、`accounts.username`→128、`accounts.acct`→320（PostgreSQL / MySQL / MariaDB；SQLite 不限制长度，无需改动；可重复执行）。`community-transfer` 启动时自动执行迁移。
   - 预检会把每个待导入字段的长度与**目标库实际的列宽**比较，超长时报阻断错误 `column_too_narrow`，每列一条，写明表.列、超长数量、最大长度、列宽和最多 5 个样例源 ID（`count` / `max_length` / `limit` / `sample_ids` 字段）。
9. **回复比父帖可见范围更大。** 第三次演练在帖子阶段失败：`reply widens parent audience`——本地「首页」（unlisted）回复挂在远端「仅关注者」帖子下。现在不再中止：回复的可见性收窄到父帖的级别（从不放宽；unlisted→仅关注者；父帖是私信时回复变成私信，收件人只保留父帖参与者并加上父帖作者），帖子属性 `import_visibility_narrowed_from` 记录原值。预检和导入报告里是一条非阻断警告 `reply_visibility_narrowed`（数量 + 最多 5 个样例源 ID），回复链会逐级收窄。站点导入里互相回复成环、永远无法发布的帖子也不再让整个导入失败，而是保留在私有归档里并给出 `archived_parent` 警告。

10. **私有归档记录的源 ID 重复。** 第三次演练在 preserved 阶段失败：`import conflict for archive:<misskey:poll> source identifier`。导出器用 `id or userId or noteId` 拼原始行的 ID，而 Misskey 的 `poll` 没有 `id` 列（主键是 `noteId`），于是同一作者的多个投票都拿到作者的 `userId`（14 个重复 ID、多出 67 行）。现在导出器从 `pg_index` 读取每张表真实的（可能是复合的）主键，复合主键按列顺序用 `/` 连接；目录里没有主键信息时只用已知表（`poll`/`promo_note` → `noteId`，`user_profile`/`user_keypair`/`user_publickey` → `userId`）或 `id`，否则用整行摘要，绝不退回到外键。同一张表出现重复 ID 时导出器立即失败；预检遇到含重复 ID 的旧 spool 报阻断错误 `duplicate_source_id`（表、数量、最多 5 个样例），`--apply` 直接拒绝。
    `poll_votes` 的「143 行只有 136 个不同 ID」不是同一个原因：Misskey 多选投票每个选项一行（各自有 `id`），而 spool 的键只用了 `投票帖/用户`，没有带选项。导入回执本来就带选项，所以没有丢票；spool 键现在也带上选项。
11. **归档原因明细。** 导入报告里每个被归档的记录都按 `类型:原因` 汇总成一条 info `archived_records`（数量 + 最多 5 个样例源 ID），例如 `post:quote_note`、`post:channel_note`、`post:chat_archive_only`、`post:unknown_visibility`、`post:boost_of_archived_post`、`post:archived_parent`、`post:reply_cycle_or_unpublished_parent`、`reaction:post_not_published`、`reaction:custom_emoji_not_imported`、`poll:post_not_published`、`poll_vote:poll_not_published`、`bookmark:post_not_published`。前几种是有意为之（目标端没有对应语义）；`*_not_published`、`reply_cycle_or_unpublished_parent` 要逐个看样例，判断是不是数据丢失。原始源行（`records`）照旧只计入 `archived.record`，不进明细。
12. **收窄预测与导入一致。** 预检的 `reply_visibility_narrowed` 计数现在按导入器的规则逐条模拟：跨分块、父帖在后面出现、回复链、私信父帖、父帖被归档（回复照常发布但不收窄）、父帖成环或缺失（回复会被归档，不计入）、以及重跑时已按回执提交的帖子（不再重复计数）。测试用随机回复森林在 SQLite / PostgreSQL 上断言预检计数 == 导入报告计数 == 实际带 `import_visibility_narrowed_from` 的帖子数。旧 spool 首次打开时的原地加密改为每 500 行一个事务并截断 WAL，内存不再随 spool 大小增长。

13. **引用帖（quote）以前全部被归档。** 第四次演练 317 条 `post:quote_note` 被归档（其中 314 条的目标在源库里），连带 49 条转发（`boost_of_archived_post`）和 137 个回应（`post_not_published`）。现在带内容的 renote（`renoteId` 且有 text / cw / 文件 / 投票）作为普通帖子导入，并用 `statuses.quote_id` 指向导入后的目标（本地或远端），与站内自己发的引用一致；纯 renote 仍是转发。链接在帖子写入之后进行，所以顺序无关：同一分块内在帖子阶段之后立即链接，目标在后面分块的，在流式导入的帖子阶段结束后再统一补链一次（帖子属性 `import_quote_pending` 在补链后删除）。和站内引用规则一致，只链接**公开或不公开（unlisted）**的目标，仅关注者 / 私信目标绝不通过引用暴露。没能链接的引用仍作为普通帖子导入（不归档），目标 URI 保存在 `properties.import_quote_uri`，原因记在 `properties.import_quote_unlinked`，报告里是非阻断警告 `quote_unlinked`，按原因汇总：`post:target_missing`（目标不在导出里）、`post:target_not_quotable`（目标是仅关注者 / 私信）、`post:target_archived`（目标在私有归档里）。预检对这三类给出与导入一致的数量和样例。转发和回应随之正常导入。
14. **实例 Actor / 中继 Actor 以前没有迁移身份。** 核对第 5 项报 `db account=None`：Misskey 的 `instance.actor`、`relay.actor` 虽作为本地账号导入，但 Community 仍以新生成的 `<base>/actor` 身份签名。现在导出器把这两个用户标为 `system_role`（`instance_actor` / `relay_actor`），导入时保留原 actor URI `https://maid.lat/users/<id>`、keyId `<uri>#main-key` 和密钥对，并写入 `system_settings`：`federation.instance_actor_uri`、`federation.relay_actor_uri`。服务端（**需要部署新版本**）据此：实例 Actor 就是该账号，签名用原 keyId；`/users/<id>` 和 `/actor` 都返回同一个文档（id 为原 URI）；WebFinger `acct:instance.actor@maid.lat`、原 URI 均可解析；设置存在但账号缺失时报错而不是另生成新身份。Misskey 的 `relay` 表也会迁移：每个中继以 `relay.actor` 为订阅者（`relays.actor_uri`，新迁移 `RelayActorURIScript`），Follow id 保留 Misskey 的 `https://maid.lat/activities/follow-relay/<relay id>`，状态（accepted / requesting / rejected）照搬，中继无需重新订阅，之后的 Undo 也用同一 actor 和 id。新加的中继同样由 `relay.actor` 订阅。核对脚本第 5 项现在检查：账号存在、密钥与 Misskey 相同、keyId 为 `<uri>#main-key`、设置指向原 URI、`/users/<id>` 与 `/actor` 都返回原 id 和密钥；5b 检查 relay.actor，5c 检查中继订阅数量。

## 重新执行 maid.lat 演练（隔离网络、只读源）

前提：一台**不能访问公网**的演练机；源库是 maid.lat 的只读副本或恢复出来的快照，使用上面的只读角色；目标是一个空的 Community 数据库。

```sh
# 0) 目标实例配置（演练机）
cat > /srv/maid-rehearsal/.env <<'ENV'
APP_BASE_URL=https://maid.lat
DB_CONNECTION=postgres
DB_DSN=host=127.0.0.1 port=5432 dbname=community_rehearsal user=community sslmode=disable
ENCRYPTED_KEY=<32 字节随机串>
ENV
export ENV_FILE=/srv/maid-rehearsal/.env PGPASSFILE=/srv/maid-rehearsal/.pgpass   # .pgpass 权限 0600

COMMON=(--source misskey
  --source-dsn 'host=10.0.0.5 port=5432 dbname=misskey user=community_export' --sslmode require
  --source-base-url https://maid.lat --id-method aidx --source-version '<maid.lat 的 Misskey 版本>'
  --object-storage-mode reference --object-base-url '<objectStorageBaseUrl>'
  --work-dir /srv/maid-rehearsal/work --batch-size 5000)

# 1) 读取源库（可在源站在线时进行；中断后重跑同一命令即续传）
community-transfer "${COMMON[@]}" --fetch-only --max-rows-per-sec 20000

# 2) 预检（不写业务数据），查看 report-validate.json 的 issues
community-transfer "${COMMON[@]}" --final-sync --report /srv/maid-rehearsal/report-validate.json

# 3) 导入（中断后加 --reuse-spool 重跑即可从检查点继续）
TRANSFER_DEBUG=1 community-transfer "${COMMON[@]}" --reuse-spool --apply --report /srv/maid-rehearsal/report-import.json

# 4) 通行密钥 / 安全密钥不迁移：按 report-validate.json 里的 security_keys_separate 名单通知用户重新注册
#    （TOTP 已随主导入迁移，含密 spool 已自动擦除）

# 5) 启动目标实例（仍在隔离网络中）并核对
MISSKEY_DB_DSN='postgresql://community_export@10.0.0.5:5432/misskey?sslmode=require' \
COMMUNITY_DB_DSN='postgresql://community@127.0.0.1:5432/community_rehearsal' \
COMMUNITY_BASE='http://127.0.0.1:8080' HOST_HEADER=maid.lat SAMPLE=200 \
python3 scripts/migration/verify_misskey_import.py --seed 42 -v
```

### 全量重跑（第四次演练之后：引用帖、实例 / 中继 Actor）

导出格式变了（引用帖不再是 archive_only，新增 `system_role` 和 `relays`），必须重新读取并换一个**空的**目标库；先在目标机部署新版本服务端（新增迁移 `RelayActorURIScript`，启动时自动执行）。

```sh
# .env：DB_DSN 指向新的空库；APP_BASE_URL=https://maid.lat、ENCRYPTED_KEY 不变
mv /srv/maid-rehearsal/work /srv/maid-rehearsal/work.round4
community-transfer "${COMMON[@]}" --fetch-only --max-rows-per-sec 20000
# 预检：期望 can_execute: true；quote_unlinked 只剩 target_missing（约 3 条）及少量 not_quotable / archived；
#       archived_records 里不应再有 post:quote_note
community-transfer "${COMMON[@]}" --final-sync --report /srv/maid-rehearsal/report-validate-5.json
TRANSFER_DEBUG=1 community-transfer "${COMMON[@]}" --reuse-spool --apply --report /srv/maid-rehearsal/report-import-5.json
# 启动（或重启）目标实例后再核对：system_settings 在启动时读入
python3 scripts/migration/verify_misskey_import.py ...   # 第 5 / 5b / 5c 项应为 PASS
```

SQL 抽查：`SELECT count(*) FROM statuses WHERE quote_id IS NOT NULL;`（预期约 311）；`SELECT * FROM system_settings WHERE key LIKE 'federation.%';`；`SELECT inbox_url, state, actor_uri, follow_activity_id FROM relays;`。

### 全量重跑（第三次演练 preserved 阶段失败之后）

旧 spool 里的原始行 ID 是错的，不能 `--reuse-spool`；目标库里已经有第三次演练写入的帖子回执和错误 ID 的归档记录，必须换一个**空的**目标库。

```sh
# 新版本二进制；.env 只改 DB_DSN 指向新的空库（APP_BASE_URL=https://maid.lat、ENCRYPTED_KEY 不变）
# 旧工作目录整个移走（含明文的部分已加密；确认不再需要后安全删除）
mv /srv/maid-rehearsal/work /srv/maid-rehearsal/work.round3
# 1) 重新读取源库
community-transfer "${COMMON[@]}" --fetch-only --max-rows-per-sec 20000
# 2) 预检：期望 can_execute: true，没有 duplicate_source_id / column_too_narrow；
#    reply_visibility_narrowed 的数量就是导入后会被收窄的帖子数
community-transfer "${COMMON[@]}" --final-sync --report /srv/maid-rehearsal/report-validate-4.json
# 3) 导入；之后在 report-import-4.json 里看 archived_records 明细
TRANSFER_DEBUG=1 community-transfer "${COMMON[@]}" --reuse-spool --apply --report /srv/maid-rehearsal/report-import-4.json
# 4) 核对：records 行数应等于不同 (kind, id) 的数量
```

### 从同一份已读取数据重跑（帖子阶段失败之后）

```sh
# 新版本二进制；.env 不变（APP_BASE_URL=https://maid.lat，ENCRYPTED_KEY 必须保持原值）
# a) 预检：打开 spool 时原地加密 keys/records 明文并 VACUUM；预期 can_execute: true，
#    reply_visibility_narrowed 警告列出将被收窄的回复（至少 2 条）
community-transfer "${COMMON[@]}" --reuse-spool --report /srv/maid-rehearsal/report-validate-3.json
# b) 导入：账号 / 表情 / 媒体及已提交的帖子分块按回执跳过，从帖子阶段的检查点继续
TRANSFER_DEBUG=1 community-transfer "${COMMON[@]}" --reuse-spool --apply --report /srv/maid-rehearsal/report-import-3.json
```

### 从同一份已读取数据重跑（表情列宽失败之后）

上一次演练的工作目录和 spool 可以直接复用，不需要重新读取源库：

```sh
# 新版本二进制；.env 不变（APP_BASE_URL=https://maid.lat，ENCRYPTED_KEY 保持原值）
# a) 预检：启动时自动执行 ImportColumnWidthsScript；旧 spool 的明文凭据在打开时被加密
community-transfer "${COMMON[@]}" --reuse-spool --report /srv/maid-rehearsal/report-validate-2.json
#    期望 can_execute: true、没有 column_too_narrow；若仍有，报告会写明表.列和样例 ID
# b) 导入：已提交的分块按回执跳过，从失败处（表情阶段）继续
TRANSFER_DEBUG=1 community-transfer "${COMMON[@]}" --reuse-spool --apply --report /srv/maid-rehearsal/report-import-2.json
```

如果在 community-transfer 运行期间 Community 服务进程也连着同一个 PostgreSQL 库，迁移改列后请重启服务（pgx 的预编译语句缓存会让旧连接对 `emojis` 等表报一次 `cached plan must not change result type`）。

步骤 2 只预检；预检结果为 `can_execute: false` 时（退出码 2）先处理阻断问题，再执行步骤 3。`TRANSFER_DEBUG=1` 会在失败时打印底层错误链（可能包含记录内容，只用于隔离演练）。
