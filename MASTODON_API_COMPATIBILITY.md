# Mastodon API 兼容性分析报告

## 📊 总体评估

**兼容性评分**: 60/100  
**已实现接口数**: ~45个  
**缺失核心接口数**: ~35个

---

## ✅ 已实现的 Mastodon API

### 1. Accounts (账户) - 60% 实现
#### 已实现:
- ✅ `GET /api/v2/accounts/:id` - 获取账户信息
- ✅ `POST /api/v2/accounts/register` - 注册账户
- ✅ `POST /api/v2/accounts/login` - 登录
- ✅ `GET /api/v2/accounts/verify_email` - 验证邮箱
- ✅ `POST /api/v2/accounts/reset_password` - 重置密码
- ✅ `PUT /api/v2/accounts/me` - 更新账户信息
- ✅ `GET /api/v2/accounts/search` - 搜索账户

#### ❌ 缺失:
- ❌ `GET /api/v1/accounts/verify_credentials` - 验证凭证（标准端点）
- ❌ `GET /api/v1/accounts/:id/statuses` - 获取用户嘟文
- ❌ `GET /api/v1/accounts/:id/followers` - 获取粉丝列表
- ❌ `GET /api/v1/accounts/:id/following` - 获取关注列表
- ❌ `GET /api/v1/accounts/:id/featured_tags` - 获取特色标签
- ❌ `POST /api/v1/accounts/:id/block` - 屏蔽用户
- ❌ `POST /api/v1/accounts/:id/unblock` - 取消屏蔽
- ❌ `POST /api/v1/accounts/:id/mute` - 静音用户
- ❌ `POST /api/v1/accounts/:id/unmute` - 取消静音
- ❌ `GET /api/v1/accounts/relationships` - 获取关系
- ❌ `GET /api/v1/accounts/familiar_followers` - 熟悉的关注者

### 2. Statuses (嘟文) - 70% 实现
#### 已实现:
- ✅ `POST /api/v2/statuses` - 创建嘟文
- ✅ `GET /api/v2/statuses/:id` - 查看嘟文
- ✅ `DELETE /api/v2/statuses/:id` - 删除嘟文
- ✅ `PUT /api/v2/statuses/:id` - 更新嘟文
- ✅ `POST /api/v2/statuses/:id/favourite` - 收藏
- ✅ `POST /api/v2/statuses/:id/unfavourite` - 取消收藏
- ✅ `POST /api/v2/statuses/:id/reblog` - 转发
- ✅ `POST /api/v2/statuses/:id/unreblog` - 取消转发
- ✅ `POST /api/v2/statuses/:id/pin` - 置顶
- ✅ `POST /api/v2/statuses/:id/unpin` - 取消置顶
- ✅ `POST /api/v2/statuses/:id/bookmark` - 书签
- ✅ `POST /api/v2/statuses/:id/unbookmark` - 取消书签
- ✅ `POST /api/v2/statuses/:id/mute` - 静音对话
- ✅ `POST /api/v2/statuses/:id/unmute` - 取消静音
- ✅ `GET /api/v2/statuses/:id/favourited_by` - 收藏者列表
- ✅ `GET /api/v2/statuses/:id/reblogged_by` - 转发者列表
- ✅ `GET /api/v2/statuses/:id/history` - 编辑历史
- ✅ `GET /api/v2/statuses/:id/source` - 源文本

#### ❌ 缺失:
- ❌ 标准端点应为 `/api/v1/statuses/*` 而非 `/api/v2/statuses/*`

### 3. Timelines (时间线) - 100% 实现
#### 已实现:
- ✅ `GET /api/v2/timelines/home` - 主页时间线
- ✅ `GET /api/v2/timelines/public` - 公共时间线
- ✅ `GET /api/v2/timelines/tag/:tag` - 标签时间线
- ✅ `GET /api/v2/timelines/list/:id` - 列表时间线

### 4. Streaming (流式传输) - 100% 实现
#### 已实现:
- ✅ `GET /api/v2/streaming/user` - 用户流
- ✅ `GET /api/v2/streaming/public` - 公共流
- ✅ `GET /api/v2/streaming/public/local` - 本地公共流
- ✅ `GET /api/v2/streaming/public/remote` - 远程公共流
- ✅ `GET /api/v2/streaming/hashtag` - 标签流
- ✅ `GET /api/v2/streaming/list` - 列表流
- ✅ `GET /api/v2/streaming/direct` - 私信流
- ✅ `GET /api/v2/streaming/notifications` - 通知流

