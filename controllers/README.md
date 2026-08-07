# controllers (HTTP 控制器)

Mastodon API 兼容的 HTTP 控制器层，负责处理所有 `/api/v1`、`/api/v2`、`/oauth` 等 RESTful 端点。本包基于 [Gin](https://github.com/gin-gonic/gin) 框架，将 HTTP 请求解析、参数校验、业务调用与响应序列化组织为扁平的 handler 函数，并按业务域拆分到 `controllers/api/` 下的独立子模块中。

## 目录结构

```
controllers/
├── abort.go              # AbortSentinel 控制流机制（panic + recovery）
├── helpers.go            # 所有 handler 共用的通用辅助函数
├── helpers_test.go
├── apierrors/
│   ├── http_error.go     # HttpError 统一错误类型
│   └── http_error_test.go
└── api/
    ├── account/          # 账户、认证、关系、邀请码、推送、公告
    ├── admin/            # 管理员：账户/状态/表情/中继/统计/Matrix/缩略图
    ├── common/           # 实例信息、验证码配置
    ├── contact/          # 联系人、Matrix 用户查询
    ├── conversation/     # 会话列表、已读、删除
    ├── favourite/        # 收藏列表、书签列表
    ├── filter/           # 过滤器及其关键词的 CRUD
    ├── follow_request/   # 关注请求列表、授权、拒绝
    ├── group/            # 群组、成员、消息、Matrix 桥接
    ├── instance/         # 实例对等节点（peers）
    ├── list/             # 列表 CRUD、成员管理、关注者
    ├── marker/           # 读写位置标记
    ├── media/            # 媒体上传（同步/异步）、更新、删除
    ├── oauth/            # OAuth 应用注册、授权、令牌、撤销
    ├── report/           # 举报创建与管理
    ├── rule/             # 实例规则（公开 + 管理）
    ├── search/           # Mastodon v2 搜索
    ├── status/           # 帖子全生命周期（创建/转嘟/收藏/投票/翻译…）
    ├── streaming/        # WebSocket 流式接口
    ├── timelines/        # 主页/公共/标签/列表时间线、通知
    └── trends/           # 热门标签、帖子、链接
```

## 核心功能

- **统一的控制流终止机制**：通过 `AbortSentinel` panic 实现类似"提前返回"的语义，避免层层嵌套的 `if err != nil`。
- **通用错误处理**：`AbortIfError` / `AbortIf` 自动将 `gorm.ErrRecordNotFound`、`*apierrors.HttpError` 映射为对应的 HTTP 状态码。
- **认证上下文访问**：`GetAccount` / `MustGetAccount` 从 Gin context 中安全取出已认证账户。
- **泛型资源加载**：`GetIdFromContext[T]` 通过泛型从 URL `:id` 参数加载任意 GORM 模型。
- **按业务域分模块**：21 个子模块各司其职，handler 为包级函数（无全局状态），由 `router.go` 统一注册。

## 架构 / 结构

### 顶层包（`controllers/`）

所有 handler 共用的基础设施。

| 文件 | 职责 |
|------|------|
| `abort.go` | 定义 `AbortSentinel`、`AbortPanic()`、`AbortRecoveryMiddleware()`。handler 在写完错误响应后调用 `AbortPanic()` 终止执行，由中间件静默捕获，避免被 `gin.Recovery()` 当作真实 panic 打印堆栈 |
| `helpers.go` | `GetIdFromContext[T]`（泛型按 id 加载模型）、`GetAccount` / `MustGetAccount`（认证账户访问）、`AbortIfError` / `AbortIf` / `AbortIfNotFoundAndOtherError`（统一错误→HTTP 响应映射） |
| `apierrors/http_error.go` | `HttpError` 类型，携带 `HttpCode` 与内部 error；提供 `NewHttpErrorWithCode`、`WarpToHttpError`（自动识别 `gorm.ErrRecordNotFound`→404）等构造函数 |

> **中间件注册顺序约定**：`AbortRecoveryMiddleware()` 必须在 `gin.Recovery()` **之后**注册（成为更内层中间件），使其 `defer recover()` 先于 gin 的执行。参见 `router.go` 中 `SetRouter` 的实现与 `helpers_test.go` 中的 `TestAbortRecoveryMiddleware_ProductionOrder`。

### `controllers/api/` 子模块一览

| 子模块 | 包名 | 职责说明 |
|--------|------|----------|
| **account** | `account` | 账户全生命周期：注册 / 登录 / 登出、邮箱验证与找回密码、凭证查询与更新、密码修改、关注/取关、屏蔽/静音/置顶、域名屏蔽、邀请码管理、推送订阅、公告（列表/点踩/反应）、精选标签、建议、偏好设置、自定义表情、目录、关系查询 |
| **admin** | `admin` | 管理员后台：账户审核（启用/批准/拒绝/解禁）、状态操作与删除、自定义表情 CRUD、联邦中继管理、实例统计（dimensions/measures/retention）、Matrix 桥接状态与控制、缩略图管理与配置 |
| **common** | `common` | 跨域公共端点：`GET /api/v1/instance`（与 v2 共用）、验证码配置读取（公开）、验证码配置更新（管理员） |
| **contact** | `contact` | 联系人系统：添加/删除/拉黑联系人、好友请求（待审/接受/拒绝）、Matrix 用户查询、本地用户搜索 |
| **conversation** | `conversation` | 私信会话：列表、标记已读、删除会话 |
| **favourite** | `favourite` | 收藏与书签的时间线列表读取（`GET /favourites`、`GET /bookmarks`） |
| **filter** | `filter` | 内容过滤器：过滤器组 CRUD、过滤器关键词的增删改查与列表 |
| **follow_request** | `follow_request` | 关注请求：列表、授权（authorize）、拒绝（reject） |
| **group** | `group` | 群组聊天：创建/删除群组、成员邀请与移除、消息收发、Matrix 群组桥接 |
| **instance** | `instance` | 实例联邦信息：`GET /api/v1/instance/peers`（已知对等节点列表） |
| **list** | `list` | 用户列表（Timelines List）：创建/更新/删除、关注/取关、成员增删查、关注者查询 |
| **marker** | `marker` | 读写位置标记：保存与读取各时间线的最后阅读位置 |
| **media** | `media` | 媒体附件：同步上传（`POST /media`）、异步上传（`POST /media/v2`，v2）、更新元数据、删除；内置类型/大小校验与并发限流（`MaxAsyncUploads`） |
| **oauth** | `oauth` | OAuth 2.0 全流程：应用注册（`POST /apps`）、授权页与授权（`/oauth/authorize`）、令牌签发与撤销（`/oauth/token`、`/oauth/revoke`）、应用凭证验证 |
| **report** | `report` | 举报：用户创建举报、管理员列表/详情/更新/删除、分配/取消分配/解决/重新打开 |
| **rule** | `rule` | 实例规则：公开列表与详情（`GET /v2/instance/rules`）、管理员 CRUD（`POST /admin/rule` 等） |
| **search** | `search` | Mastodon 搜索 v2（`GET /api/v2/search`）：支持 accounts / statuses / hashtags，含分页、resolve、following 过滤 |
| **status** | `status` | 帖子核心操作：创建/删除/更新、上下文（context）、转嘟/取消、收藏/取消、置顶、静音、书签、投票、源码、编辑历史、定时发布管理、翻译 |
| **streaming** | `streaming` | WebSocket 实时流：user / direct / notifications / public（local/remote/all）/ hashtag（local/remote/all）/ list，以及健康检查 |
| **timelines** | `timelines` | 时间线聚合：home / public / tag / list / mention，以及通知时间线（列表、详情、清除、dismiss、通知策略） |
| **trends** | `trends` | 趋势内容：热门标签（`GET /trends/tags`）、热门帖子（`/trends/statuses`）、热门链接（`/trends/links`） |

## 用法示例

### 路由注册

所有 handler 均在根目录的 `router.go` → `SetRouter(app)` 中注册。路由按「公开 / 可选认证 / 强制认证 / 管理员」分层，并通过 `middleware.CheckScopeMiddleware(...)` 细粒度控制 OAuth scope。

```go
// router.go（节选）
func SetRouter(app gin.IRouter) {
    // 捕获 AbortSentinel 控制流 panic（必须在 gin.Recovery 之后）
    app.Use(controllers.AbortRecoveryMiddleware())

    v1ApiGroup := app.Group("/api").Group("/v1")
    v2ApiGroup := app.Group("/api").Group("/v2")

    // 公开接口（无需认证）
    v1ApiGroup.POST("/accounts/register", account.Register)
    v1ApiGroup.GET("/instance", common.GetInstanceDetail)
    v1ApiGroup.GET("/trends/tags", trends.ListTags)

    // 可选认证（登录或不登录均可访问）
    maybeAuth := v1ApiGroup.Group("", middleware.AuthMiddleware())
    maybeAuth.GET("/statuses/:id", status.Show)
    maybeAuth.GET("/timelines/public", timelines.Public)

    // 强制认证（需登录 + OAuth scope 校验）
    auth := v1ApiGroup.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
    auth.Group("", middleware.CheckScopeMiddleware(middleware.Scope{
        Type:  models.AppScopeTypeWrite,
        Value: models.AppScopeWriteStatuses,
    })).
        POST("/statuses", status.Create).
        DELETE("/statuses/:id", status.Delete).
        POST("/statuses/:id/reblog", status.Reblog).
        POST("/statuses/:id/favourite", status.Favourite)

    // 媒体上传（写入 scope + 每用户每分钟 30 次限流）
    mediaGroup := v1ApiGroup.Group("",
        middleware.AuthMiddleware(),
        middleware.CheckAuthMiddleware(),
        middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/media", 30))
    mediaGroup.Group("", middleware.CheckScopeMiddleware(middleware.Scope{
        Type:  models.AppScopeTypeWrite,
        Value: models.AppScopeWriteMedia,
    })).
        POST("/media", media.Upload).
        POST("/media/v2", media.UploadAsync).
        PUT("/media/:id", media.Update)

    // Search API v2（可选认证）
    v2ApiGroup.Group("", middleware.AuthMiddleware()).GET("/search", search.Search)
}
```

### Handler 编写规范

子模块 handler 是普通的 `func(c *gin.Context)` 包级函数，通过 `controllers` 顶层包的辅助函数处理认证与错误。

```go
// controllers/api/status/create.go
package status

import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/controllers"
    "github.com/kasuganosora/community/db"
    "github.com/kasuganosora/community/models"
    "net/http"
)

func Create(c *gin.Context) {
    var req CreateStatusRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // 从 context 取出已认证账户（未登录则 abort + panic 哨兵）
    currentAccount := controllers.MustGetAccount(c)

    status := &models.Status{
        AccountId: currentAccount.Id,
        Content:   models.StatusContent{Content: req.Status, SpoilerText: req.SpoilerText},
        Sensitive: req.Sensitive,
        Language:  req.Language,
    }

    err := status.SetVisibilityByString(req.Visibility)
    controllers.AbortIfError(c, err) // 出错时自动写入 HTTP 响应并 AbortPanic()

    // ... 业务逻辑 ...

    c.JSON(http.StatusOK, serializer.ToStatus(status, currentAccount))
}
```

### 调用真实 API

```bash
# 创建帖子（需 Bearer token，scope: write:statuses）
curl -X POST https://example.com/api/v1/statuses \
  -H "Authorization: Bearer <ACCESS_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"status":"hello world","visibility":"public"}'

# 搜索（可选认证）
curl "https://example.com/api/v2/search?q=foo&type=accounts&limit=20"

# 上传媒体（scope: write:media，限流 30 次/分钟）
curl -X POST https://example.com/api/v1/media \
  -H "Authorization: Bearer <ACCESS_TOKEN>" \
  -F "file=@photo.jpg" -F "description=a photo"
```

### 错误处理

使用 `apierrors.HttpError` 携带自定义 HTTP 状态码，由 `AbortIfError` 统一转换：

```go
import "github.com/kasuganosora/community/controllers/apierrors"

// 在业务层返回带状态码的错误
err := apierrors.NewHttpErrorWithCodeAndMessage(nil, 422, "account is suspended")
controllers.AbortIfError(c, err)
// → HTTP 422 {"error":"account is suspended"}，随后 panic(abortSentinel) 终止 handler

// WarpToHttpError 自动识别 gorm.ErrRecordNotFound → 404
err := db.First(&account, id).Error
controllers.AbortIfError(c, apierrors.WarpToHttpError(err))
```

## 依赖关系

```
controllers
├── gin-gonic/gin                      # HTTP 框架
├── gorm.io/gorm                       # ORM（GetIdFromContext 泛型加载、错误识别）
├── github.com/pkg/errors              # 错误包装与 errors.Is/As
└── apierrors (子包)                   # HttpError 类型
        │
        ▼
controllers/api/* （各子模块）
├── controllers                        # 通用辅助函数
├── db                                 # 数据库连接（*gorm.DB 全局实例）
├── models                             # 数据模型（Account / Status / ...）
├── serializer                         # Mastodon JSON 序列化器
├── feed (home/public/tag/list/...)    # 时间线内容聚合
├── search                             # 搜索引擎抽象
├── media / storage                    # 媒体处理与对象存储
├── cache                              # 缓存（trends 等）
├── systemsettings                     # 系统设置读写
├── captcha                            # 验证码（注册流程）
├── email                              # 邮件发送
├── invitecode                         # 邀请码校验
├── i18nz                              # 国际化
├── activitypub                        # 联邦（WebFinger、Inbox/Outbox）
├── matrix / matrix/homeserver         # Matrix 桥接
└── middleware (由 router.go 注入)      # 认证、scope、限流等
```

> **设计要点**：控制器层保持「薄」——仅做请求解析、参数校验、调用 `models`/`feed`/`search` 等领域层，再交由 `serializer` 序列化响应。复杂业务逻辑不放在 handler 中，便于测试与复用（各子模块均配有 `*_test.go`）。
