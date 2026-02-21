# 远程文件缓存机制设计方案

## 1. 设计目标

- **智能缓存策略**：根据文件类型、来源、大小等规则匹配不同的缓存策略
- **多存储后端**：支持本地磁盘、S3、MinIO、阿里云OSS等多种存储后端
- **分层缓存**：支持内存 -> 本地磁盘 -> 远程存储的多级缓存
- **TTL管理**：支持按规则设置不同的缓存过期时间
- **缓存预热**：支持热门资源的主动缓存
- **缓存清理**：支持LRU、LFU等淘汰策略

## 2. 架构设计

```
┌─────────────────────────────────────────────────────────────┐
│                      Cache Manager                          │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐ │
│  │ Rule Engine │  │  Metadata   │  │   Policy Manager    │ │
│  │   (匹配)     │  │   Store     │  │    (策略管理)        │ │
│  └─────────────┘  └─────────────┘  └─────────────────────┘ │
└────────────────────┬────────────────────────────────────────┘
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
┌─────────────┐ ┌─────────┐ ┌─────────────┐
│   Memory    │ │  Local  │ │   Remote    │
│    Cache    │ │  Disk   │ │   Storage   │
│  (LRU/LFU)  │ │         │ │(S3/OSS/...) │
└─────────────┘ └─────────┘ └─────────────┘
```

## 3. 核心组件

### 3.1 缓存策略 (CachePolicy)

```go
type CachePolicy struct {
    ID          string            // 策略ID
    Name        string            // 策略名称
    Priority    int               // 优先级（数字越小优先级越高）
    
    // 匹配规则
    Rules       []MatchRule       // 匹配规则列表
    
    // 存储配置
    Storage     StorageConfig     // 存储后端配置
    
    // 缓存配置
    TTL         time.Duration     // 缓存过期时间
    MaxSize     int64             // 最大缓存大小（字节）
    
    // 行为配置
    Behavior    CacheBehavior     // 缓存行为配置
}

type MatchRule struct {
    Field      string      // 匹配字段: url, content_type, file_extension, source_domain, file_size
    Operator   string      // 操作符: eq, ne, contains, prefix, suffix, regex, gt, lt, between
    Value      interface{} // 匹配值
}

type StorageConfig struct {
    Type       string            // 类型: local, s3, minio, aliyun_oss, tencent_cos
    Bucket     string            // 存储桶名称
    Region     string            // 区域
    PathPrefix string            // 路径前缀
    Config     map[string]string // 额外配置
}

type CacheBehavior struct {
    PreCache       bool          // 是否预缓存
    AsyncFetch     bool          // 是否异步获取
    Compress       bool          // 是否压缩
    CompressLevel  int           // 压缩级别
    Thumbnail      ThumbnailConfig // 缩略图配置
}

type ThumbnailConfig struct {
    Enabled    bool   // 是否生成缩略图
    MaxWidth   int    // 最大宽度
    MaxHeight  int    // 最大高度
    Quality    int    // 图片质量 (1-100)
    Format     string // 输出格式: webp, jpeg, png
}
```

### 3.2 预定义策略示例

```yaml
# 头像缓存策略
policies:
  - id: "avatar"
    name: "用户头像缓存"
    priority: 100
    rules:
      - field: "url"
        operator: "regex"
        value: ".*avatar.*\\.(jpg|jpeg|png|webp|gif)$"
      - field: "content_type"
        operator: "prefix"
        value: "image/"
    storage:
      type: "s3"
      bucket: "avatars"
      region: "us-east-1"
      path_prefix: "avatars/"
    ttl: "720h"  # 30天
    max_size: 5242880  # 5MB
    behavior:
      pre_cache: true
      async_fetch: true
      compress: true
      compress_level: 6
      thumbnail:
        enabled: true
        max_width: 400
        max_height: 400
        quality: 85
        format: "webp"

  # 帖子媒体文件缓存策略
  - id: "media"
    name: "帖子媒体缓存"
    priority: 200
    rules:
      - field: "source_domain"
        operator: "eq"
        value: "media.example.com"
    storage:
      type: "s3"
      bucket: "media"
      path_prefix: "posts/"
    ttl: "168h"  # 7天
    max_size: 52428800  # 50MB
    behavior:
      pre_cache: false
      async_fetch: true
      compress: false

  # 远程视频缓存策略
  - id: "video"
    name: "远程视频缓存"
    priority: 300
    rules:
      - field: "content_type"
        operator: "prefix"
        value: "video/"
      - field: "file_size"
        operator: "lt"
        value: 1073741824  # 1GB
    storage:
      type: "s3"
      bucket: "videos"
      path_prefix: "videos/"
    ttl: "720h"  # 30天
    max_size: 1073741824  # 1GB
    behavior:
      pre_cache: false
      async_fetch: false  # 视频不异步获取
      compress: false

  # 联邦实例头像缓存
  - id: "federation_avatar"
    name: "联邦头像缓存"
    priority: 150
    rules:
      - field: "url"
        operator: "regex"
        value: "https://[^/]+/users/[^/]+/avatar"
    storage:
      type: "local"
      path_prefix: "federation/avatars/"
    ttl: "168h"  # 7天
    max_size: 2097152  # 2MB
    behavior:
      pre_cache: true
      thumbnail:
        enabled: true
        max_width: 200
        max_height: 200
        quality: 80
        format: "webp"

  # 默认缓存策略
  - id: "default"
    name: "默认缓存策略"
    priority: 999
    rules: []  # 无规则，匹配所有
    storage:
      type: "local"
      path_prefix: "cache/"
    ttl: "24h"
    max_size: 104857600  # 100MB
```

