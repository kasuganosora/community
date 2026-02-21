# 远程文件缓存机制设计方案 V3

## 1. 设计目标

- **只缓存缩略图**：不缓存原始文件，只缓存压缩后的缩略图
- **异步生成**：缩略图后台异步生成
- **智能替换**：在消息/timeline中自动替换源链接为缓存链接
- **按类型配置**：不同文件类型可设置不同的缩略图阈值
- **单存储后端**：支持配置一个对象存储供应商
- **双访问模式**：未缓存时可选择"直通"或"服务器代理"

## 2. 核心流程

### 2.1 整体流程

```
用户发布消息/获取Timeline
    │
    ▼
检测消息中的远程资源链接
    │
    ▼
匹配缓存策略
    │
    ├── 不匹配策略 → 保持原链接
    │
    └── 匹配策略
        │
        ├── 检查缩略图是否已存在
        │   │
        │   ├── 已存在 → 替换链接为缓存链接
        │   │
        │   └── 不存在
        │       │
        │       ├── 原图大小 <= 阈值？
        │       │   ├── 是 → 保持原链接（小图不处理）
        │       │   └── 否 → 触发异步生成缩略图
        │       │               ↓
        │       │           后台下载原图
        │       │           生成缩略图
        │       │           上传到存储
        │       │           更新数据库
        │       │
        │       └── 消息中保持原链接（或临时占位）
        │
        └── 返回处理后的内容
```

### 2.2 缩略图异步生成流程

```
1. 放入异步任务队列
    │
2. 后台Worker处理
    │
3. 下载远程原图（临时文件）
    │
4. 生成缩略图（压缩+格式转换）
    │
5. 上传缩略图到对象存储
    │
6. 删除临时原图（只保留缩略图）
    │
7. 更新数据库记录
    │
8. 可选：推送WebSocket通知前端更新链接
```

## 3. 核心配置

### 3.1 主配置结构

```go
type CacheConfig struct {
    Enabled     bool                `json:"enabled"`      // 是否启用缓存
    Storage     StorageConfig       `json:"storage"`      // 存储配置
    AccessMode  AccessMode          `json:"access_mode"`  // 未生成缩略图时的访问模式
    LinkRewrite LinkRewriteConfig   `json:"link_rewrite"` // 链接重写配置
    
    // 按文件类型的缩略图阈值配置
    ThumbnailRules []ThumbnailRule  `json:"thumbnail_rules"` // 缩略图规则列表
}

// 链接重写配置
type LinkRewriteConfig struct {
    Enabled          bool     `json:"enabled"`           // 是否启用链接重写
    StatusLink       bool     `json:"status_link"`       // 重写帖子中的链接
    AvatarLink       bool     `json:"avatar_link"`       // 重写头像链接
    MediaLink        bool     `json:"media_link"`        // 重写媒体链接
    TimelinePrefetch bool     `json:"timeline_prefetch"` // 获取timeline时预触发缩略图生成
}

// 缩略图规则（按文件类型）
type ThumbnailRule struct {
    // 匹配条件
    ContentTypes []string `json:"content_types"` // 内容类型，如 ["image/jpeg", "image/png"]
    Extensions   []string `json:"extensions"`    // 文件扩展名，如 ["jpg", "png"]
    
    // 阈值
    MaxOriginalSize int64 `json:"max_original_size"` // 原图超过此大小才生成缩略图（0表示都生成）
    
    // 缩略图输出配置
    MaxWidth   int    `json:"max_width"`   // 最大宽度
    MaxHeight  int    `json:"max_height"`  // 最大高度
    Quality    int    `json:"quality"`     // 质量 1-100
    Format     string `json:"format"`      // 输出格式：webp, jpeg, png
    
    // 特殊处理
    Enabled           bool     `json:"enabled"`            // 是否启用此规则
    ExcludeAnimations bool     `json:"exclude_animations"` // 是否排除动画（gif/webp动画）
}

// 存储配置（单一后端）
type StorageConfig struct {
    Type       string       `json:"type"`        // local, s3, minio, aliyun_oss, tencent_cos
    Bucket     string       `json:"bucket"`      // 存储桶名称
    Region     string       `json:"region"`      // 区域
    Endpoint   string       `json:"endpoint"`    // 自定义端点（MinIO需要）
    PathPrefix string       `json:"path_prefix"` // 路径前缀，如 "thumbnails/"
    LocalPath  string       `json:"local_path"`  // 本地缓存路径（Type=local时使用）
    PublicURL  string       `json:"public_url"`  // 公共访问URL前缀（CDN域名）
    Credentials Credentials `json:"credentials"` // 凭证
}

type Credentials struct {
    AccessKeyID     string `json:"access_key_id"`
    SecretAccessKey string `json:"secret_access_key"`
}

// 访问模式
type AccessMode string

const (
    AccessModeProxy    AccessMode = "proxy"    // 服务器代理访问
    AccessModeRedirect AccessMode = "redirect" // 直通模式（重定向到原URL）
)
```