### 5. Lists (列表) - 100% 实现
#### 已实现:
- ✅ `GET /api/v2/lists` - 获取列表
- ✅ `POST /api/v2/lists` - 创建列表
- ✅ `GET /api/v2/lists/:id` - 查看列表
- ✅ `PUT /api/v2/lists/:id` - 更新列表
- ✅ `DELETE /api/v2/lists/:id` - 删除列表
- ✅ `GET /api/v2/lists/:id/accounts` - 列表成员
- ✅ `POST /api/v2/lists/:id/accounts` - 添加成员
- ✅ `DELETE /api/v2/lists/:id/accounts` - 移除成员

### 6. Notifications (通知) - 20% 实现
#### 已实现:
- ✅ `GET /api/v2/notifications` - 获取通知列表
- ✅ 流式通知

#### ❌ 缺失:
- ❌ `GET /api/v1/notifications/:id` - 查看单个通知
- ❌ `POST /api/v1/notifications/:id/dismiss` - 忽略通知
- ❌ `POST /api/v1/notifications/clear` - 清空通知
- ❌ `GET /api/v1/notifications/policy` - 通知策略
- ❌ `POST /api/v1/notifications/policy` - 更新通知策略

### 7. Conversations (对话) - 80% 实现
#### 已实现:
- ✅ `GET /api/v2/conversations` - 对话列表
- ✅ `DELETE /api/v2/conversations/:id` - 删除对话
- ✅ `POST /api/v2/conversations/:id/read` - 标记已读

### 8. Filters (过滤器) - 100% 实现
#### 已实现:
- ✅ `GET /api/v2/filters` - 过滤器列表
- ✅ `POST /api/v2/filters` - 创建过滤器
- ✅ `GET /api/v2/filters/:id` - 查看过滤器
- ✅ `PUT /api/v2/filters/:id` - 更新过滤器
- ✅ `DELETE /api/v2/filters/:id` - 删除过滤器
- ✅ `GET /api/v2/filters/:id/keywords` - 关键词列表
- ✅ `POST /api/v2/filters/:filter_id/keywords` - 添加关键词
- ✅ `PUT /api/v2/filters/keywords/:id` - 更新关键词
- ✅ `DELETE /api/v2/filters/keywords/:id` - 删除关键词

### 9. Instance (实例) - 100% 实现
#### 已实现:
- ✅ `GET /api/v2/instance` - 实例信息
- ✅ `GET /api/v2/instance/rules` - 实例规则

### 10. Reports (举报) - 100% 实现
#### 已实现:
- ✅ `POST /api/v2/reports` - 创建举报
- ✅ 管理员举报管理接口完整

### 11. Markers (标记) - 100% 实现
#### 已实现:
- ✅ `GET /api/v2/markers` - 获取标记
- ✅ `POST /api/v2/markers` - 保存标记

---

## ❌ 完全缺失的重要 API

### 1. Media (媒体) - 0% 实现
- ❌ `POST /api/v2/media` - 上传媒体
- ❌ `GET /api/v1/media/:id` - 获取媒体
- ❌ `PUT /api/v1/media/:id` - 更新媒体

### 2. Polls (投票) - 50% 实现
- ✅ `POST /api/v2/polls/:id/votes` - 投票
- ✅ `GET /api/v2/polls/:id` - 查看投票
- ❌ 缺少标准端点路径 `/api/v1/polls/*`

### 3. Search (搜索) - 10% 实现
- ✅ 仅账户搜索
- ❌ `GET /api/v2/search` - 通用搜索（标准端点）
- ❌ 内容搜索、标签搜索

### 4. Follow Requests (关注请求) - 0% 实现
- ❌ `GET /api/v1/follow_requests` - 关注请求列表
- ❌ `POST /api/v1/follow_requests/:id/authorize` - 接受请求
- ❌ `POST /api/v1/follow_requests/:id/reject` - 拒绝请求

