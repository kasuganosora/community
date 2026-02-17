# Phase 1: 核心功能补全

**时间预估**: 2-3 周  
**优先级**: 🔴 高  
**状态**: 🚧 进行中

## 📋 任务概览

Phase 1 主要目标是补全 Mastodon API 的核心功能，确保与主流 Mastodon 客户端的基本兼容性。

---

## ✅ 任务清单

### 1. 修正 API 版本号 🔴 高优先级
**预估时间**: 1 天  
**状态**: ⏳ 待开始

#### 子任务:
- [ ] 创建 `/api/v1/` 路由组
- [ ] 迁移 accounts 相关接口到 v1
  - [ ] `GET /api/v1/accounts/:id`
  - [ ] `GET /api/v1/accounts/verify_credentials`
  - [ ] `GET /api/v1/accounts/:id/statuses`
  - [ ] `GET /api/v1/accounts/:id/followers`
  - [ ] `GET /api/v1/accounts/:id/following`
- [ ] 迁移 statuses 相关接口到 v1
  - [ ] `POST /api/v1/statuses`
  - [ ] `GET /api/v1/statuses/:id`
  - [ ] `DELETE /api/v1/statuses/:id`
  - [ ] `PUT /api/v1/statuses/:id`
  - [ ] 所有 status 操作接口
- [ ] 迁移 timelines 相关接口到 v1
- [ ] 迁移 notifications 相关接口到 v1
- [ ] 迁移 conversations 相关接口到 v1
- [ ] 迁移 lists 相关接口到 v1
- [ ] 保持 v2 接口的向后兼容性（deprecated）

#### 验收标准:
- ✅ 所有标准接口使用 `/api/v1/` 路径
- ✅ 仅 filters 和 search 使用 `/api/v2/`
- ✅ 保留 v2 接口的向后兼容性
- ✅ 通过 Mastodon 客户端基本连接测试

---

### 2. 实现 Media API 🔴 高优先级
**预估时间**: 3-4 天  
**状态**: ⏳ 待开始

#### 子任务:
- [ ] 创建 Media 模型和数据库表
  ```sql
  - id (bigint, primary key)
  - account_id (bigint, foreign key)
  - status_id (bigint, foreign key, nullable)
  - file_name (varchar)
  - file_path (varchar)
  - file_size (bigint)
  - content_type (varchar)
  - width (int, nullable)
  - height (int, nullable)
  - duration (float, nullable)
  - thumbnail_path (varchar, nullable)
  - description (text, nullable)
  - created_at (timestamp)
  - updated_at (timestamp)
  ```
  
- [ ] 实现文件上传功能
  - [ ] 支持的格式：
    - 图片：JPEG, PNG, GIF, WebP
    - 视频：MP4, WebM, MOV
    - 音频：MP3, OGG, WAV
  - [ ] 文件大小限制：
    - 图片：10MB
    - 视频：40MB
    - 音频：40MB
  - [ ] 图片自动处理：
    - 生成缩略图
    - EXIF 方向校正
    - 尺寸优化

- [ ] 实现 API 端点：
  - [ ] `POST /api/v2/media` - 上传媒体文件
    - 参数：file, description, focus
    - 返回：MediaAttachment 对象
    - 支持异步处理（返回 202 Accepted）
  
  - [ ] `GET /api/v1/media/:id` - 获取媒体信息
    - 返回：MediaAttachment 对象
  
  - [ ] `PUT /api/v1/media/:id` - 更新媒体信息
    - 参数：description, focus
    - 返回：更新后的 MediaAttachment 对象

- [ ] 实现 MediaAttachment 序列化器
  ```json
  {
    "id": "123",
    "type": "image|video|audio|unknown",
    "url": "https://example.com/media/123.jpg",
    "preview_url": "https://example.com/media/123-small.jpg",
    "remote_url": null,
    "preview_remote_url": null,
    "text_url": "https://example.com/@user/123",
    "meta": {
      "focus": {"x": 0, "y": 0},
      "original": {"width": 1920, "height": 1080, "size": "1920x1080"},
      "small": {"width": 400, "height": 225, "size": "400x225"}
    },
    "description": "图片描述",
    "blurhash": "LEHV6nWB2yk8pyo0adR*.7kCMdnj"
  }
  ```

- [ ] 存储服务集成
  - [ ] 本地文件存储
  - [ ] 支持 S3/MinIO（可选）
  - [ ] 支持 CDN（可选）

#### 验收标准:
- ✅ 可以通过 API 上传图片、视频、音频
- ✅ 自动生成缩略图
- ✅ 返回正确的 MediaAttachment JSON 格式
- ✅ 可以在创建 status 时附加媒体
- ✅ 文件大小和类型验证正确

---

### 3. 实现 Search API v2 🔴 高优先级
**预估时间**: 2-3 天  
**状态**: ⏳ 待开始