## 4. 缓存管理器接口

```go
// CacheManager 缓存管理器接口
type CacheManager interface {
    // 获取缓存文件
    Get(ctx context.Context, url string, options GetOptions) (FileEntry, error)
    
    // 预缓存文件
    PreCache(ctx context.Context, url string, policyID string) error
    
    // 清理过期缓存
    CleanExpired(ctx context.Context) error
    
    // 获取缓存统计
    GetStats(ctx context.Context) CacheStats
    
    // 手动清理缓存
    Purge(ctx context.Context, pattern string) error
}

type FileEntry struct {
    URL         string
    LocalPath   string
    StorageType string
    Bucket      string
    ObjectKey   string
    Size        int64
    ContentType string
    CachedAt    time.Time
    ExpiresAt   time.Time
    AccessCount int64
    LastAccess  time.Time
    Metadata    map[string]string
}
```

## 5. 存储后端接口

```go
// StorageBackend 存储后端接口
type StorageBackend interface {
    // 存储类型
    Type() string
    
    // 上传文件
    Put(ctx context.Context, key string, data io.Reader, size int64, contentType string) error
    
    // 下载文件
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    
    // 删除文件
    Delete(ctx context.Context, key string) error
    
    // 检查文件是否存在
    Exists(ctx context.Context, key string) (bool, error)
    
    // 获取文件信息
    Stat(ctx context.Context, key string) (FileInfo, error)
    
    // 获取文件URL（可能是有签名的临时URL）
    GetURL(ctx context.Context, key string, expire time.Duration) (string, error)
    
    // 清理过期文件
    CleanExpired(ctx context.Context, before time.Time) error
}

type FileInfo struct {
    Key         string
    Size        int64
    ContentType string
    ModifiedAt  time.Time
}
```

## 6. 数据库表设计

```sql
-- 缓存策略表
CREATE TABLE cache_policies (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    priority INT NOT NULL DEFAULT 999,
    rules JSON NOT NULL,           -- 匹配规则 JSON
    storage_config JSON NOT NULL,  -- 存储配置 JSON
    ttl_seconds INT NOT NULL,      -- TTL（秒）
    max_size BIGINT NOT NULL,      -- 最大大小（字节）
    behavior_config JSON,          -- 行为配置 JSON
    enabled BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- 缓存文件元数据表
CREATE TABLE cache_entries (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    url VARCHAR(2048) NOT NULL UNIQUE,
    policy_id VARCHAR(64) NOT NULL,
    storage_type VARCHAR(32) NOT NULL,
    bucket VARCHAR(128),
    object_key VARCHAR(1024) NOT NULL,
    local_path VARCHAR(1024),
    size BIGINT NOT NULL,
    content_type VARCHAR(128),
    checksum VARCHAR(64),          -- 文件校验和
    cached_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    last_accessed_at DATETIME,
    access_count INT UNSIGNED DEFAULT 0,
    metadata JSON,                 -- 额外元数据
    status ENUM('active', 'expired', 'purged') DEFAULT 'active',
    INDEX idx_expires_at (expires_at),
    INDEX idx_policy (policy_id),
    INDEX idx_status (status),
    INDEX idx_last_access (last_accessed_at)
);

-- 缓存访问日志表（用于统计分析）
CREATE TABLE cache_access_logs (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    entry_id BIGINT UNSIGNED NOT NULL,
    accessed_at DATETIME NOT NULL,
    hit BOOLEAN NOT NULL,          -- 是否命中缓存
    response_time_ms INT,          -- 响应时间（毫秒）
    user_agent VARCHAR(512),
    referer VARCHAR(1024),
    INDEX idx_entry (entry_id),
    INDEX idx_time (accessed_at)
);

-- 存储后端配置表
CREATE TABLE storage_backends (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    type VARCHAR(32) NOT NULL,     -- local, s3, minio, aliyun_oss, etc.
    config JSON NOT NULL,          -- 配置 JSON
    default_bucket VARCHAR(128),
    region VARCHAR(64),
    endpoint VARCHAR(512),
    credentials_encrypted TEXT,    -- 加密存储的凭证
    enabled BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## 7. 配置热重载

```go
// 配置文件示例 config/cache.yaml
cache:
  enabled: true
  
  # 内存缓存配置
  memory_cache:
    enabled: true
    max_size: 1073741824  # 1GB
    ttl: "1h"
    eviction_policy: "lru"  # lru, lfu, random
  
  # 本地磁盘缓存配置
  local_cache:
    enabled: true
    base_path: "/var/cache/community"
    max_size: 10737418240  # 10GB
    cleanup_interval: "1h"
  
  # 存储后端配置
  storage_backends:
    - id: "s3_media"
      type: "s3"
      bucket: "community-media"
      region: "us-east-1"
      credentials:
        access_key_id: "${AWS_ACCESS_KEY_ID}"
        secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
    
    - id: "minio_cache"
      type: "minio"
      endpoint: "http://localhost:9000"
      bucket: "cache"
      credentials:
        access_key_id: "minioadmin"
        secret_access_key: "minioadmin"
  
  # 缓存策略
  policies:
    - id: "avatar"
      name: "头像缓存"
      priority: 100
      rules:
        - field: "url"
          operator: "regex"
          value: ".*avatar.*\\.(jpg|jpeg|png|webp)$"
      storage_backend: "s3_media"
      path_prefix: "avatars/"
      ttl: "720h"
      max_size: 5242880
