# 远程文件缓存机制设计方案 V2

## 1. 设计目标（精简版）

- **智能缓存策略**：根据文件类型、大小等规则自动匹配缩略图生成策略
- **单存储后端**：支持配置一个对象存储供应商（S3/MinIO/阿里云OSS等）
- **无内存缓存**：直接本地磁盘缓存或对象存储
- **缩略图优化**：大图自动转小尺寸缩略图，支持排除特定格式
- **双访问模式**：未缓存时可选择"直通"或"服务器代理"

## 2. 简化架构

```
┌─────────────────────────────────────────────────────────────┐
│                    Remote File Cache                        │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐ │
│  │ Rule Engine │  │  Metadata   │  │   Thumbnail Gen     │ │
│  │   (匹配)     │  │   Store     │  │    (缩略图生成)      │ │
│  └─────────────┘  └─────────────┘  └─────────────────────┘ │
└────────────────────┬────────────────────────────────────────┘
                     │
        ┌────────────┴────────────┐
        ▼                         ▼
┌─────────────┐           ┌─────────────┐
│  Local Disk │           │   Object    │
│   (Cache)   │           │  Storage    │
│             │           │(S3/OSS/...) │
└─────────────┘           └─────────────┘
```

## 3. 核心配置

### 3.1 主配置结构

```go
type CacheConfig struct {
    Enabled bool `json:"enabled"` // 是否启用缓存
    
    // 缩略图配置
    Thumbnail ThumbnailConfig `json:"thumbnail"`
    
    // 存储配置
    Storage StorageConfig `json:"storage"`
    
    // 访问模式
    AccessMode AccessMode `json:"access_mode"` // proxy 或 redirect
    
    // 缓存策略列表
    Policies []CachePolicy `json:"policies"`
}

// 缩略图配置
type ThumbnailConfig struct {
    Enabled           bool     `json:"enabled"`             // 是否启用缩略图
    MaxSize           int64    `json:"max_size"`            // 超过此大小生成缩略图（默认512KB）
    MaxWidth          int      `json:"max_width"`           // 最大宽度（默认1920）
    MaxHeight         int      `json:"max_height"`          // 最大高度（默认1080）
    Quality           int      `json:"quality"`             // 图片质量 1-100（默认85）
    Format            string   `json:"format"`              // 输出格式：webp, jpeg, png（默认webp）
    ExcludeFormats    []string `json:"exclude_formats"`     // 不处理的格式，如 ["gif", "svg"]
    PreserveAnimation bool     `json:"preserve_animation"`  // 是否保留动画（仅对gif/webp）
}

// 存储配置（单一后端）
type StorageConfig struct {
    Type       string            `json:"type"`        // local, s3, minio, aliyun_oss, tencent_cos
    Bucket     string            `json:"bucket"`      // 存储桶名称
    Region     string            `json:"region"`      // 区域
    Endpoint   string            `json:"endpoint"`    // 自定义端点（MinIO需要）
    PathPrefix string            `json:"path_prefix"` // 路径前缀
    LocalPath  string            `json:"local_path"`  // 本地缓存路径（Type=local时使用）
    Credentials Credentials     `json:"credentials"` // 凭证
}

type Credentials struct {
    AccessKeyID     string `json:"access_key_id"`
    SecretAccessKey string `json:"secret_access_key"`
}

// 访问模式
type AccessMode string

const (
    AccessModeProxy    AccessMode = "proxy"    // 服务器代理访问（先下载再返回）
    AccessModeRedirect AccessMode = "redirect" // 直通模式（重定向到原URL）
)
```

### 3.2 缓存策略配置

```go
type CachePolicy struct {
    ID       string      `json:"id"`       // 策略ID
    Name     string      `json:"name"`     // 策略名称
    Priority int         `json:"priority"` // 优先级（数字越小越优先）
    Enabled  bool        `json:"enabled"`  // 是否启用
    
    Rules       []MatchRule   `json:"rules"`       // 匹配规则
    Thumbnail   *ThumbnailOverride `json:"thumbnail,omitempty"` // 缩略图覆盖配置（可选）
    TTL         string        `json:"ttl"`         // 缓存时间（如 "7d", "24h"）
    StoragePath string        `json:"storage_path"` // 存储路径覆盖（可选）
}

// 缩略图覆盖配置（用于覆盖全局配置）
type ThumbnailOverride struct {
    Enabled    *bool   `json:"enabled,omitempty"`
    MaxSize    *int64  `json:"max_size,omitempty"`
    MaxWidth   *int    `json:"max_width,omitempty"`
    MaxHeight  *int    `json:"max_height,omitempty"`
    Quality    *int    `json:"quality,omitempty"`
    Format     *string `json:"format,omitempty"`
}

type MatchRule struct {
    Field    string      `json:"field"`    // url, content_type, file_extension, file_size, source_domain
    Operator string      `json:"operator"` // eq, ne, contains, prefix, suffix, regex, gt, lt, between, in
    Value    interface{} `json:"value"`    // 匹配值
}
```