#### 子任务:
- [ ] 创建 Search 控制器
- [ ] 实现 API 端点：
  - [ ] `GET /api/v2/search` - 通用搜索
    - 参数：
      - q: 搜索查询
      - type: accounts|statuses|hashtags
      - limit: 返回数量
      - offset: 偏移量
      - following: 仅搜索关注的人
      - account_id: 特定账户
      - max_id: 最大ID
      - min_id: 最小ID
    - 返回：
      ```json
      {
        "accounts": [...],
        "statuses": [...],
        "hashtags": [...]
      }
      ```

- [ ] 实现搜索逻辑：
  - [ ] 账户搜索
    - 按用户名、显示名搜索
    - 支持模糊匹配
    - 按关注状态排序
  
  - [ ] 内容搜索
    - 按嘟文内容全文搜索
    - 支持高级搜索语法
    - 权限过滤（仅公开或自己的）
  
  - [ ] 标签搜索
    - 按标签名搜索
    - 返回使用次数

- [ ] 集成搜索引擎（可选）：
  - [ ] 全文索引支持
  - [ ] Elasticsearch（可选）
  - [ ] Meilisearch（可选）

#### 验收标准:
- ✅ 可以搜索账户、嘟文、标签
- ✅ 返回结果符合 Mastodon API 格式
- ✅ 支持分页
- ✅ 权限控制正确

---

### 4. 实现标准 OAuth 2.0 🔴 高优先级
**预估时间**: 3-4 天  
**状态**: ⏳ 待开始

#### 子任务:
- [ ] 创建 OAuth 相关模型：
  - [ ] OAuthApplication (客户端应用)
    ```sql
    - id (bigint, primary key)
    - name (varchar)
    - client_id (varchar, unique)
    - client_secret (varchar)
    - redirect_uri (text)
    - scopes (varchar)
    - website (varchar, nullable)
    - created_at (timestamp)
    ```
  
  - [ ] OAuthToken (访问令牌)
    ```sql
    - id (bigint, primary key)
    - application_id (bigint, foreign key)
    - account_id (bigint, foreign key)
    - access_token (varchar, unique)
    - refresh_token (varchar, unique, nullable)
    - token_type (varchar, default 'Bearer')
    - scopes (varchar)
    - expires_at (timestamp)
    - created_at (timestamp)
    ```

- [ ] 实现 OAuth 端点：
  - [ ] `POST /api/v1/apps` - 创建应用
    - 参数：client_name, redirect_uris, scopes, website
    - 返回：{id, name, website, client_id, client_secret}
  
  - [ ] `GET /oauth/authorize` - 授权页面
    - 显示授权确认页面
    - 参数：response_type, client_id, redirect_uri, scope, state
  
  - [ ] `POST /oauth/authorize` - 确认授权
    - 生成授权码
    - 重定向到 redirect_uri
  
  - [ ] `POST /oauth/token` - 获取令牌
    - 支持授权码模式
    - 支持密码模式
    - 支持刷新令牌
    - 返回：
      ```json
      {
        "access_token": "...",
        "token_type": "Bearer",
        "scope": "read write",
        "created_at": 1234567890
      }
      ```
  
  - [ ] `POST /oauth/revoke` - 撤销令牌
  
  - [ ] `GET /api/v1/apps/verify_credentials` - 验证应用凭证

- [ ] 实现 Scope 权限系统：
  - [ ] read - 读取权限
    - read:accounts
    - read:statuses
    - read:notifications
    - read:lists
    - read:filters
    - read:search
    - read:follows
  
  - [ ] write - 写入权限
    - write:accounts
    - write:statuses
    - write:notifications
    - write:lists
    - write:filters
    - write:follows
  
  - [ ] admin - 管理权限
    - admin:read
    - admin:write

- [ ] 实现令牌中间件：
  - [ ] 验证 Bearer Token
  - [ ] 检查 Scope 权限
  - [ ] 令牌过期处理

#### 验收标准:
- ✅ 可以创建 OAuth 应用
- ✅ 完整的 OAuth 授权流程
- ✅ 可以通过令牌访问 API
- ✅ Scope 权限正确验证
- ✅ 与主流 Mastodon 客户端兼容

---

### 5. 实现 Follow Requests API 🟡 中优先级
**预估时间**: 2 天  
**状态**: ⏳ 待开始

#### 子任务:
- [ ] 创建 FollowRequest 模型
- [ ] 实现 API 端点：
  - [ ] `GET /api/v1/follow_requests` - 获取关注请求列表
    - 参数：limit, max_id, since_id
    - 返回：Account 数组
  
  - [ ] `POST /api/v1/follow_requests/:id/authorize` - 接受请求
    - 返回：Relationship 对象
  
  - [ ] `POST /api/v1/follow_requests/:id/reject` - 拒绝请求
    - 返回：Relationship 对象

