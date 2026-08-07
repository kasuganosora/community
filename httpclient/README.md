# httpclient (HTTP 客户端)

基于标准库 `net/http` 封装的 HTTP 客户端模块，提供连接池复用、请求超时控制、自动重试（指数退避）和 HTTP 代理支持，并内置链式请求构建器（Builder Pattern）。

整个项目对外的 HTTP 请求均通过本包发出，例如 Matrix 联邦（`/_matrix/key/v2/server`）、验证码供应商校验（Cloudflare Turnstile / reCAPTCHA / hCaptcha）以及媒体下载等。

## 核心功能

| 功能 | 说明 |
|------|------|
| 连接池复用 | 内置 `http.Transport`，`MaxIdleConns=100`、`MaxIdleConnsPerHost=20`、`IdleConnTimeout=90s`，避免频繁建连 |
| 超时控制 | 单次请求级超时（`SetTimeout`，基于 `context.WithTimeout`），到期立即取消 |
| 自动重试 | `SetTryTimes` 设置额外重试次数，状态码非 2xx 或网络错误时触发指数退避重试 |
| 代理支持 | 自动读取 `HTTP_PROXY` 配置；亦可使用 `NewNoProxyClient()` 强制绕过代理 |
| 链式请求构建 | `Request` 暴露链式 Setter，支持 Header / User-Agent / Query / Body / 重试 / 超时 |
| 多种请求体 | 内置 `SetJsonBody`、`SetMultipartBody`、`SetFormUrlencoded`，自动设置 `Content-Type` |
| 便捷响应解析 | `Response` 提供 `Json` / `JsonMap` / `String` / `GetBodyData` / `StatusOk` / `Close` |

## 用法示例

### 基本用法（GET + JSON 解析）

```go
import "github.com/kasuganosora/community/httpclient"

// 创建客户端（默认复用连接池，并按 HTTP_PROXY 启用代理）
client := httpclient.NewClient()

req := client.NewRequest("GET", "https://api.github.com/repos/golang/go").
    SetHeader("Accept", "application/vnd.github+json").
    SetTimeout(10)

resp, err := req.Do()
if err != nil {
    // 重试耗尽或请求失败
    return err
}
defer resp.Close()

if !resp.StatusOk() {
    return fmt.Errorf("unexpected status %d", resp.StatusCode)
}

var repo struct {
    FullName  string `json:"full_name"`
    StarCount int    `json:"stargazers_count"`
}

if err := resp.Json(&repo); err != nil {
    return err
}
```

### POST JSON + 自动重试

```go
client := httpclient.NewClient()

payload := map[string]any{
    "username": "alice",
    "content":  "hello world",
}

resp, err := client.NewRequest("POST", "https://example.com/api/v1/statuses").
    SetHeader("Authorization", "Bearer "+accessToken).
    SetJsonBody(payload).
    SetTimeout(30).
    SetTryTimes(2). // 失败后最多再重试 2 次
    Do()

if err != nil {
    return err
}
defer resp.Close()
```

> 重试采用指数退避：第 i 次重试前等待 `200<<i` 毫秒（200ms、400ms、800ms …）。请求体在每次重试时会被完整重发。

### 文件上传（multipart）

```go
// data 中 key 为字段名，*multipart.FileHeader 与 *os.File 会作为文件上传，
// 其余类型作为普通表单字段
data := map[string]any{
    "description": "avatar",
    "file":        fileHeader, // *multipart.FileHeader
}

resp, err := client.NewRequest("POST", "https://example.com/api/v1/media").
    SetMultipartBody(data).
    SetTimeout(60).
    Do()
```

### 获取原始字节数据

```go
// 适用于二进制内容（图片、媒体文件）
resp, err := client.NewRequest("GET", mediaURL).SetTimeout(15).Do()
if err != nil {
    return err
}
defer resp.Close()

data, err := resp.GetBodyData() // 读取后自动关闭 Body 并缓存，可重复调用
```

### 强制绕过代理

```go
// NewNoProxyClient 不读取 HTTP_PROXY，使用默认 Transport
client := httpclient.NewNoProxyClient()
```

## 配置说明

### 默认常量

| 常量 | 值 | 说明 |
|------|----|------|
| `DefaultTimeout` | `10` | 默认超时（秒），仅作参考常量 |
| `DefaultUA` | `"Community/1.0"` | 默认 User-Agent |
| `DefaultTryTimes` | `0` | 默认重试次数（即不重试） |

### 连接池（`http.Transport`）

`NewClient()` 创建的客户端统一使用以下 Transport 参数：

| 参数 | 值 | 说明 |
|------|----|------|
| `MaxIdleConns` | `100` | 跨 host 的最大空闲连接数 |
| `MaxIdleConnsPerHost` | `20` | 每个 host 的最大空闲连接数 |
| `IdleConnTimeout` | `90s` | 空闲连接超时回收 |
| `Proxy` | `http.ProxyURL(proxyURL)` | 当配置了代理时启用 |

