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
| `APP_BASE_URL` | 必需 | 必须与源站域名一致 |
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

spool（`spool.sqlite3*`）、读取状态（`state/staging.sqlite3`）和 `out-*/.spool`、`bundle.json` 里都有**明文** `twoFactorSecret`、密码哈希和签名私钥。

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
   - 通行密钥 / 安全密钥（passkeys / WebAuthn）：不在主导入里，**导入完成后立即**单独运行  
     `tootctl passkeys import-misskey --dsn "$MISSKEY_DSN" --dry-run`，确认后去掉 `--dry-run`（按用户名匹配，可重复执行）。安装向导里是独立的 `passkeys` 阶段。预检对有安全密钥的账号给出 `security_keys_separate` 警告提醒这一步。
   - **引用（quote）笔记**（maid.lat：317 条）和**频道笔记**（5 条）只进入私有归档（archive-only），不作为公开帖子发布。回复它们的笔记照常发布，但不带回复链（帖子属性 `import_reply_parent_archived` 记录原父帖 ID）；转发它们的笔记同样只归档。
   - **账号级可见性策略**：Misskey 的 `requireSigninToViewContents`（需要登录才能查看）、`makeNotesFollowersOnlyBefore` / `makeNotesHiddenBefore`（把某个时间点之前的笔记自动改为仅关注者可见 / 隐藏；正数是 Unix 秒，负数是"距今 N 秒"）在 Community 里没有对应功能。设置了其中任何一个的本地账号会在预检中报 `unsupported_privacy_policy`（阻断），报告会写明字段和值，例如 `makeNotesHiddenBefore=-2592000`。映射策略待定。
   - 使用目标站不存在的自定义表情（例如本地迁移范围之外的远端表情）的回应会归档，而不是让整个事务失败。
6. **核对脚本以前会误报通过。** `verify_misskey_import.py` 现在：空目标库直接 FAIL；逐个检查每个本地用户是否存在；实例 Actor 的公钥必须等于 Misskey `instance.actor` 的公钥；连接失败显示真实错误（不再是 `HTTP 0`）。
7. **旧导出器的隐藏错误。** 失败时会打印阶段、异常类型和 psql 的原始错误（密码已脱敏），`--verbose` 打印 traceback；psql 警告（例如 PGPASSFILE 权限不安全）也会显示。

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

# 4) 通行密钥 / 安全密钥（导入后立即执行；TOTP 已随主导入迁移，含密 spool 已自动擦除）
tootctl passkeys import-misskey --dsn "postgresql://community_export@10.0.0.5:5432/misskey?sslmode=require" --dry-run

# 5) 启动目标实例（仍在隔离网络中）并核对
MISSKEY_DB_DSN='postgresql://community_export@10.0.0.5:5432/misskey?sslmode=require' \
COMMUNITY_DB_DSN='postgresql://community@127.0.0.1:5432/community_rehearsal' \
COMMUNITY_BASE='http://127.0.0.1:8080' HOST_HEADER=maid.lat SAMPLE=200 \
python3 scripts/migration/verify_misskey_import.py --seed 42 -v
```

步骤 2 只预检；预检结果为 `can_execute: false` 时（退出码 2）先处理阻断问题，再执行步骤 3。`TRANSFER_DEBUG=1` 会在失败时打印底层错误链（可能包含记录内容，只用于隔离演练）。