- [ ] 实现私密账户逻辑：
  - [ ] 账户设置：locked (私密/公开)
  - [ ] 关注时创建 FollowRequest
  - [ ] 接受后创建 Follow 关系
  - [ ] 拒绝后删除请求

#### 验收标准:
- ✅ 私密账户可以接收关注请求
- ✅ 可以查看、接受、拒绝请求
- ✅ 返回格式符合标准

---

### 6. 实现 Favourites & Bookmarks 列表 🟡 中优先级
**预估时间**: 1-2 天  
**状态**: ⏳ 待开始

#### 子任务:
- [ ] 实现 Favourites API：
  - [ ] `GET /api/v1/favourites` - 获取收藏列表
    - 参数：limit, max_id, since_id, min_id
    - 返回：Status 数组
    - 需要 scope: read:favourites

- [ ] 实现 Bookmarks API：
  - [ ] `GET /api/v1/bookmarks` - 获取书签列表
    - 参数：limit, max_id, since_id, min_id
    - 返回：Status 数组
    - 需要 scope: read:bookmarks

- [ ] 添加分页支持
- [ ] 添加缓存优化

#### 验收标准:
- ✅ 可以获取收藏和书签列表
- ✅ 分页功能正常
- ✅ 权限验证正确

---

### 7. 完善账户相关接口 🟡 中优先级
**预估时间**: 2 天  
**状态**: ⏳ 待开始

#### 子任务:
- [ ] 实现缺失的账户端点：
  - [ ] `GET /api/v1/accounts/verify_credentials` - 验证凭证
    - 返回当前登录账户的完整信息
  
  - [ ] `PATCH /api/v1/accounts/update_credentials` - 更新凭证
    - 支持更新所有账户字段
    - 支持上传头像和头部图片
  
  - [ ] `GET /api/v1/accounts/:id/statuses` - 获取用户嘟文
    - 参数：max_id, since_id, min_id, limit, only_media, exclude_replies, pinned
  
  - [ ] `GET /api/v1/accounts/:id/followers` - 获取粉丝列表
  - [ ] `GET /api/v1/accounts/:id/following` - 获取关注列表
  - [ ] `GET /api/v1/accounts/:id/featured_tags` - 获取特色标签
  - [ ] `GET /api/v1/accounts/relationships` - 获取关系
  - [ ] `GET /api/v1/accounts/familiar_followers` - 熟悉的关注者

#### 验收标准:
- ✅ 所有账户接口符合标准
- ✅ 返回数据格式正确
- ✅ 分页功能正常

---

## 📊 进度追踪

| 任务 | 预估时间 | 实际时间 | 状态 | 完成度 |
|-----|---------|---------|------|--------|
| 1. 修正 API 版本号 | 1 天 | - | ⏳ 待开始 | 0% |
| 2. 实现 Media API | 3-4 天 | - | ⏳ 待开始 | 0% |
| 3. 实现 Search API v2 | 2-3 天 | - | ⏳ 待开始 | 0% |
| 4. 实现标准 OAuth 2.0 | 3-4 天 | - | ⏳ 待开始 | 0% |
| 5. 实现 Follow Requests API | 2 天 | - | ⏳ 待开始 | 0% |
| 6. 实现 Favourites & Bookmarks 列表 | 1-2 天 | - | ⏳ 待开始 | 0% |
| 7. 完善账户相关接口 | 2 天 | - | ⏳ 待开始 | 0% |

**总计**: 14-18 天

---

## 🧪 测试计划

### 单元测试
- [ ] Media 上传处理测试
- [ ] Search 查询测试
- [ ] OAuth 流程测试
- [ ] Follow Request 逻辑测试

### 集成测试
- [ ] API 端点完整性测试
- [ ] 权限验证测试
- [ ] 分页功能测试
- [ ] 文件上传测试

### 兼容性测试
- [ ] Tusky 客户端测试
- [ ] Ivory 客户端测试
- [ ] Pinafore 客户端测试
- [ ] 官方 Web 客户端测试

---

## 📝 注意事项

1. **向后兼容性**: 保持 v2 接口的向后兼容性
2. **性能优化**: 大文件上传需要异步处理
3. **安全性**: OAuth 实现必须严格遵循 RFC 6749
4. **数据迁移**: 添加新表时注意数据迁移
5. **文档更新**: 及时更新 API 文档

---

## 🎯 验收标准

Phase 1 完成的标志：
- ✅ 所有高优先级任务完成
- ✅ 通过 Mastodon 客户端基本功能测试
- ✅ API 兼容性评分达到 80+
- ✅ 无严重 bug
- ✅ 代码覆盖率 > 60%

---

**创建时间**: 2025-02-17  
**更新时间**: 2025-02-17  
**负责人**: @community-team