```

## 8. 监控指标

```go
type CacheMetrics struct {
    // 命中率
    HitRate float64
    
    // 缓存统计
    TotalEntries    int64
    ActiveEntries   int64
    ExpiredEntries  int64
    TotalSize       int64
    
    // 访问统计
    TotalRequests   int64
    CacheHits       int64
    CacheMisses     int64
    
    // 性能指标
    AvgResponseTime time.Duration
    P95ResponseTime time.Duration
    P99ResponseTime time.Duration
    
    // 存储统计（按后端）
    StorageStats map[string]StorageMetrics
}
```

## 9. API设计

### 9.1 管理员API

```
# 获取缓存统计
GET /api/v1/admin/cache/stats

# 获取缓存策略列表
GET /api/v1/admin/cache/policies

# 创建/更新缓存策略
PUT /api/v1/admin/cache/policies/:id

# 删除缓存策略
DELETE /api/v1/admin/cache/policies/:id

# 手动清理缓存
POST /api/v1/admin/cache/purge
Body: { "pattern": "*.jpg", "older_than": "7d" }

# 获取缓存条目列表
GET /api/v1/admin/cache/entries?page=1&limit=50

# 删除特定缓存条目
DELETE /api/v1/admin/cache/entries/:id

# 预缓存URL
POST /api/v1/admin/cache/preload
Body: { "urls": ["http://...", "http://..."] }
```

### 9.2 内部使用API

```go
// 获取远程文件（自动缓存）
cacheManager.Get(ctx, "https://example.com/image.jpg", cache.GetOptions{
    ForceRefresh: false,
    Timeout: 30 * time.Second,
})

// 获取文件（不缓存）
cacheManager.Get(ctx, "https://example.com/image.jpg", cache.GetOptions{
    NoCache: true,
})
```

## 10. 实现计划

### 第一阶段：基础架构
1. 定义核心接口（CacheManager, StorageBackend）
2. 实现本地磁盘存储后端
3. 实现简单的规则匹配引擎
4. 实现基础缓存策略管理

### 第二阶段：存储后端
1. 实现S3存储后端
2. 实现MinIO存储后端
3. 实现阿里云OSS后端
4. 实现腾讯云COS后端

### 第三阶段：高级功能
1. 实现内存缓存层（LRU/LFU）
2. 实现缩略图生成
3. 实现压缩功能
4. 实现缓存预热

### 第四阶段：监控和管理
1. 实现缓存统计和指标
2. 实现管理API
3. 实现缓存清理任务
4. 实现配置热重载

## 11. 使用示例

```go
// 初始化缓存管理器
manager, err := cache.NewManager(cache.Config{
    Enabled: true,
    MemoryCache: cache.MemoryCacheConfig{
        Enabled: true,
        MaxSize: 1 << 30,  // 1GB
    },
    Policies: []cache.CachePolicy{
        {
            ID:       "avatar",
            Priority: 100,
            Rules: []cache.MatchRule{
                {Field: "url", Operator: "regex", Value: `.*avatar.*\.(jpg|png)$`},
            },
            Storage: cache.StorageConfig{
                Type:       "s3",
                Bucket:     "avatars",
                PathPrefix: "avatars/",
            },
            TTL:     30 * 24 * time.Hour,
            MaxSize: 5 << 20,  // 5MB
        },
    },
})

// 获取文件
entry, err := manager.Get(ctx, "https://remote.com/avatar.jpg")
if err != nil {
    log.Error("Failed to get file: %v", err)
    return
}

// 使用本地路径或URL
if entry.LocalPath != "" {
    // 使用本地缓存
    serveFile(entry.LocalPath)
} else {
    // 使用存储URL
    redirectTo(entry.GetURL())
}
```

## 评审要点

1. **规则引擎是否足够灵活？**
2. **多级缓存策略是否合理？**
3. **存储后端扩展性如何？**
4. **数据库设计是否满足性能需求？**
5. **API设计是否易于使用？**
6. **是否有遗漏的重要功能？**