### 3.2 完整配置示例

```yaml
# config/remote_cache.yaml
remote_cache:
  enabled: true
  
  # 存储配置（S3/MinIO/OSS等）
  storage:
    type: "s3"                          # 或 minio, aliyun_oss, tencent_cos
    bucket: "community-thumbnails"
    region: "us-east-1"
    path_prefix: "thumbnails/"          # 缩略图存储路径前缀
    public_url: "https://cdn.example.com"  # CDN域名，用于生成访问URL
    credentials:
      access_key_id: "${AWS_ACCESS_KEY_ID}"
      secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
  
  # 未生成缩略图时的访问模式
  access_mode: "redirect"  # proxy(代理原图) 或 redirect(重定向到原URL)
  
  # 链接重写配置
  link_rewrite:
    enabled: true
    status_link: true         # 重写帖子内容中的链接
    avatar_link: true         # 重写头像链接
    media_link: true          # 重写媒体附件链接
    timeline_prefetch: true   # 获取timeline时预触发缩略图生成（异步）
  
  # 缩略图规则（按文件类型）
  thumbnail_rules:
    # 规则1：JPEG/PNG照片（大图才生成缩略图）
    - content_types: ["image/jpeg", "image/png"]
      extensions: ["jpg", "jpeg", "png"]
      max_original_size: 524288      # 512KB，超过才生成
      max_width: 1920
      max_height: 1080
      quality: 85
      format: "webp"
      enabled: true
      exclude_animations: false
    
    # 规则2：WebP图片（统一转码优化）
    - content_types: ["image/webp"]
      extensions: ["webp"]
      max_original_size: 0           # 0表示都生成（统一格式）
      max_width: 1920
      max_height: 1080
      quality: 85
      format: "webp"
      enabled: true
      exclude_animations: true       # 排除动画webp
    
    # 规则3：GIF（不生成缩略图，保持原样）
    - content_types: ["image/gif"]
      extensions: ["gif"]
      enabled: false                 # 禁用，不生成缩略图
    
    # 规则4：SVG（矢量图不生成缩略图）
    - content_types: ["image/svg+xml"]
      extensions: ["svg"]
      enabled: false
    
    # 规则5：视频封面（如果有）
    - content_types: ["video/mp4", "video/webm"]
      extensions: ["mp4", "webm"]
      max_original_size: 10485760    # 10MB，大视频生成封面
      max_width: 1280
      max_height: 720
      quality: 80
      format: "webp"
      enabled: true
      exclude_animations: false
```

### 3.3 环境变量配置