### 3.3 预定义策略示例

```yaml
# 默认配置
cache:
  enabled: true
  
  # 全局缩略图配置
  thumbnail:
    enabled: true
    max_size: 524288        # 512KB，超过此大小生成缩略图
    max_width: 1920         # 最大宽度1920
    max_height: 1080        # 最大高度1080
    quality: 85             # 质量85%
    format: "webp"          # 输出webp格式
    exclude_formats:        # 排除的格式（不生成缩略图）
      - "gif"
      - "svg"
      - "ico"
    preserve_animation: false  # 不保留动画（gif转为静态图）
  
  # 存储配置（S3示例）
  storage:
    type: "s3"
    bucket: "community-cache"
    region: "us-east-1"
    path_prefix: "remote-cache/"
    # endpoint: ""  # S3不需要，MinIO需要
    credentials:
      access_key_id: "${AWS_ACCESS_KEY_ID}"
      secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
  
  # 访问模式
  access_mode: "proxy"  # 未缓存时通过服务器代理访问（可选：redirect直接跳转）
  
  # 缓存策略（按优先级排序）
  policies:
    # 策略1：联邦实例头像（小图，长缓存）
    - id: "federation_avatar"
      name: "联邦头像缓存"
      priority: 10
      enabled: true
      rules:
        - field: "url"
          operator: "regex"
          value: "https?://[^/]+/users/[^/]+/avatar"
      ttl: "720h"  # 30天
      storage_path: "federation/avatars/"
      thumbnail:
        max_width: 200
        max_height: 200
        quality: 80
    
    # 策略2：帖子图片（大图，中等缓存）
    - id: "post_image"
      name: "帖子图片缓存"
      priority: 20
      enabled: true
      rules:
        - field: "content_type"
          operator: "prefix"
          value: "image/"
        - field: "url"
          operator: "regex"
          value: ".*media[^/]+.*\.(jpg|jpeg|png|webp|gif)"
      ttl: "168h"  # 7天
      storage_path: "posts/images/"
    
    # 策略3：帖子视频（不生成缩略图，长缓存）
    - id: "post_video"
      name: "帖子视频缓存"
      priority: 30
      enabled: true
      rules:
        - field: "content_type"
          operator: "prefix"
          value: "video/"
      ttl: "720h"  # 30天
      storage_path: "posts/videos/"
      thumbnail:
        enabled: false  # 视频不生成缩略图
    
    # 策略4：远程附件（通用）
    - id: "remote_attachment"
      name: "远程附件缓存"
      priority: 100
      enabled: true
      rules: []  # 无规则，匹配所有
      ttl: "168h"  # 7天
      storage_path: "attachments/"
```

## 4. 数据库表设计（简化）

