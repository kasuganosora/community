# errors (统一错误处理)

提供统一的错误处理体系，包括预定义错误码、HTTP 状态码映射、错误详情结构体、统一响应封装以及 Gin 错误处理中间件。所有业务层错误通过 `*AppError` 表示，中间件自动注入 RequestID 并按统一格式返回 JSON 响应。

## 核心功能

- **错误码体系**：预定义通用、认证、业务三类错误码（`ErrorCode`），每类对应固定的 HTTP 状态码。
- **HTTP 状态码映射**：预定义错误实例（如 `ErrNotFound`、`ErrUnauthorized`）内置对应的 HTTP 状态码，响应时自动使用。
- **错误详情与链式调用**：通过 `WithDetail()`、`WithRequestID()` 链式附加详情与请求 ID，不丢失已有字段。
- **RequestID 追踪**：错误中间件自动从 Gin context 中读取 `request_id` 并注入到错误响应中，便于全链路追踪。
- **统一响应封装**：`Response` 结构体统一封装成功/失败响应，配套 `Meta` 分页元数据与便捷发送函数。
- **错误包装器**：`ErrorWrapper` 用于将底层 `error` 统一包装为 `*AppError`，并自动携带 RequestID。
- **开发模式脱敏**：生产环境下隐藏内部错误详情，开发模式下输出完整信息。

## 错误码

### 通用错误（1xxx）

| 错误码常量 | `ErrorCode` 值 | 预定义实例 | HTTP 状态码 | 说明 |
|-----------|----------------|-----------|-------------|------|
| `ErrCodeInvalidRequest` | `INVALID_REQUEST` | `ErrInvalidRequest` | 400 | 请求无效 |
| `ErrCodeUnauthorized` | `UNAUTHORIZED` | `ErrUnauthorized` | 401 | 未授权 |
| `ErrCodeForbidden` | `FORBIDDEN` | `ErrForbidden` | 403 | 禁止访问 |
| `ErrCodeNotFound` | `NOT_FOUND` | `ErrNotFound` | 404 | 资源不存在 |
| `ErrCodeMethodNotAllow` | `METHOD_NOT_ALLOWED` | `ErrMethodNotAllow` | 405 | 方法不允许 |
| `ErrCodeConflict` | `CONFLICT` | `ErrConflict` | 409 | 资源冲突 |
| `ErrCodeTooManyRequest` | `RATE_LIMITED` | `ErrRateLimit` | 429 | 请求频率超限 |
| `ErrCodeInternal` | `INTERNAL_ERROR` | `ErrInternal` | 500 | 服务器内部错误 |
| `ErrCodeServiceUnavail` | `SERVICE_UNAVAILABLE` | `ErrServiceUnavail` | 503 | 服务不可用 |

### 认证错误（2xxx）

| 错误码常量 | `ErrorCode` 值 | 预定义实例 | HTTP 状态码 | 说明 |
|-----------|----------------|-----------|-------------|------|
| `ErrCodeInvalidToken` | `INVALID_TOKEN` | `ErrInvalidToken` | 401 | Token 无效 |
| `ErrCodeTokenExpired` | `TOKEN_EXPIRED` | `ErrTokenExpired` | 401 | Token 过期 |
| `ErrCodeInvalidCredential` | `INVALID_CREDENTIAL` | `ErrInvalidCredential` | 401 | 凭证无效 |
| `ErrCodeAccountDisabled` | `ACCOUNT_DISABLED` | `ErrAccountDisabled` | 403 | 账户已禁用 |
| `ErrCodeAccountLocked` | `ACCOUNT_LOCKED` | `ErrAccountLocked` | 403 | 账户已锁定 |

### 业务错误（3xxx）

| 错误码常量 | `ErrorCode` 值 | 预定义实例 | HTTP 状态码 | 说明 |
|-----------|----------------|-----------|-------------|------|
| `ErrCodeValidation` | `VALIDATION_ERROR` | `ErrValidation` | 400 | 参数验证失败 |
| `ErrCodeResourceExhaust` | `RESOURCE_EXHAUSTED` | `ErrResourceExhaust` | 429 | 资源耗尽 |
| `ErrCodeDuplicateEntry` | `DUPLICATE_ENTRY` | `ErrDuplicateEntry` | 409 | 重复条目 |
| `ErrCodeNotImplemented` | `NOT_IMPLEMENTED` | `ErrNotImplemented` | 501 | 功能未实现 |
| `ErrCodeDependencyFail` | `DEPENDENCY_FAILURE` | `ErrDependencyFail` | 503 | 依赖服务故障 |

## 错误详情结构体

### `AppError`

应用错误结构体，实现 `error` 接口，是整个错误处理体系的核心：

| 字段 | 类型 | JSON 标签 | 说明 |
|------|------|-----------|------|
| `Code` | `ErrorCode` | `code` | 错误码 |
| `Message` | `string` | `message` | 错误消息 |
| `Detail` | `string` | `detail,omitempty` | 详细信息（生产环境默认隐藏） |
| `Status` | `int` | `-`（不输出） | HTTP 状态码 |
| `RequestID` | `string` | `request_id,omitempty` | 请求 ID，用于全链路追踪 |
| `InternalID` | `string` | `error_id,omitempty` | 内部错误 ID |

`AppError` 方法：

| 方法 | 说明 |
|------|------|
| `Error() string` | 实现 `error` 接口，格式为 `[CODE] Message: Detail` |
| `WithDetail(detail string) *AppError` | 返回附加详情的新实例，保留已有 RequestID |
| `WithRequestID(requestID string) *AppError` | 返回附加请求 ID 的新实例 |
| `JSON() gin.H` | 返回统一格式的 JSON 响应 map |