```bash
# 基础配置
REMOTE_CACHE_ENABLED=true
REMOTE_CACHE_ACCESS_MODE=redirect

# 存储配置
REMOTE_CACHE_STORAGE_TYPE=s3
REMOTE_CACHE_STORAGE_BUCKET=community-thumbnails
REMOTE_CACHE_STORAGE_REGION=us-east-1
REMOTE_CACHE_STORAGE_PATH_PREFIX=thumbnails/
REMOTE_CACHE_STORAGE_PUBLIC_URL=https://cdn.example.com
REMOTE_CACHE_STORAGE_ACCESS_KEY_ID=xxx
REMOTE_CACHE_STORAGE_SECRET_ACCESS_KEY=xxx

# 链接重写
REMOTE_CACHE_REWRITE_ENABLED=true
REMOTE_CACHE_REWRITE_STATUS_LINK=true
REMOTE_CACHE_REWRITE_AVATAR_LINK=true
REMOTE_CACHE_REWRITE_MEDIA_LINK=true
REMOTE_CACHE_REWRITE_TIMELINE_PREFETCH=true

# 缩略图规则（JSON格式）
REMOTE_CACHE_THUMBNAIL_RULES='[{"content_types":["image/jpeg","image/png"],"max_original_size":524288,"max_width":1920,"max_height":1080,"quality":85,"format":"webp"}]'
```

## 4. 数据库表设计

```sql
-- 缩略图表（只存缩略图元数据）
CREATE TABLE remote_thumbnails (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    
    -- 原图信息
    original_url VARCHAR(2048) NOT NULL UNIQUE COMMENT '原始远程URL',
    original_url_hash VARCHAR(64) NOT NULL COMMENT 'URL的MD5 hash，用于查询',
    original_size BIGINT UNSIGNED COMMENT '原图大小（字节）',
    original_content_type VARCHAR(128) COMMENT '原图内容类型',
    original_width INT COMMENT '原图宽度（如果能获取）',
    original_height INT COMMENT '原图高度（如果能获取）',
    
    -- 缩略图信息
    thumbnail_url VARCHAR(2048) COMMENT '缩略图访问URL（CDN地址）',
    thumbnail_key VARCHAR(1024) NOT NULL COMMENT '存储中的key',
    thumbnail_size BIGINT UNSIGNED COMMENT '缩略图大小',
    thumbnail_width INT COMMENT '缩略图宽度',
    thumbnail_height INT COMMENT '缩略图高度',
    thumbnail_format VARCHAR(16) COMMENT '缩略图格式：webp, jpeg, png',
    
    -- 存储信息
    storage_type VARCHAR(32) NOT NULL COMMENT '存储类型：s3, minio等',
    bucket VARCHAR(128) COMMENT '存储桶',
    
    -- 处理状态
    status ENUM('pending', 'processing', 'completed', 'failed') DEFAULT 'pending',
    error_message TEXT COMMENT '错误信息',
    retry_count INT UNSIGNED DEFAULT 0 COMMENT '重试次数',
    
    -- 时间戳
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    processed_at DATETIME COMMENT '处理完成时间',
    
    -- 索引
    INDEX idx_url_hash (original_url_hash),
    INDEX idx_status (status),
    INDEX idx_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='远程缩略图表';

-- 异步任务队列表（用于缩略图生成）
CREATE TABLE thumbnail_jobs (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    original_url VARCHAR(2048) NOT NULL,
    priority INT DEFAULT 0 COMMENT '优先级（越大越优先）',
    status ENUM('pending', 'processing', 'completed', 'failed') DEFAULT 'pending',
    error_message TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    retry_count INT UNSIGNED DEFAULT 0,
    INDEX idx_status_created (status, created_at),
    INDEX idx_priority (priority DESC, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='缩略图生成任务表';

-- 缓存统计表
CREATE TABLE remote_thumbnail_stats (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    stat_date DATE NOT NULL,
    total_thumbnails INT UNSIGNED DEFAULT 0,
    total_original_size BIGINT UNSIGNED DEFAULT 0 COMMENT '原图总大小',
    total_thumbnail_size BIGINT UNSIGNED DEFAULT 0 COMMENT '缩略图总大小',
    saved_size BIGINT UNSIGNED DEFAULT 0 COMMENT '节省的空间',
    pending_jobs INT UNSIGNED DEFAULT 0,
    failed_jobs INT UNSIGNED DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_date (stat_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='缩略图统计表';
```