```sql
-- 缓存文件元数据表
CREATE TABLE remote_file_cache (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    original_url VARCHAR(2048) NOT NULL UNIQUE COMMENT '原始远程URL',
    cache_key VARCHAR(512) NOT NULL COMMENT '缓存键（hash）',
    
    -- 匹配的策略
    policy_id VARCHAR(64) NOT NULL COMMENT '匹配的策略ID',
    
    -- 文件信息
    file_name VARCHAR(512) COMMENT '文件名',
    content_type VARCHAR(128) COMMENT '内容类型',
    original_size BIGINT UNSIGNED COMMENT '原始文件大小（字节）',
    cached_size BIGINT UNSIGNED COMMENT '缓存后大小（字节）',
    
    -- 存储位置
    storage_type VARCHAR(32) NOT NULL COMMENT '存储类型：local, s3, minio等',
    bucket VARCHAR(128) COMMENT '存储桶',
    object_key VARCHAR(1024) NOT NULL COMMENT '对象存储key',
    
    -- 缩略图信息
    is_thumbnail BOOLEAN DEFAULT FALSE COMMENT '是否是缩略图',
    original_url_hash VARCHAR(64) COMMENT '原图URL的hash（缩略图用）',
    thumbnail_width INT COMMENT '缩略图宽度',
    thumbnail_height INT COMMENT '缩略图高度',
    
    -- 缓存状态
    status ENUM('active', 'processing', 'failed', 'expired') DEFAULT 'active',
    error_message TEXT COMMENT '错误信息（失败时）',
    
    -- 时间戳
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL COMMENT '过期时间',
    last_accessed_at DATETIME COMMENT '最后访问时间',
    access_count INT UNSIGNED DEFAULT 0 COMMENT '访问次数',
    
    -- 索引
    INDEX idx_cache_key (cache_key),
    INDEX idx_policy (policy_id),
    INDEX idx_status_expires (status, expires_at),
    INDEX idx_original_hash (original_url_hash),
    INDEX idx_last_access (last_accessed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='远程文件缓存表';

-- 缓存统计表（可选，用于监控）
CREATE TABLE remote_file_cache_stats (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    stat_date DATE NOT NULL,
    total_files INT UNSIGNED DEFAULT 0,
    total_size BIGINT UNSIGNED DEFAULT 0,
    thumbnail_count INT UNSIGNED DEFAULT 0,
    thumbnail_saved_size BIGINT UNSIGNED DEFAULT 0 COMMENT '缩略图节省的空间',
    hit_count INT UNSIGNED DEFAULT 0,
    miss_count INT UNSIGNED DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_date (stat_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='缓存统计表';
```

## 5. 核心接口

```go
// CacheManager 缓存管理器
type CacheManager interface {
    // Get 获取远程文件（自动缓存和缩略图生成）
    // 如果已缓存，返回缓存信息
    // 如果未缓存，根据AccessMode决定是代理还是重定向
    Get(ctx context.Context, url string) (*CacheEntry, error)
    
    // GetThumbnail 获取缩略图（如果配置了缩略图）
    // 如果缩略图不存在，会异步生成
    GetThumbnail(ctx context.Context, url string) (*CacheEntry, error)
    
    // GetDirect 获取原始文件（不生成缩略图）
    GetDirect(ctx context.Context, url string) (*CacheEntry, error)
    
    // Delete 删除缓存
    Delete(ctx context.Context, url string) error
    
    // GetStats 获取缓存统计
    GetStats(ctx context.Context) (*CacheStats, error)
    
    // CleanExpired 清理过期缓存
    CleanExpired(ctx context.Context) error
    
    // ReloadConfig 重新加载配置
    ReloadConfig(cfg CacheConfig) error
}

// CacheEntry 缓存条目
type CacheEntry struct {
    OriginalURL   string    `json:"original_url"`
    CacheKey      string    `json:"cache_key"`
    
    // 存储信息
    StorageType   string    `json:"storage_type"`
    Bucket        string    `json:"bucket"`
    ObjectKey     string    `json:"object_key"`
    
    // 访问URL（可能是签名URL或本地路径）
    AccessURL     string    `json:"access_url"`
    LocalPath     string    `json:"local_path,omitempty"` // 本地缓存路径
    
    // 文件信息
    ContentType   string    `json:"content_type"`
    Size          int64     `json:"size"`
    IsThumbnail   bool      `json:"is_thumbnail"`
    Width         int       `json:"width,omitempty"`
    Height        int       `json:"height,omitempty"`
    
    // 状态
    IsCached      bool      `json:"is_cached"`      // 是否已缓存
    IsProcessing  bool      `json:"is_processing"`  // 是否正在处理中
    
    // 元数据
    CachedAt      time.Time `json:"cached_at"`
    ExpiresAt     time.Time `json:"expires_at"`
}

// StorageBackend 存储后端接口
type StorageBackend interface {
    Type() string
    
    // Put 上传文件
    Put(ctx context.Context, key string, data io.Reader, size int64, contentType string) error
    
    // Get 获取文件（返回reader）
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    
    // GetURL 获取访问URL（可能是预签名URL）
    GetURL(ctx context.Context, key string) (string, error)
    
    // Exists 检查是否存在
    Exists(ctx context.Context, key string) (bool, error)
    
    // Delete 删除
    Delete(ctx context.Context, key string) error
}

// ThumbnailGenerator 缩略图生成器接口
type ThumbnailGenerator interface {
    // Generate 生成缩略图
    Generate(ctx context.Context, input io.Reader, output io.Writer, options ThumbnailOptions) error
    
    // GetDimensions 获取图片尺寸
    GetDimensions(ctx context.Context, input io.Reader) (width, height int, err error)
    
    // IsSupported 是否支持该格式
    IsSupported(format string) bool
}

type ThumbnailOptions struct {
    MaxWidth   int
    MaxHeight  int
    Quality    int
    Format     string
    PreserveAnimation bool
}
```