### 5. Blocks & Mutes (屏蔽和静音) - 0% 实现
- ❌ `GET /api/v1/blocks` - 屏蔽列表
- ❌ `GET /api/v1/mutes` - 静音列表
- ❌ `GET /api/v1/domain_blocks` - 域名屏蔽列表
- ❌ `POST /api/v1/domain_blocks` - 屏蔽域名
- ❌ `DELETE /api/v1/domain_blocks` - 取消屏蔽域名

### 6. Favourites & Bookmarks (收藏和书签) - 0% 实现
- ❌ `GET /api/v1/favourites` - 收藏列表
- ❌ `GET /api/v1/bookmarks` - 书签列表

### 7. Trends (趋势) - 0% 实现
- ❌ `GET /api/v1/trends/statuses` - 趋势嘟文
- ❌ `GET /api/v1/trends/tags` - 趋势标签
- ❌ `GET /api/v1/trends/links` - 趋势链接

### 8. Suggestions (推荐) - 0% 实现
- ❌ `GET /api/v2/suggestions` - 推荐账户
- ❌ `DELETE /api/v1/suggestions/:account_id` - 移除推荐

### 9. Announcements (公告) - 0% 实现
- ❌ `GET /api/v1/announcements` - 公告列表
- ❌ `POST /api/v1/announcements/:id/dismiss` - 忽略公告
- ❌ `PUT /api/v1/announcements/:id/reactions/:name` - 添加反应
- ❌ `DELETE /api/v1/announcements/:id/reactions/:name` - 删除反应

### 10. Featured Tags (特色标签) - 0% 实现
- ❌ `GET /api/v1/featured_tags` - 特色标签列表
- ❌ `POST /api/v1/featured_tags` - 创建特色标签
- ❌ `DELETE /api/v1/featured_tags/:id` - 删除特色标签
- ❌ `GET /api/v1/featured_tags/suggestions` - 建议特色标签

### 11. Followed Tags (关注的标签) - 0% 实现
- ❌ `GET /api/v1/followed_tags` - 关注的标签列表
- ❌ `POST /api/v1/tags/:id/follow` - 关注标签
- ❌ `POST /api/v1/tags/:id/unfollow` - 取消关注标签
- ❌ `GET /api/v1/tags/:id` - 查看标签信息

### 12. Directory (目录) - 0% 实现
- ❌ `GET /api/v1/directory` - 用户目录

### 13. Endorsements (背书) - 0% 实现
- ❌ `GET /api/v1/endorsements` - 背书列表
- ❌ `POST /api/v1/accounts/:id/pin` - 背书用户
- ❌ `POST /api/v1/accounts/:id/unpin` - 取消背书

### 14. Scheduled Statuses (定时嘟文) - 部分实现
- ✅ `GET /api/v2/scheduled_statuses` - 列表
- ✅ `GET /api/v2/scheduled_statuses/:id` - 查看
- ✅ `POST /api/v2/scheduled_statuses/:id` - 更新
- ❌ `DELETE /api/v1/scheduled_statuses/:id` - 删除
- ❌ 缺少标准端点路径

### 15. Preferences (偏好设置) - 0% 实现
- ❌ `GET /api/v1/preferences` - 获取偏好设置

### 16. Custom Emojis (自定义表情) - 0% 实现
- ❌ `GET /api/v1/custom_emojis` - 自定义表情列表

### 17. Apps (应用) - 0% 实现
- ❌ `POST /api/v1/apps` - 创建应用
- ❌ `GET /api/v1/apps/verify_credentials` - 验证应用凭证

### 18. OAuth - 0% 实现
- ❌ 标准 OAuth 流程端点

---

## ⚠️ API 规范问题

### 1. 版本号问题
**问题**: 项目使用 `/api/v2/` 作为主要路径，但 Mastodon 标准使用 `/api/v1/`
- 大部分接口应在 `/api/v1/` 下
- 只有少数新特性在 `/api/v2/` 下（如 filters v2, search v2）

**建议**: 
- 保持 `/api/v1/` 作为标准路径
- 仅对确实有 v2 版本的接口使用 `/api/v2/`

### 2. OAuth 认证
**问题**: 缺少标准 OAuth 2.0 实现
- 当前使用自定义登录接口
- 缺少标准 token 端点

