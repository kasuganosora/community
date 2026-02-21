# 远程文件缓存与缩略图服务

本项目实现了远程文件缓存和缩略图生成服务，支持自动将时间线中的远程媒体图片转换为本地缓存的缩略图。

## 功能特性

### 核心功能
- ✅ **异步缩略图生成**：通过后台工作队列处理缩略图生成，不影响用户请求响应
- ✅ **数据库驱动配置**：所有配置存储在数据库中，支持动态修改
- ✅ **多存储后端**：支持本地存储、S3、MinIO 等存储后端
- ✅ **智能规则匹配**：根据文件类型、大小自动决定是否生成缩略图
- ✅ **URL重写**：自动替换时间线内容中的远程图片URL为本地缩略图URL
- ✅ **失败重试机制**：支持失败任务的重试，最大重试次数可配置

### 支持的格式
- **图片**：JPEG、PNG、WebP、GIF、HEIC、AVIF、SVG
- **视频**：MP4、WebM、MOV（提取封面帧）

### 存储后端
- ✅ `local` - 本地文件系统存储（完整实现）
- ✅ `s3` - Amazon S3 兼容存储（AWS SDK v2 集成）
- ✅ `minio` - MinIO 对象存储（AWS SDK v2 兼容模式）

## 架构设计

### 包结构
```
thumbnail/
├── config.go          # 配置管理（数据库驱动）
├── init.go            # 服务初始化和生命周期管理
├── middleware.go      # Gin中间件（URL重写）
├── README.md          # 本文档
├── service/           # 核心业务逻辑
│   ├── service.go     # 缩略图服务
│   ├── service_test.go # 服务单元测试
│   └── worker.go      # 后台工作器
├── storage/           # 存储后端
│   ├── storage.go     # 存储接口和本地实现
│   ├── storage_test.go # 存储测试
│   ├── s3.go          # S3存储实现（AWS SDK v2）
│   ├── minio.go       # MinIO存储实现
│   └── types.go       # 存储配置类型
└── processor/         # 媒体处理器
    ├── processor.go   # 图片处理器
    ├── processor_test.go # 处理器测试
    ├── types.go       # 处理器配置
    └── video.go       # 视频处理器（FFmpeg集成）
```

### 数据模型
```go
// 缩略图记录
Thumbnail {
    ID, OriginalURL, OriginalURLHash
    OriginalSize, OriginalMediaType
    ThumbnailURL, ThumbnailSize, ThumbnailWidth, ThumbnailHeight
    StorageType, Bucket, StorageKey
    Status: pending/processing/completed/failed/skipped
}

// 缩略图任务队列
ThumbnailJob {
    ID, OriginalURL, Status, Priority
    RetryCount, MaxRetries, Error
    StartedAt, CompletedAt
}
```

## Admin API

### 缩略图统计
```
GET /api/v1/admin/thumbnails/stats
```

### 获取配置
```
GET /api/v1/admin/thumbnails/config
```

### 更新配置
```
PUT /api/v1/admin/thumbnails/config
{
    "enabled": true,
    "access_mode": "proxy",
    "rewrite_enabled": true,
    "storage": {
        "type": "local",
        "local_path": "./storage/thumbnails",
        "path_prefix": "thumbnails/"
    },
    "rules": [...]
}
```

### 获取缩略图列表
```
GET /api/v1/admin/thumbnails?page=1&limit=20&status=completed
```

### 删除缩略图
```
DELETE /api/v1/admin/thumbnails/:id
```

### 重新生成缩略图
```
POST /api/v1/admin/thumbnails/:id/regenerate
```

### 批量清理缩略图
```
POST /api/v1/admin/thumbnails/purge
{
    "older_than_days": 30,
    "status": "failed"  // failed/completed/all
}
```

## 配置说明

### 缩略图规则
```go
{
    "content_types": ["image/jpeg", "image/png"],
    "extensions": ["jpg", "jpeg", "png"],
    "max_original_size": 524288,  // 512KB，0表示全部处理
    "max_width": 1920,
    "max_height": 1080,
    "quality": 85,
    "format": "webp",             // 输出格式：webp/jpeg/png
    "enabled": true,
    "exclude_animations": false,  // 是否跳过动画（GIF/WebP）
    "media_type": "image"         // image/video
}
```

### 访问模式
- `proxy` - 服务器代理访问缩略图
- `redirect` - 重定向到原URL（直通模式）

### 存储配置示例

#### 本地存储
```json
{
    "type": "local",
    "local_path": "./storage/thumbnails",
    "path_prefix": "thumbnails/",
    "public_url": "https://cdn.example.com"
}
```

#### S3 存储
```json
{
    "type": "s3",
    "bucket": "my-thumbnails",
    "region": "us-east-1",
    "path_prefix": "thumbnails/",
    "public_url": "https://cdn.example.com"
}
```
环境变量设置：
- `THUMBNAIL_STORAGE_ACCESS_KEY`
- `THUMBNAIL_STORAGE_SECRET_KEY`