## 6. 处理流程

### 6.1 获取文件流程

```
1. 接收请求 Get(url)
   │
2. 检查数据库是否存在缓存记录
   │
3. 存在且未过期？
   ├── 是 → 返回缓存信息（AccessURL）
   │
   └── 否 → 检查AccessMode
            │
            ├── AccessMode = "redirect" 
            │   └── 返回原URL（不缓存）
            │
            └── AccessMode = "proxy"
                │
                ├── 检查是否匹配策略
                │   │
                │   └── 匹配 → 检查是否需要缩略图
                │       │
                │       ├── 需要 → 异步生成缩略图
                │       │           先返回原图（或临时占位）
                │       │
                │       └── 不需要 → 直接缓存原图
                │
                └── 不匹配 → 直接代理返回原图（不缓存）
```

### 6.2 缩略图生成流程

```
1. 匹配策略且需要缩略图（大小>阈值且格式不在排除列表）
   │
2. 下载远程文件（临时存储）
   │
3. 生成缩略图
   ├── 获取原图尺寸
   ├── 计算目标尺寸（保持比例）
   ├── 生成缩略图（转换格式）
   └── 保存缩略图到存储
   │
4. 更新数据库（缩略图记录）
   │
5. 删除临时文件
```

## 7. 配置示例

### 7.1 完整配置文件

```yaml
# config/remote_cache.yaml
remote_cache:
  enabled: true
  
  # 访问模式: proxy(代理) 或 redirect(重定向)
  access_mode: "proxy"
  
  # 缩略图配置
  thumbnail:
    enabled: true
    max_size: 524288        # 512KB
    max_width: 1920
    max_height: 1080
    quality: 85
    format: "webp"
    exclude_formats:
      - "gif"
      - "svg"
      - "ico"
    preserve_animation: false
  
  # 存储配置（S3）
  storage:
    type: "s3"
    bucket: "community-cache"
    region: "us-east-1"
    path_prefix: "remote/"
    credentials:
      access_key_id: "${AWS_ACCESS_KEY_ID}"
      secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
  
  # 策略
  policies:
    - id: "federation_avatar"
      name: "联邦头像"
      priority: 10
      rules:
        - field: "url"
          operator: "regex"
          value: ".*/users/[^/]+/avatar.*"
      ttl: "720h"
      storage_path: "avatars/"
      thumbnail:
        max_width: 200
        max_height: 200
    
    - id: "post_media"
      name: "帖子媒体"
      priority: 20
      rules:
        - field: "content_type"
          operator: "prefix"
          value: "image/"
      ttl: "168h"
      storage_path: "posts/"
    
    - id: "video"
      name: "视频文件"
      priority: 30
      rules:
        - field: "content_type"
          operator: "prefix"
          value: "video/"
      ttl: "720h"
      storage_path: "videos/"
      thumbnail:
        enabled: false
    
    - id: "default"
      name: "默认"
      priority: 999
      rules: []
      ttl: "168h"
      storage_path: "misc/"
```

### 7.2 环境变量配置