### 代理（环境变量）

代理地址通过配置项 `HTTP_PROXY`（对应 `config.C.HttpProxy`）提供。当其非空时，`NewClient()` 会解析该 URL 并设置为 Transport 的 `Proxy`；解析失败会 panic。

```bash
# .env
HTTP_PROXY=http://127.0.0.1:7890
```

### 超时与重试

| 方法 | 参数 | 说明 |
|------|------|------|
| `Request.SetTimeout(seconds int)` | 秒 | 单次请求超时；`<= 0` 表示不设超时。基于 `context.WithTimeout`，到期立即取消 |
| `Request.SetTryTimes(n int)` | 次数 | 额外重试次数。实际总请求次数 = `1 + n`，指数退避 |

## 核心类型与函数

### 函数

| 函数 | 说明 |
|------|------|
| `NewClient() *Client` | 创建客户端，启用连接池；当 `HTTP_PROXY` 非空时自动启用代理 |
| `NewNoProxyClient() *Client` | 创建不带代理的客户端 |

### 类型

#### `Client`

内嵌 `http.Client`，所有标准库方法可直接使用：

```go
type Client struct {
    http.Client
}
```

#### `Request`

链式请求构建器，方法均返回 `*Request` 以支持链式调用：

| 方法 | 说明 |
|------|------|
| `SetHeader(key, value string)` | 设置单个 Header |
| `AddHeader(key, value string)` | 追加 Header（同一 key 多值） |
| `SetUserAgent(ua string)` | 设置 User-Agent |
| `SetTimeout(timeout int)` | 单次请求超时（秒） |
| `SetUrlParams(key, value string)` / `AddUrlParams(...)` | 设置 / 追加 Query 参数 |
| `SetBody(body []byte)` | 设置原始字节请求体 |
| `SetJsonBody(v any)` | 序列化为 JSON 并设置 `Content-Type: application/json` |
| `SetMultipartBody(data map[string]any)` | 构建 multipart 表单，支持 `*multipart.FileHeader`、`*os.File` 与普通字段 |
| `SetFormUrlencoded(data url.Values)` | 构建 `application/x-www-form-urlencoded` 表单 |
| `SetTryTimes(tryTimes int)` | 设置额外重试次数 |
| `GetMethodAndURL()` / `GetBody()` | 读取已设置的 Method / Body |
| `Do() (*Response, error)` | 发起请求，执行超时与重试逻辑 |

> `NewRequest(method, urlStr)` 会自动解析 URL 并**剥离 query string**，请通过 `SetUrlParams` / `AddUrlParams` 设置查询参数。

#### `Response`

内嵌 `*http.Response`，并提供便捷解析：

| 方法 | 说明 |
|------|------|
| `Json(v any) error` | 读取 Body 并 JSON 反序列化到 `v` |
| `JsonMap() (map[string]any, error)` | 反序列化为 `map[string]any` |
| `String() (string, error)` | 以字符串返回 Body |
| `GetBodyData() ([]byte, error)` | 读取并缓存 Body 字节；首次调用后自动关闭 Body，可重复读取 |
| `StatusOk() bool` | 状态码是否为 2xx |
| `Close()` | 关闭底层 Body；幂等，多次调用安全。仅读取 Header 的场景应主动调用以归还连接 |

## 错误处理与注意事项

- **非 2xx 视为失败**：`Do()` 在状态码不在 2xx 区间时会返回错误（包含状态码），并按 `SetTryTimes` 触发重试。若需要处理 4xx/5xx，请通过 `Do()` 返回的 error 区分，或扩展客户端逻辑。
- **panic 行为**：`NewRequest`、`SetJsonBody`、`SetMultipartBody` 在参数非法（如 URL 解析失败、文件打开失败）时会 panic，调用方应保证入参合法。
- **连接归还**：务必调用 `resp.Close()` 或消费 Body（`GetBodyData` / `Json` / `String` 均会关闭 Body），否则连接不会被回收到连接池。

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`config`](../config/) | 读取 `HTTP_PROXY` 代理配置（`config.C.HttpProxy`） |
| [`log`](../log/) | 重试失败时记录错误日志（`log.ErrorV`） |
| [`utils`](../utils/) | multipart 普通字段转换为字符串（`utils.ToString`） |
| `github.com/pkg/errors` | 错误包装（`WithStack` / `WithMessage`） |

## 使用方

该客户端被以下模块使用：

- [`captcha/providers`](../captcha/providers/) — 各验证码供应商（Cloudflare / reCAPTCHA / hCaptcha）的服务端校验请求
- [`matrix`](../matrix/) — Matrix 客户端与联邦请求（`/_matrix/key/v2/server` 等）
- [`matrix/homeserver`](../matrix/homeserver/) — 联邦密钥获取、媒体下载