#### MinIO 存储
```json
{
    "type": "minio",
    "bucket": "thumbnails",
    "region": "us-east-1",
    "endpoint": "http://localhost:9000",
    "path_prefix": "thumbnails/",
    "public_url": "https://minio.example.com"
}
```

## 使用流程

1. **初始化**：服务启动时自动迁移数据库表，加载配置
2. **请求处理**：当用户查看时间线时，检查内容中的远程图片URL
3. **规则匹配**：根据配置规则判断是否需要生成缩略图
4. **异步处理**：如需生成，创建待处理任务并立即返回原URL
5. **后台生成**：工作器从队列获取任务，下载原图并生成缩略图
6. **URL重写**：后续请求时，自动将原URL替换为缩略图URL

## 测试

```bash
# 运行所有缩略图测试
go test ./thumbnail/... -v

# 运行特定包测试
go test ./thumbnail/service/... -v
go test ./thumbnail/processor/... -v
go test ./thumbnail/storage/... -v

# 运行所有项目测试
go test ./... -v
```

### 测试结果
```
✓ TestNewService - 服务创建测试
✓ TestMatchRule - 规则匹配测试
✓ TestGenerateCacheKey - 缓存键生成测试
✓ TestStorageOperations - 存储操作测试
✓ TestNewDefaultProcessor - 处理器创建测试
✓ TestIsSupported - 格式支持测试
✓ TestGetDimensions - 尺寸获取测试
✓ TestGenerateThumbnail - 缩略图生成测试
✓ TestCalculateDimensions - 尺寸计算测试
✓ TestVideoProcessorIsSupported - 视频支持测试
✓ TestVideoProcessorSetSeekTime - 视频时间点设置测试
✓ TestEncodeWebP - WebP编码测试
✓ TestGenerateThumbnailPNG - PNG缩略图测试
✓ TestGenerateThumbnailNoScaling - 无需缩放测试
✓ TestNewLocalBackend - 本地后端测试
✓ TestLocalBackendPutAndGet - 存储读写测试
✓ TestLocalBackendExists - 存在检查测试
✓ TestLocalBackendDelete - 删除测试
✓ TestLocalBackendNestedPath - 嵌套路径测试
✓ TestLocalBackendWithPublicURL - 公共URL测试
✓ TestUnsupportedStorageType - 不支持类型测试
✓ TestLocalBackendLargeFile - 大文件测试
✓ TestLocalBackendSpecialCharacters - 特殊字符测试
✓ TestLocalBackendMultipleOperations - 多操作测试
✓ TestWorkerLifecycle - 工作器生命周期测试
✓ TestConfigAccessMode - 访问模式测试
✓ TestConfigRewriteEnabled - URL重写测试
```

## FFmpeg 视频处理

视频缩略图生成依赖 FFmpeg。请确保系统已安装 FFmpeg：

```bash
# Ubuntu/Debian
sudo apt-get install ffmpeg

# macOS
brew install ffmpeg

# Windows
choco install ffmpeg
```

视频处理器会自动检测 FFmpeg 并使用它来提取视频封面帧。

## 性能优化建议

1. **存储选择**
   - 小规模部署：使用本地存储
   - 大规模部署：使用 S3/MinIO + CDN

2. **缩略图尺寸**
   - 根据实际展示需求设置合理的 max_width/max_height
   - 避免生成过大缩略图浪费存储空间

3. **规则优化**
   - 对小图片设置 max_original_size 阈值，避免不必要的处理
   - 对 GIF 动画设置 exclude_animations 为 true

4. **并发控制**
   - 根据服务器资源调整工作器数量（默认 3 个）
   - 监控队列长度，必要时增加工作器

## 安全注意事项

1. **凭证管理**
   - S3/MinIO 凭证通过环境变量设置，不存储在数据库
   - Admin API 返回的配置会隐藏 AccessKey 和 SecretKey

2. **URL验证**
   - 只处理 http/https 协议的 URL
   - 跳过本站域名的 URL

3. **文件类型限制**
   - 通过 content_types 和 extensions 限制处理的文件类型
   - 避免处理可能有害的文件格式

## 故障排查

### 缩略图生成失败
1. 检查日志中的错误信息
2. 确认原始文件可访问（HEAD 请求返回 200）
3. 检查存储后端配置是否正确
4. 验证 FFmpeg 是否安装（视频处理）

### URL 未重写
1. 确认 rewrite_enabled 设置为 true
2. 检查缩略图状态是否为 completed
3. 验证中间件是否正确注册
4. 检查内容类型是否为 application/json

### 存储空间不足
1. 运行清理任务删除旧缩略图
2. 调整 TTLDays 配置自动过期
3. 考虑使用对象存储（S3/MinIO）替代本地存储

## 许可证

本项目采用与主项目相同的许可证。