```bash
# 启用缓存
REMOTE_CACHE_ENABLED=true

# 存储类型
REMOTE_CACHE_STORAGE_TYPE=s3
REMOTE_CACHE_STORAGE_BUCKET=community-cache
REMOTE_CACHE_STORAGE_REGION=us-east-1
REMOTE_CACHE_STORAGE_PATH_PREFIX=remote/

# 凭证
REMOTE_CACHE_STORAGE_ACCESS_KEY_ID=xxx
REMOTE_CACHE_STORAGE_SECRET_ACCESS_KEY=xxx

# 缩略图配置
REMOTE_CACHE_THUMBNAIL_ENABLED=true
REMOTE_CACHE_THUMBNAIL_MAX_SIZE=524288
REMOTE_CACHE_THUMBNAIL_MAX_WIDTH=1920
REMOTE_CACHE_THUMBNAIL_MAX_HEIGHT=1080
REMOTE_CACHE_THUMBNAIL_QUALITY=85
REMOTE_CACHE_THUMBNAIL_FORMAT=webp
REMOTE_CACHE_THUMBNAIL_EXCLUDE_FORMATS=gif,svg,ico

# 访问模式
REMOTE_CACHE_ACCESS_MODE=proxy
```

## 8. API设计

### 8.1 内部API（Go接口）

```go
// 初始化
manager := remotecache.NewManager(config)

// 获取文件（自动处理缓存和缩略图）
entry, err := manager.Get(ctx, "https://example.com/image.jpg")
if err != nil {
    return err
}

// 如果已缓存，使用AccessURL
if entry.IsCached {
    // 返回 entry.AccessURL 给用户
} else if entry.LocalPath != "" {
    // 使用本地路径（代理模式）
    serveFile(entry.LocalPath)
} else {
    // 重定向到原URL
    redirect(entry.OriginalURL)
}
```

### 8.2 管理员API

```
# 获取缓存配置
GET /api/v1/admin/remote-cache/config

# 更新缓存配置
PUT /api/v1/admin/remote-cache/config

# 获取缓存统计
GET /api/v1/admin/remote-cache/stats

# 获取缓存列表
GET /api/v1/admin/remote-cache/entries?page=1&limit=50

# 删除特定缓存
DELETE /api/v1/admin/remote-cache/entries/:id

# 清理过期缓存
POST /api/v1/admin/remote-cache/clean

# 预缓存URL（批量）
POST /api/v1/admin/remote-cache/preload
Body: { "urls": ["http://...", "http://..."] }

# 刷新缓存（删除并重新获取）
POST /api/v1/admin/remote-cache/refresh
Body: { "url": "http://..." }
```

## 9. 实现计划

### 第一阶段：核心功能
1. 定义核心接口（CacheManager, StorageBackend, ThumbnailGenerator）
2. 实现规则匹配引擎
3. 实现本地存储后端
4. 实现基础缓存流程（下载→存储→记录）

### 第二阶段：存储后端
1. 实现S3存储后端
2. 实现MinIO存储后端
3. 实现阿里云OSS后端
4. 实现腾讯云COS后端

### 第三阶段：缩略图
1. 集成图片处理库（如 imaging 或 go-vips）
2. 实现缩略图生成器
3. 实现异步生成缩略图（goroutine + queue）
4. 支持 webp/jpeg/png 输出

### 第四阶段：优化
1. 实现缓存清理任务（定时清理过期文件）
2. 实现缓存统计
3. 管理API
4. 配置热重载

## 10. 关键设计决策

### 10.1 为什么选择单存储后端？
- 简化配置和管理
- 满足大多数场景需求
- 降低复杂度

### 10.2 为什么缩略图大小阈值是512KB？
- 可配置，用户可自定义
- 512KB是一个合理的平衡点：小图直接原图，大图生成缩略图节省带宽

### 10.3 为什么支持"直通"和"代理"两种模式？
- **直通模式**：适合CDN场景，未缓存时直接跳转原站
- **代理模式**：适合隐私/安全场景，所有流量经过服务器

### 10.4 缩略图格式选择？
- **webp**：默认，更好的压缩率
- **jpeg**：兼容性更好
- **png**：需要透明通道
- **gif**：被排除，避免动画gif转为静态图的问题

## 11. 评审检查清单

- [ ] 缩略图大小阈值（512KB）是否合理？是否需要按类型分别设置？
- [ ] 排除格式列表是否完整？（gif, svg, ico）
- [ ] 访问模式（proxy/redirect）是否满足需求？
- [ ] 是否需要支持视频缩略图（截图）？
- [ ] 策略优先级机制是否足够？
- [ ] 是否需要支持缓存预热（虽然用户说不需要，但管理员手动触发是否需要）？
- [ ] 缩略图是异步生成还是同步生成？
- [ ] 是否需要支持原图和缩略图的独立TTL？