## 5. 核心接口

### 5.1 缩略图服务接口

```go
// ThumbnailService 缩略图服务接口
type ThumbnailService interface {
    // GetThumbnailURL 获取缩略图URL
    // 如果缩略图已存在，直接返回URL
    // 如果不存在，触发异步生成，返回原图URL（根据AccessMode）
    GetThumbnailURL(ctx context.Context, originalURL string) (ThumbnailResult, error)
    
    // BatchGetThumbnailURLs 批量获取缩略图URL（用于Timeline）
    BatchGetThumbnailURLs(ctx context.Context, originalURLs []string) map[string]ThumbnailResult
    
    // ProcessContent 处理内容中的链接（自动替换）
    // 自动检测内容中的远程图片链接，替换为缩略图链接
    ProcessContent(ctx context.Context, content string) (string, error)
    
    // ProcessStatus 处理整个帖子（包括附件）
    ProcessStatus(ctx context.Context, status *models.Status) error
    
    // ProcessAccount 处理用户账号（头像等）
    ProcessAccount(ctx context.Context, account *models.Account) error
    
    // TriggerGeneration 手动触发生成（后台异步）
    TriggerGeneration(ctx context.Context, originalURL string) error
    
    // GetStats 获取统计信息
    GetStats(ctx context.Context) (*ThumbnailStats, error)
    
    // DeleteThumbnail 删除缩略图
    DeleteThumbnail(ctx context.Context, originalURL string) error
}

// ThumbnailResult 缩略图结果
type ThumbnailResult struct {
    OriginalURL   string `json:"original_url"`
    ThumbnailURL  string `json:"thumbnail_url,omitempty"`  // 缩略图URL（如果已生成）
    ProxyURL      string `json:"proxy_url,omitempty"`      // 代理URL（如果配置）
    Status        string `json:"status"`                   // pending, processing, completed, failed, skipped
    IsThumbnail   bool   `json:"is_thumbnail"`             // 是否是缩略图链接
    Reason        string `json:"reason,omitempty"`         // 说明（如"skipped: too small", "excluded: gif"）
}

// ThumbnailStats 统计信息
type ThumbnailStats struct {
    TotalThumbnails   int64 `json:"total_thumbnails"`
    PendingJobs       int64 `json:"pending_jobs"`
    ProcessingJobs    int64 `json:"processing_jobs"`
    FailedJobs        int64 `json:"failed_jobs"`
    TotalOriginalSize int64 `json:"total_original_size"`
    TotalThumbSize    int64 `json:"total_thumb_size"`
    SavedSize         int64 `json:"saved_size"`
}
```

### 5.2 异步任务处理器

```go
// JobProcessor 任务处理器接口
type JobProcessor interface {
    // Start 启动处理器
    Start(workers int)
    
    // Stop 停止处理器
    Stop()
    
    // Submit 提交任务
    Submit(job ThumbnailJob) error
    
    // SubmitBatch 批量提交任务
    SubmitBatch(jobs []ThumbnailJob) error
}

type ThumbnailJob struct {
    ID           string
    OriginalURL  string
    Priority     int
    ContentType  string
    OriginalSize int64
}
```

### 5.3 存储后端接口

```go
// StorageBackend 存储后端接口
type StorageBackend interface {
    Type() string
    
    // Put 上传缩略图
    Put(ctx context.Context, key string, data io.Reader, size int64, contentType string) error
    
    // GetURL 获取访问URL
    GetURL(key string) string
    
    // Delete 删除
    Delete(ctx context.Context, key string) error
    
    // Exists 检查是否存在
    Exists(ctx context.Context, key string) (bool, error)
}
```