**建议**: 实现标准 OAuth 2.0 流程

### 3. 响应格式
需要验证所有接口响应是否符合 Mastodon 的标准 JSON 格式

---

## 📋 优先级建议

### 🔴 高优先级 (核心功能)
1. **Media API** - 媒体上传是核心功能
2. **Search API** - 完整的搜索功能
3. **标准 OAuth** - 第三方应用兼容
4. **Follow Requests** - 私密账户功能
5. **Favourites/Bookmarks 列表** - 内容管理

### 🟡 中优先级 (用户体验)
6. **Blocks/Mutes 管理** - 用户管理
7. **Trends** - 内容发现
8. **Followed Tags** - 标签管理
9. **Featured Tags** - 个人资料增强
10. **Preferences** - 设置管理

### 🟢 低优先级 (增强功能)
11. **Suggestions** - 推荐系统
12. **Directory** - 用户发现
13. **Endorsements** - 背书功能
14. **Custom Emojis** - 表情管理
15. **Announcements** - 公告系统

---

## 🎯 兼容性改进路线图

### Phase 1: 核心功能补全 (2-3周)
- [ ] 实现标准 OAuth 2.0
- [ ] 实现 Media API
- [ ] 实现 Search API v2
- [ ] 实现 Follow Requests API
- [ ] 修正 API 路径版本号

### Phase 2: 用户体验提升 (2-3周)
- [ ] 实现 Blocks/Mutes API
- [ ] 实现 Favourites/Bookmarks 列表
- [ ] 实现 Followed Tags API
- [ ] 实现 Trends API
- [ ] 实现 Featured Tags API

### Phase 3: 高级功能 (2-4周)
- [ ] 实现 Suggestions API
- [ ] 实现 Directory API
- [ ] 实现 Endorsements API
- [ ] 实现 Announcements API
- [ ] 实现 Preferences API

### Phase 4: 完善 (持续)
- [ ] 自定义表情支持
- [ ] 完整的管理员 API
- [ ] ActivityPub 联邦功能增强
- [ ] 性能优化
- [ ] 文档完善

---

## 📊 兼容性矩阵

| 功能模块 | 实现进度 | 标准兼容性 | 优先级 |
|---------|---------|-----------|--------|
| Accounts | 60% | 低 | 🔴 高 |
| Statuses | 70% | 中 | 🔴 高 |
| Timelines | 100% | 中 | ✅ 完成 |
| Streaming | 100% | 中 | ✅ 完成 |
| Notifications | 20% | 低 | 🔴 高 |
| Media | 0% | - | 🔴 高 |
| Search | 10% | 低 | 🔴 高 |
| Lists | 100% | 中 | ✅ 完成 |
| Filters | 100% | 高 | ✅ 完成 |
| Instance | 100% | 中 | ✅ 完成 |
| OAuth | 0% | - | 🔴 高 |

---

## 🔍 详细接口对比

### 当前实现 vs Mastodon 标准

| 当前路径 | Mastodon 标准 | 状态 |
|---------|--------------|------|
| `/api/v2/accounts/:id` | `/api/v1/accounts/:id` | ⚠️ 版本不匹配 |
| `/api/v2/statuses` | `/api/v1/statuses` | ⚠️ 版本不匹配 |
| `/api/v2/timelines/*` | `/api/v1/timelines/*` | ⚠️ 版本不匹配 |
| `/api/v2/filters/*` | `/api/v2/filters/*` | ✅ 正确 |
| `/api/v2/lists/*` | `/api/v1/lists/*` | ⚠️ 版本不匹配 |
| 缺失 | `/api/v1/media/*` | ❌ 缺失 |
| 缺失 | `/api/v2/search` | ❌ 缺失 |
| 缺失 | `/api/v1/follow_requests/*` | ❌ 缺失 |

---

## 💡 建议

1. **立即修复**: API 版本号问题
2. **优先实现**: Media、Search、OAuth
3. **逐步补全**: 其他缺失接口
4. **持续测试**: 与主流 Mastodon 客户端兼容性测试

---

生成时间: 2025-02-17  
项目: Community (基于 Mastodon API 规范)  
版本: 1.0