### `Response`

统一响应结构体，封装成功与失败响应：

| 字段 | 类型 | JSON 标签 | 说明 |
|------|------|-----------|------|
| `Success` | `bool` | `success` | 是否成功 |
| `Data` | `any` | `data,omitempty` | 响应数据 |
| `Error` | `*AppError` | `error,omitempty` | 错误信息 |
| `Meta` | `*Meta` | `meta,omitempty` | 分页元数据 |

### `Meta`

分页元数据结构体：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Page` | `int` | 当前页码 |
| `PageSize` | `int` | 每页条数 |
| `Total` | `int64` | 总记录数 |
| `TotalPages` | `int` | 总页数 |

### `ErrorWrapper`

错误包装器，用于在业务层统一包装底层 `error`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `RequestID` | `string` | 请求 ID，包装时自动注入 |

## 用法示例

### 在 Gin handler 中返回错误

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/errors"
)

func GetUser(c *gin.Context) {
    user, err := userService.FindByID(id)
    if err != nil {
        errors.SendNotFound(c, "user")
        return
    }
    errors.SendSuccess(c, user)
}
```

### 链式构建自定义错误

```go
// 附加详情与请求 ID
err := errors.ErrForbidden.
    WithDetail("you do not have permission to access this resource").
    WithRequestID(requestID)

errors.SendError(c, err)
```

### 使用便捷构造函数

```go
// 字段验证错误
errors.SendValidationError(c, "email", "invalid email format")

// 资源未找到
errors.SendNotFound(c, "post")

// 直接创建
appErr := errors.NewAppError(errors.ErrCodeConflict, "username already taken", http.StatusConflict)
errors.SendError(c, appErr)
```

### 带分页的成功响应

```go
meta := &errors.Meta{
    Page:       page,
    PageSize:   pageSize,
    Total:      total,
    TotalPages: totalPages,
}

errors.SuccessWithMeta(users, meta).Send(c, http.StatusOK)
```

### 注册错误处理中间件

`ErrorHandler()` 作为 Gin 中间件注册，自动捕获 handler 中通过 `c.Error()` 添加的错误，转换为统一 JSON 响应并注入 RequestID：

```go
router := gin.Default()
router.Use(errors.ErrorHandler()) // 注册统一错误处理中间件
```

中间件行为：
1. 捕获 context 中最后一个错误。
2. 若为 `*AppError`，注入 Gin context 中的 `request_id`。
3. 对 5xx 错误自动记录日志（包含 code、status、path、method、request_id）。
4. 非 `*AppError` 的错误统一转为 `ErrInternal`，生产环境隐藏内部详情。

### 使用 ErrorWrapper 包装底层错误

```go
wrapper := &errors.ErrorWrapper{RequestID: requestID}

// 将数据库错误包装为统一错误
appErr := wrapper.Wrap(dbErr, errors.ErrInternal)
if appErr != nil {
    errors.SendError(c, appErr)
}

// 包装并记录日志
appErr = wrapper.WrapWithLog(dbErr, errors.ErrInternal, "query user failed")
```

### 统一错误响应格式

```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "Resource not found",
    "detail": "user not found",
    "request_id": "req-abc-123"
  }
}
```

> `detail` 字段仅在开发模式（`config.C.DevMode == "true"`）下返回，生产环境默认隐藏。

## 核心类型与函数

### 类型

| 类型 | 说明 |
|------|------|
| `ErrorCode` | 错误码类型（`string` 别名） |
| `AppError` | 应用错误结构体，实现 `error` 接口 |
| `Response` | 统一响应结构体 |
| `Meta` | 分页元数据结构体 |
| `ErrorWrapper` | 错误包装器 |

### 函数

#### 错误创建

| 函数 | 说明 |
|------|------|
| `NewAppError(code ErrorCode, message string, status int) *AppError` | 创建自定义应用错误 |
| `NewValidationError(field, message string) *AppError` | 创建字段验证错误 |
| `NewNotFoundError(resource string) *AppError` | 创建资源未找到错误 |

#### 响应构造与发送

| 函数 | 说明 |
|------|------|
| `Success(data any) *Response` | 构造成功响应 |
| `SuccessWithMeta(data any, meta *Meta) *Response` | 构造带分页的成功响应 |
| `Fail(err *AppError) *Response` | 构造失败响应 |
| `SendSuccess(c *gin.Context, data any)` | 发送成功响应（200） |
| `SendCreated(c *gin.Context, data any)` | 发送创建成功响应（201） |
| `SendNoContent(c *gin.Context)` | 发送无内容响应（204） |
| `SendError(c *gin.Context, err *AppError)` | 发送错误响应（使用 `err.Status`） |
| `SendValidationError(c *gin.Context, field, message string)` | 发送验证错误（400） |
| `SendNotFound(c *gin.Context, resource string)` | 发送未找到错误（404） |

#### 错误处理与转换

| 函数 | 说明 |
|------|------|
| `ErrorHandler() gin.HandlerFunc` | 统一错误处理中间件 |
| `AsAppError(err error, target **AppError) bool` | 将 `error` 转换为 `*AppError`，支持解包 `gin.Error` |

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`github.com/gin-gonic/gin`](https://github.com/gin-gonic/gin) | HTTP 框架，中间件、context、JSON 响应 |
| `github.com/kasuganosora/community/config` | 读取 `DevMode` 控制详情脱敏 |
| `github.com/kasuganosora/community/log` | 5xx 错误与未处理错误的日志记录 |
| 标准库 `encoding/json` | `Response` 的 JSON 序列化 |
| 标准库 `errors` | 错误类型断言（`errors.As`） |
| 标准库 `net/http` | HTTP 状态码常量 |