### 5.4 图片处理器接口

```go
// ImageProcessor 图片处理器接口
type ImageProcessor interface {
    // GenerateThumbnail 生成缩略图
    GenerateThumbnail(input io.Reader, opts ThumbnailOptions) (output io.Reader, width, height int, err error)
    
    // GetDimensions 获取图片尺寸
    GetDimensions(input io.Reader) (width, height int, err error)
    
    // IsAnimation 检查是否是动画（GIF/WebP动画）
    IsAnimation(input io.Reader) (bool, error)
    
    // SupportedFormats 支持的格式
    SupportedFormats() []string
}
```

## 6. 链接自动替换逻辑

### 6.1 替换流程

```go
// ProcessStatusContent 处理帖子内容
func (s *ThumbnailService) ProcessStatusContent(ctx context.Context, content string) (string, error) {
    // 1. 使用正则提取所有图片链接
    imageURLs := extractImageURLs(content)
    
    // 2. 批量获取缩略图URL
    results := s.BatchGetThumbnailURLs(ctx, imageURLs)
    
    // 3. 替换内容中的链接
    processedContent := content
    for originalURL, result := range results {
        if result.Status == "completed" {
            // 缩略图已生成，替换为缩略图链接
            processedContent = strings.ReplaceAll(processedContent, originalURL, result.ThumbnailURL)
        }
        // 其他状态（pending/processing）保持原链接，等待前端轮询或WS更新
    }
    
    return processedContent, nil
}

// ProcessStatus 处理整个帖子
func (s *ThumbnailService) ProcessStatus(ctx context.Context, status *models.Status) error {
    // 1. 处理内容中的图片
    if status.Content.Content != "" {
        processed, err := s.ProcessStatusContent(ctx, status.Content.Content)
        if err != nil {
            return err
        }
        status.Content.Content = processed
    }
    
    // 2. 处理附件
    for _, attachment := range status.Attachments {
        if attachment.Type == "image" {
            result, err := s.GetThumbnailURL(ctx, attachment.URL)
            if err != nil {
                continue
            }
            if result.Status == "completed" {
                // 保存缩略图URL到附件的meta中
                attachment.ThumbnailURL = result.ThumbnailURL
            }
            // 触发异步生成（如果还没开始）
            if result.Status == "pending" {
                s.TriggerGeneration(ctx, attachment.URL)
            }
        }
    }
    
    return nil
}

// ProcessTimeline 批量处理Timeline
func (s *ThumbnailService) ProcessTimeline(ctx context.Context, statuses []*models.Status) {
    // 收集所有需要处理的URL
    var allURLs []string
    urlToStatus := make(map[string][]*models.Status)
    
    for _, status := range statuses {
        urls := extractAllImageURLs(status)
        for _, url := range urls {
            allURLs = append(allURLs, url)
            urlToStatus[url] = append(urlToStatus[url], status)
        }
    }
    
    // 批量获取（会触发未生成缩略图的异步任务）
    results := s.BatchGetThumbnailURLs(ctx, allURLs)
    
    // 应用到各个status
    for url, result := range results {
        if result.Status == "completed" {
            for _, status := range urlToStatus[url] {
                replaceURLInStatus(status, url, result.ThumbnailURL)
            }
        }
    }
}
```

### 6.2 前端更新机制（可选）

```go
// 方式1：前端轮询
// 前端检测到pending状态的图片，定时轮询检查是否生成完成

// 方式2：WebSocket推送（实时更新）
func (s *ThumbnailService) onThumbnailCompleted(job ThumbnailJob) {
    // 推送WS消息给相关用户
    ws.Broadcast(map[string]interface{}{
        "type": "thumbnail_ready",
        "original_url": job.OriginalURL,
        "thumbnail_url": job.ThumbnailURL,
    })
}
```

