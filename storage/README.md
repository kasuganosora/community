# storage (存储后端)

统一的存储抽象层。所有上传、读取、删除都经过 `storage.Backend` 接口，通过 `STORAGE_TYPE` 在**本地存储**（`uploads/` 目录）与 **S3 兼容对象存储**（AWS S3、Cloudflare R2、MinIO 等，基于 `minio-go`）之间切换。

## 对象键（storage key）

每个文件用一个相对的、以 `/` 分隔的**对象键**标识，例如：

| 用途 | 对象键 | 数据库保存的值 |
|------|--------|----------------|
| 媒体附件原图 | `media/2026/10/09/<文件名>` | `media_attachments.file_path = 2026/10/09/<文件名>`（`media/` 之下的部分） |
| 媒体缩略图 | `media/2026/10/09/thumb_<文件名>` | `media_attachments.thumbnail_path = thumb_<文件名>`（与原图同目录） |
| 自定义表情 | `emojis/<uuid>.png`、`emojis/<uuid>-static.png` | 表情 URL `/emoji-assets/<文件名>` |
| Matrix 媒体 | `matrix/2026/10/09/<media_id>.<ext>` | `matrix_media.file_path`（完整对象键） |

本地后端把键映射为 `uploads/<键>`，S3 后端映射为存储桶中的同名对象。数据库**只保存键**，不保存存储桶 URL 或本机路径；公开 URL 在读取时由配置推导：

- 媒体始终通过本站路由 `/media/<id>/<文件名>`、`/media/<id>/thumbnail/<缩略图>` 访问（`controllers/api/media/serve.go`）。
- 本地后端或未配置 `S3_PUBLIC_URL`：由本站从后端读取并返回（`http.ServeContent`，支持 Range）。
- 配置了 `S3_PUBLIC_URL`：图片 / 音频 / 视频以 302 跳转到 `<S3_PUBLIC_URL>/<对象键>`；其他类型仍由本站以 `attachment` 返回。

`models.MediaStorageKey` / `MediaAttachment.StorageKey()` 兼容旧数据：`2026/..` 相对路径、`uploads/...` 本机路径，以及旧版本 S3 模式误存的完整桶 URL（`https://<endpoint>/<bucket>/media/...`）。迁移脚本 `NormalizeStorageKeysScript` 会把后者改写为键。

## 架构

```
storage/
├── backend.go        # Backend 接口、CleanKey/NormalizeKey、LocalBackend、Default()/SetDefault()
├── s3.go             # S3Config/LoadS3Config、S3Backend、CheckHealth（缓存的健康探测）
├── storage.go        # 兼容 API：Save/SaveByMultipart/Delete/Open/PutFile、CloudStorage(StorageProvider)
├── local.go          # 旧的 LocalStorage 类型（保留兼容）
└── storagetest/      # 测试用：内存对象存储 Memory、真实 S3 测试服务器辅助 S3FromEnv
```

```go
b := storage.Default()                     // 按配置选择 LocalBackend 或 S3Backend
err := b.Put(ctx, "media/a.jpg", r, size, "image/jpeg")
rc, info, err := b.Open(ctx, "media/a.jpg") // 不存在时返回 storage.ErrNotFound；rc 支持 Seek
err = b.Delete(ctx, "media/a.jpg")          // 对象不存在不报错
url := b.PublicURL("media/a.jpg")           // 无公开地址时为 ""
report := storage.CheckHealth(ctx)          // HeadBucket 探测，超时 4 秒，结果缓存 30 秒
```

`CleanKey` 拒绝空键、绝对路径、`..`、反斜杠与 NUL；本地后端另外用 `isPathInsideRoot` 校验结果路径仍在根目录内。

## 配置

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `STORAGE_TYPE` | `local` | `local`，或 `s3` / `minio`（同义） |
| `S3_ENDPOINT` | `localhost:9000` | 端点 `host[:port]`；带 `https://` / `http://` 前缀时同时决定是否使用 TLS |
| `S3_REGION` | `us-east-1` | Cloudflare R2 填 `auto` |
| `S3_BUCKET` | `community` | 存储桶（必须已存在；不会自动创建或修改访问策略） |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | （无） | 访问密钥；缺失时 S3 后端报错，**不会回退到本地** |
| `S3_USE_SSL` | `false` | 是否使用 HTTPS（端点带协议前缀时以前缀为准） |
| `S3_FORCE_PATH_STYLE` | `auto` | `true` 路径风格（R2 / MinIO 推荐），`false` 虚拟主机风格 |
| `S3_PUBLIC_URL` | （无） | 可选，指向**存储桶根目录**的公开地址（R2 公开桶 / 自定义域名 / CDN） |
| `S3_LEGACY_LOCAL_FALLBACK` | `true` | 桶内缺失时是否回退读取（及删除）`uploads/` 下的旧文件 |

Cloudflare R2 示例：

```dotenv
STORAGE_TYPE=s3
S3_ENDPOINT=https://<ACCOUNT_ID>.r2.cloudflarestorage.com
S3_REGION=auto
S3_BUCKET=community-file
S3_ACCESS_KEY=<R2_ACCESS_KEY_ID>
S3_SECRET_KEY=<R2_SECRET_ACCESS_KEY>
S3_USE_SSL=true
S3_FORCE_PATH_STYLE=true
# S3_PUBLIC_URL=https://media.example.com
```

## 健康检查

`/health/detailed` 的 `storage` 项：本地模式检查目录；S3 模式调用 `storage.CheckHealth`（对配置的桶做 HeadBucket），凭据错误、桶不存在、端点不可达时报告 `unhealthy`（超时为 `degraded`），消息中不包含密钥和端点主机名。

## 测试

单元测试默认使用 `storagetest.Memory`。设置以下变量后，`storage`、`controllers/api/media`、`emojiasset`、`matrix/homeserver`、`health` 中的 S3 用例会连接真实的 S3 兼容服务（每个用例创建并清理独立的桶）：

```bash
TEST_S3_ENDPOINT=127.0.0.1:9000 TEST_S3_ACCESS_KEY=... TEST_S3_SECRET_KEY=... go test ./...
```
