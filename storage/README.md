# storage (存储后端)

统一的存储抽象层，为上层应用提供文件上传、下载、删除与 URL 生成能力。通过 `STORAGE_TYPE` 配置项在**本地存储**与 **S3 / MinIO 对象存储**之间透明切换，并对所有本地路径操作实施**路径遍历防护**（path traversal protection）。

## 核心功能

- **统一接口**：`StorageProvider` 接口屏蔽底层存储差异，上层代码无需关心本地还是云端
- **本地存储**：基于 `uploads/` 根目录的文件读写，`LocalStorage` 封装了 `Exists` / `Open` / `Save` / `Delete`
- **S3 / MinIO 对象存储**：`S3Provider` 基于 `minio-go`，自动创建 bucket、设置公开读取策略、支持预签名 URL
- **路径遍历防护**：所有暴露用户路径的接口均经过 `filepath.Clean` + `isPathInsideRoot` / `hasPrefixPath` 校验，拒绝 `../` 越界与同前缀（如 `uploadsevil`）攻击
- **向后兼容**：保留 `Save` / `SaveByMultipart` / `Delete` 包级函数，兼容旧 API
- **单例管理**：`CloudStorage` 通过 `sync.Once` 初始化全局实例

## 架构/结构

```
storage/
├── storage.go       # StorageProvider 接口 + CloudStorage 管理器 + S3Provider + 包级兼容 API
├── local.go         # LocalStorage 本地存储实现 + hasPrefixPath 防护
├── security_test.go # 路径遍历防护测试
├── local_test.go
├── storage_test.go
└── uploads/         # 默认本地存储根目录
```

模块分层：

| 层 | 组成 | 职责 |
|----|------|------|
| 接口 | `StorageProvider` | 定义 Upload/Download/Delete/GetURL/IsEnabled 契约 |
| 管理器 | `CloudStorage` | 单例，根据配置选择 provider，本地模式下直接操作 `uploads/` |
| 本地实现 | `LocalStorage`（`local.go`） | 纯本地文件读写，自带路径校验 |
| 云端实现 | `S3Provider`（`storage.go`） | S3/MinIO 客户端封装 |
| 兼容 API | `Save` / `SaveByMultipart` / `Delete` | 面向旧调用方的包级函数 |

### 判定逻辑

```
STORAGE_TYPE ∈ {"s3", "minio"}  →  启用云存储（S3Provider）
STORAGE_TYPE == "local" 或为空   →  本地存储（CloudStorage 本地模式 / LocalStorage）
```

当云存储启用但 `S3_ACCESS_KEY` / `S3_SECRET_KEY` 未配置时，`S3Provider.IsEnabled()` 返回 `false`，自动回退为本地模式。

## 用法示例

### 获取存储管理器（推荐方式）

```go
import "github.com/kasuganosora/community/storage"

store := storage.GetStorage() // 全局单例

// 上传本地文件（objectKey 为云端对象键）
cloudPath, err := store.Upload("/tmp/file.jpg", "media/2025/01/01/abc.jpg")

// 上传 io.Reader
url, err := store.UploadReader(reader, "media/abc.jpg", size)

// 下载
rc, err := store.Download("media/2025/01/01/abc.jpg")
defer rc.Close()

// 删除
err = store.Delete("media/2025/01/01/abc.jpg")

// 获取访问 URL
url := store.GetURL("media/2025/01/01/abc.jpg")

// 健康检查（仅云存储有意义）
if !store.HealthCheck() {
    // 存储后端不可达
}

// 生成预签名 URL（仅 S3，临时访问）
presigned, err := store.GeneratePresignedURL("media/abc.jpg", time.Hour)
```

> `GetStorage()` 在未启用云存储时，`Upload` 返回原路径，`Download`/`Delete` 走本地 `uploads/` 目录并自动做路径校验。

### 包级兼容 API（旧调用方）

```go
// 保存字节数据到 uploads/<path>
savePath, err := storage.Save("avatars/user.png", data)

// 保存 multipart 上传文件
savePath, err := storage.SaveByMultipart("avatars/user.png", fileHeader)

// 删除（云存储启用时删云端，否则删本地）
err := storage.Delete("avatars/user.png")
```

### 直接使用本地存储

```go
local := &storage.LocalStorage{Root: "uploads"}

if !local.Exists("media/abc.jpg") {
    return errors.New("not found")
}

reader, err := local.Open("media/abc.jpg") // 返回 *bytes.Reader
savePath, err := local.Save("media/new.jpg", data)
err = local.Delete("media/new.jpg")
```

### 直接使用 S3 提供者

```go
provider := storage.NewS3Provider()
if !provider.IsEnabled() {
    // S3 未配置或连接失败
}
url, err := provider.Upload("/tmp/file.jpg", "media/abc.jpg")
```

## 配置说明

存储类型通过环境变量 `STORAGE_TYPE` 控制（对应 `config.C.StorageType`）：

```bash
# 存储类型：local（默认）、s3、minio
STORAGE_TYPE=local

# 应用基础 URL（本地模式下用于拼接文件访问地址）
APP_BASE_URL=http://localhost:8080
```