## 7. API设计

### 7.1 内部API（Go）

```go
// 初始化服务
service := thumbnail.NewService(config)

// 处理单个帖子（发布或获取时调用）
err := service.ProcessStatus(ctx, status)

// 处理Timeline（批量）
service.ProcessTimeline(ctx, statuses)

// 获取缩略图URL
result, err := service.GetThumbnailURL(ctx, "https://example.com/image.jpg")
// result.ThumbnailURL - 缩略图链接（如果已生成）
// result.Status - pending/processing/completed/failed
```

### 7.2 管理员API

```
# 获取缩略图配置
GET /api/v1/admin/thumbnails/config

# 更新缩略图配置
PUT /api/v1/admin/thumbnails/config

# 获取统计信息
GET /api/v1/admin/thumbnails/stats

# 获取缩略图列表
GET /api/v1/admin/thumbnails?status=completed&page=1&limit=50

# 删除特定缩略图
DELETE /api/v1/admin/thumbnails/:id

# 清理失败/过期的缩略图
POST /api/v1/admin/thumbnails/clean

# 重试失败的任务
POST /api/v1/admin/thumbnails/retry-failed
```

### 7.3 公共API

```
# 查询缩略图状态（前端轮询用）
GET /api/v1/thumbnails/status?url=https://example.com/image.jpg

Response:
{
  "original_url": "https://example.com/image.jpg",
  "status": "completed",
  "thumbnail_url": "https://cdn.example.com/thumbnails/xxx.webp"
}
```

## 8. 实现计划

### 第一阶段：基础架构
1. 定义核心接口（ThumbnailService, StorageBackend, ImageProcessor）
2. 实现数据库表和模型
3. 实现异步任务队列（基于数据库或Redis）
4. 实现规则匹配引擎

### 第二阶段：存储和图片处理
1. 实现S3/MinIO/OSS存储后端
2. 集成图片处理库（imaging或go-vips）
3. 实现缩略图生成逻辑

### 第三阶段：链接替换
1. 实现内容中的URL提取
2. 实现链接替换逻辑
3. 集成到Timeline/Status获取流程
4. 实现头像链接替换

### 第四阶段：优化
1. 批量处理优化
2. 失败重试机制
3. 统计和监控
4. 管理API

## 9. 关键设计决策

### 9.1 为什么只缓存缩略图，不缓存原图？
- **节省存储**：原图通常很大，缩略图小很多
- **聚焦需求**：前端展示通常只需要缩略图
- **带宽优化**：用户点击原图时才加载原图（此时可能是直链CDN）

### 9.2 为什么按文件类型设置阈值？
- **差异化处理**：WebP可能需要统一转码，JPEG/PNG大文件才需要压缩
- **格式特性**：GIF/SVG不需要缩略图
- **灵活性**：用户可自定义不同格式的处理策略

### 9.3 为什么采用异步生成？
- **不阻塞响应**：Timeline获取不能等待缩略图生成
- **资源控制**：后台限制并发，避免影响主服务
- **失败重试**：失败后可自动重试，不影响用户体验

### 9.4 如何通知前端缩略图生成完成？
- **方案A（推荐）**：前端轮询，简单可靠
- **方案B**：WebSocket推送，实时但复杂
- **默认方案A**，可扩展支持方案B

## 10. 配置检查清单

- [x] 缩略图规则按文件类型配置
- [x] 不缓存原图，只缓存缩略图
- [x] 异步生成缩略图
- [x] 自动替换帖子/timeline中的链接
- [x] 支持头像链接替换
- [x] 可配置访问模式（proxy/redirect）
- [x] 支持GIF/SVG等格式排除
- [ ] 是否需要支持视频缩略图（封面图）？
- [ ] 是否需要支持HEIC/AVIF等新格式？
- [ ] 缩略图生成失败时的降级策略？