### S3 / MinIO 配置

启用 `STORAGE_TYPE=s3` 或 `minio` 时，以下配置项从 `.env` / 环境变量读取（非标准配置项，存入 `config.C.Other`）：

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `S3_ENDPOINT` | `localhost:9000` | S3/MinIO 服务端点 |
| `S3_BUCKET` | `community` | bucket 名称（不存在时自动创建） |
| `S3_REGION` | `us-east-1` | 区域 |
| `S3_ACCESS_KEY` | （无） | Access Key，**留空则禁用云存储** |
| `S3_SECRET_KEY` | （无） | Secret Key，**留空则禁用云存储** |
| `S3_USE_SSL` | `false` | 是否启用 TLS（`true`/`false`） |
| `S3_PUBLIC_URL` | （无） | 公开访问基址，设置后 `GetURL` 优先使用它 |

> bucket 创建后会自动设置公开读取策略（`s3:GetObject` 允许 `*`），以便前端直接通过 URL 访问。

## 路径遍历防护

所有接受用户可控路径的接口均经过两层防护：

1. `filepath.Join(root, path)` + `filepath.Clean(...)` 规范化路径
2. `isPathInsideRoot` / `hasPrefixPath` 验证结果路径仍位于 `root` 内部

使用 `root + separator` 前缀匹配，避免 `uploadsevil` 这类同前缀目录被误判为 `uploads` 的子路径。命中越界时返回错误：

```
invalid file path: potential path traversal detected
```

受防护的入口包括：`Save`、`SaveByMultipart`、`Delete`（包级与 `CloudStorage` 方法）、`CloudStorage.Download`，以及 `LocalStorage` 的全部方法。

## 核心类型与函数

### 类型

#### `StorageProvider`（接口）

存储提供者契约，所有实现需满足：

```go
type StorageProvider interface {
    Upload(filePath string, objectKey string) (string, error)
    UploadReader(reader io.Reader, objectKey string, size int64) (string, error)
    Download(objectKey string) (io.ReadCloser, error)
    Delete(objectKey string) error
    GetURL(objectKey string) string
    IsEnabled() bool
}
```

#### `CloudStorage`

存储管理器，通过 `GetStorage()` 单例获取。根据 `IsEnabled()` 在本地与云端间分流。

| 方法 | 说明 |
|------|------|
| `IsEnabled() bool` | 云存储是否启用 |
| `Upload(filePath, objectKey) (string, error)` | 上传；未启用时直接返回本地路径 |
| `UploadReader(reader, objectKey, size) (string, error)` | 从 Reader 上传（需启用） |
| `Download(objectKey) (io.ReadCloser, error)` | 下载；本地模式带路径遍历防护 |
| `Delete(objectKey) error` | 删除；本地模式带路径遍历防护 |
| `GetURL(objectKey) string` | 获取访问 URL（本地模式为 `APP_BASE_URL/objectKey`） |
| `GeneratePresignedURL(objectKey, expiry) (string, error)` | 生成预签名 URL（仅 S3） |
| `HealthCheck() bool` | 存储健康检查 |

#### `S3Provider`

S3/MinIO 存储实现，字段：`bucket`、`endpoint`、`region`、`useSSL`、`publicBaseURL`。通过 `NewS3Provider()` 创建并自动初始化客户端、校验/创建 bucket。

#### `LocalStorage`

本地存储实现（`local.go`），字段：`Root string`。

| 方法 | 说明 |
|------|------|
| `Exists(path) bool` | 文件是否存在（含路径校验） |
| `Open(path) (*bytes.Reader, error)` | 读取文件为 `bytes.Reader` |
| `Save(path, data) (savePath string, err error)` | 保存字节，自动创建目录，返回相对路径 |
| `Delete(path) error` | 删除文件 |

### 包级函数

| 函数 | 说明 |
|------|------|
| `GetStorage() *CloudStorage` | 获取全局存储管理器（`sync.Once` 单例） |
| `NewS3Provider() *S3Provider` | 创建 S3 提供者 |
| `Save(path string, data []byte) (string, error)` | 保存字节数据到 `uploads/`（兼容旧 API） |
| `SaveByMultipart(path string, fileHeader *multipart.FileHeader) (string, error)` | 保存 multipart 文件（兼容旧 API） |
| `Delete(path string) error` | 删除文件，云存储启用时删云端（兼容旧 API） |

## 依赖关系

| 依赖 | 用途 |
|------|------|
| `github.com/minio/minio-go/v7` | S3 / MinIO 客户端 |
| `github.com/kasuganosora/community/config` | 读取 `StorageType` / `AppBaseURL` / S3 配置 |
| `github.com/kasuganosora/community/log` | 日志输出 |
| `github.com/kasuganosora/community/utils` | `IsFileExists` 等工具函数 |

> 云存储上传/下载默认超时为 5 分钟，bucket 探测为 10 秒，删除为 30 秒，健康检查为 5 秒；下载返回的 `io.ReadCloser` 在 `Close()` 时会取消请求 context。
