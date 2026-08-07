# utils (工具函数库)

项目的基础工具函数库，提供加密、文件操作、哈希、类型转换、字符串处理、URI 解析、Matrix ID 解析以及多供应商翻译服务。这些函数被几乎所有业务包共享使用。

## 核心功能

| 模块 | 说明 |
|------|------|
| **encryption** | AES-CBC 加密 / 解密、结构体字段级加密、加密 key 派生、PKCS#7 填充 |
| **file** | 文件读取、路径处理、调用栈定位、图片类型判断 |
| **hash** | 泛型哈希计算（支持任意 `hash.Hash` 实现） |
| **types** | 泛型类型转换、深拷贝、集合操作、数组工具 |
| **string** | 加密安全随机字符串、字符串去重 |
| **uri** | URI 解析、ActivityPub / RFC3339 时间处理 |
| **matrix_id** | Matrix user ID / room ID 解析与格式化 |
| **translation** | 多供应商翻译服务（LibreTranslate / DeepL / Google） |

## 架构 / 结构

```
utils/
├── encryption.go    # AES 加解密、结构体加密、key 派生
├── file.go          # 文件 / 路径操作、调用栈定位
├── hash.go          # 泛型哈希计算
├── types.go         # 类型转换、深拷贝、集合 / 数组工具
├── string.go        # 随机字符串、字符串去重
├── uri.go           # URI 解析、时间格式化
├── matrix_id.go     # Matrix ID 解析
└── translation/
    └── translation.go  # 翻译服务（Provider 接口 + 多供应商实现）
```

导入路径：

```
github.com/kasuganosora/community/utils
github.com/kasuganosora/community/utils/translation
```

## 用法示例

### 加密（encryption）

```go
import "github.com/kasuganosora/community/utils"

// 派生加密 key（MD5(sourceKey + salt) 转大写 hex）
key := utils.GetEncryptKey("my-secret", "salt-value")

// AES-CBC 加密（结果为 base64 字符串）
encrypted, err := utils.AesEncrypt([]byte("hello world"), key)

// AES-CBC 解密
decrypted, err := utils.AesDecrypt(encrypted, key)
```

结构体字段级加密——通过 `encrypt:"true"` tag 标记需要加密的字段：

```go
type Secret struct {
    Token string `encrypt:"true"` // 会被加密 / 解密
    Name  string                 // 不受影响
}

s := &Secret{Token: "sensitive-data"}
_ = utils.AesEncryptStruct(s, key)  // 加密：Token 字段被替换为密文
_ = utils.AesDecryptStruct(s, key)  // 解密：Token 字段还原为明文
```

> 加密使用 AES-CBC，IV 从 key 的 SHA-256 摘要前 16 字节截取；`Pkcs7unpadding` 会校验填充完整性以防止 padding oracle 攻击。

### 哈希（hash）

泛型函数，支持任意实现了 `hash.Hash` 的哈希算法，返回 hex 编码摘要：

```go
import (
    "crypto/sha256"
    "crypto/md5"
    "github.com/kasuganosora/community/utils"
)

// SHA-256
h := utils.Hash(sha256.New(), "hello")
// "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

// MD5（同样适用）
m := utils.Hash(md5.New(), []byte("hello"))
```

> `Hash` 在返回前调用 `h.Reset()`，因此传入的 hash 对象可安全复用（如共享的 `sha256.New()`）。

### 文件操作（file）

```go
// 读取文件内容为字符串
content, err := utils.ReadFileToString("config.yaml")

// 判断路径是否存在
if utils.IsFileExists("/path/to/file") {
    // ...
}

// 获取调用者的文件与行号（用于日志）
loc := utils.FileWithLineNum() // "main.go:42"

// 判断是否为图片（按扩展名）
utils.CheckIsImage("photo.jpg") // true
```

### 类型转换（types）

```go
// 基础类型转换（容错，失败返回零值）
utils.ToInt("42")        // 42
utils.ToInt64(123)       // int64(123)
utils.ToFloat64("3.14")  // 3.14
utils.ToBool("true")     // true
utils.ToString(3.14)     // "3.14"

// 字符串写入任意类型指针（基于 reflect）
var n int64
_ = utils.StrToAny("100", &n) // n == 100

// 泛型工具
utils.UniqueSlice([]int{1, 2, 2, 3})       // [1, 2, 3]
utils.Contains([]string{"a", "b"}, "b")     // true
utils.Min([]int{3, 1, 2})                   // 1
utils.Max([]int{3, 1, 2})                   // 3

// 数组映射（按字段建索引）
m := utils.MapByAnyFiled(accounts, func(a Account) uint64 { return a.Id })
// map[uint64]Account

// 深拷贝（通过 reflect 递归复制，含私有字段）
var dst MyStruct
_ = utils.DeepCopy(src, &dst)

// 合并 map
merged := utils.MergeMap(dst, src)
```

### 字符串（string）

```go
// 加密安全随机字符串（crypto/rand）
token := utils.GetRandomString(32)

// 随机 hex（适合 token / secret）
hex := utils.GetRandomHex(40)

// 字符串去重
utils.FilterDuplicateString([]string{"a", "b", "a"})          // ["a", "b"]
utils.FilterDuplicateAndEmptyString([]string{"a", "", "a"})    // ["a"]
```

### URI / 时间（uri）

```go
// 从 URI 提取域名
utils.ExtractDomainFromURI("https://example.com/users/alice") // "example.com"

// 从 ActivityPub URI 提取用户名（支持 /users/x 和 /@x 两种格式）
utils.ExtractUsernameFromURI("https://example.com/users/alice") // "alice"
utils.ExtractUsernameFromURI("https://example.com/@bob")        // "bob"

// 提取 URI 末尾 ID
utils.ExtractIDFromURI("https://example.com/statuses/12345") // "12345"

// RFC3339 时间处理
t := utils.ParseRFC3339("2024-01-01T00:00:00Z")   // *time.Time
s := utils.FormatRFC3339(time.Now())               // string
```

### Matrix ID（matrix_id）

```go
// 解析 Matrix user ID
localpart, server, ok := utils.ParseMatrixUserID("@alice:example.com")
// localpart="alice", server="example.com", ok=true

// 格式化 Matrix user ID
utils.FormatMatrixUserID("alice", "example.com") // "@alice:example.com"

// 解析 Matrix room ID（以 ! 开头）
localpart, server, ok := utils.ParseMatrixRoomID("!room123:example.com")
```

### 翻译（translation）

```go
import "github.com/kasuganosora/community/utils/translation"

// 使用全局服务（通过环境变量配置供应商）
result, err := translation.Translate("Hello", "en", "zh")
// result.Text, result.DetectedSource, result.Provider

lang, err := translation.DetectLanguage("Bonjour") // "fr"

// 获取当前供应商名称
name := translation.GetService().GetProviderName() // "libretranslate"
```

自定义服务实例：

```go
service := translation.NewService()
result, err := service.Translate("Hello", "en", "zh")
```

> 翻译服务通过环境变量 `TRANSLATION_PROVIDER` 选择供应商（`libretranslate` / `deepl` / `google`），默认 `libretranslate`。

## 核心类型与函数

### encryption

| 函数 | 说明 |
|------|------|
| `AesEncrypt(data []byte, key string) (string, error)` | AES-CBC 加密，返回 base64 |
| `AesDecrypt(data string, key string) ([]byte, error)` | AES-CBC 解密 |
| `AesEncryptStruct(data any, key string) error` | 加密结构体中 `encrypt:"true"` 标记的字段 |
| `AesDecryptStruct(data any, key string) error` | 解密结构体中 `encrypt:"true"` 标记的字段 |
| `GetEncryptKey(sourceKey, salt string) string` | 派生加密 key（MD5 + salt，大写 hex） |
| `Pkcs7padding(data, blockSize) []byte` | PKCS#7 填充 |
| `Pkcs7unpadding(data) ([]byte, error)` | PKCS#7 去填充（含完整性校验） |

### file

| 函数 | 说明 |
|------|------|
| `GetSourceDir() string` | 获取源码目录（init 时缓存） |
| `ReadFileToString(filename) (string, error)` | 读取文件为字符串 |
| `FileWithLineNum(level ...int) string` | 获取调用者文件:行号（跳过 utils 内部栈帧） |
| `IsFileExists(path) bool` | 判断路径是否存在 |
| `GetFullPath(path) string` | 转换为绝对路径（相对路径基于 sourceDir） |
| `CheckIsImage(path) bool` | 按扩展名判断是否为图片（jpg/jpeg/png） |

### hash

| 函数 | 说明 |
|------|------|
| `Hash[T HashDriver](h T, data any) string` | 泛型哈希，返回 hex 摘要（支持 `string` / `[]byte`） |

### types

| 函数 | 说明 |
|------|------|
| `ToInt(v any) int` | 转换为 int |
| `ToInt64(v any) int64` | 转换为 int64 |
| `ToFloat64(v any) float64` | 转换为 float64 |
| `ToBool(v any) bool` | 转换为 bool |
| `ToString(v any) string` | 转换为 string |
| `StrToAny(str string, obj any) error` | 字符串写入指针目标（reflect） |
| `UniqueSlice[T](s []T) []T` | 数组去重 |
| `Contains[T](arr, check) bool` | 判断包含 |
| `Min[T](arr) T` / `Max[T](arr) T` | 数组最小 / 最大值 |
| `Filter[T](arr, filter) []T` | 数组过滤 |
| `MapByAnyFiled[T, FieldType](arr, getField) map[FieldType]T` | 按字段建 map |
| `GetStructsMap[k, v](objs, key) map[k]v` | 按 struct 字段名建 map |
| `MergeMap(dst, src) map[string]any` | 合并 map |
| `DeepCopy(source, target) error` | reflect 深拷贝（支持 struct / slice / map / 指针，含私有字段） |
| `NumberArrayConvert[A, B](arr) []B` | 数值数组类型转换 |
| `GetPtr[T](obj T) *T` / `GetPtrIfNotEmpty(s) *string` | 取指针 |

### string

| 函数 | 说明 |
|------|------|
| `GetRandomString(l int) string` | 加密安全随机字符串（`crypto/rand`） |
| `GetRandomHex(length int) string` | 随机 hex 字符串 |
| `FilterDuplicateString(strs) []string` | 去重 |
| `FilterDuplicateAndEmptyString(strs) []string` | 去重并移除空字符串 |

### uri

| 函数 | 说明 |
|------|------|
| `ExtractDomainFromURI(uri) string` | 提取域名 |
| `ExtractUsernameFromURI(uri) string` | 提取 ActivityPub 用户名 |
| `ExtractIDFromURI(uri) string` | 提取末尾 ID |
| `ParseRFC3339(timeStr) *time.Time` | 解析 RFC3339 时间 |
| `ParseRFC3339WithFallback(timeStr, fallback) time.Time` | 解析（失败返回 fallback） |
| `FormatRFC3339(t) string` | 格式化为 RFC3339 |
| `FormatRFC3339Ptr(t *time.Time) string` | 格式化时间指针（nil 返回空串） |

### matrix_id

| 函数 | 说明 |
|------|------|
| `ParseMatrixUserID(userID) (localpart, server string, ok bool)` | 解析 `@user:server` |
| `FormatMatrixUserID(localpart, server) string` | 格式化 user ID |
| `ParseMatrixRoomID(roomID) (localpart, server string, ok bool)` | 解析 `!room:server` |

### translation

#### `Provider`（接口）

```go
type Provider interface {
    Translate(text, sourceLang, targetLang string) (*Result, error)
    DetectLanguage(text string) (string, error)
    Name() string
}
```

#### `Result`

| 字段 | 类型 | 说明 |
|------|------|------|
| `Text` | `string` | 翻译结果 |
| `DetectedSource` | `string` | 检测到的源语言 |
| `Provider` | `string` | 供应商名称 |

#### 供应商

| 供应商 | 环境变量 | 说明 |
|--------|----------|------|
| `LibreTranslateProvider` | `LIBRETRANSLATE_URL`、`LIBRETRANSLATE_API_KEY` | 开源自托管方案（默认） |
| `DeepLProvider` | `DEEPL_API_KEY` | DeepL（简化实现，未配置时回退 mock） |
| `GoogleProvider` | `GOOGLE_TRANSLATE_API_KEY` | Google Translate（简化实现，未配置时回退 mock） |
| `MockProvider` | - | 原样返回文本，用于测试与回退 |

#### 包级函数

| 函数 | 说明 |
|------|------|
| `NewService() *Service` | 创建翻译服务（按环境变量选择供应商） |
| `GetService() *Service` | 获取全局服务实例（`init` 时创建） |
| `Translate(text, sourceLang, targetLang) (*Result, error)` | 使用全局服务翻译 |
| `DetectLanguage(text) (string, error)` | 使用全局服务检测语言 |

## 环境变量配置

翻译服务通过环境变量配置：

```bash
# 翻译供应商: libretranslate, deepl, google（默认 libretranslate）
TRANSLATION_PROVIDER=libretranslate

# LibreTranslate
LIBRETRANSLATE_URL=https://libretranslate.de
LIBRETRANSLATE_API_KEY=your-api-key

# DeepL
DEEPL_API_KEY=your-api-key

# Google Translate
GOOGLE_TRANSLATE_API_KEY=your-api-key
```

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`github.com/pkg/errors`](https://github.com/pkg/errors) | 错误包装（`WithMessagef` 等） |
| [`github.com/kasuganosora/community/log`](../log) | 翻译服务日志（DeepL / Google 未配置时告警） |

> `utils` 包本身仅依赖标准库（`crypto/*`、`reflect`、`net/url`、`strconv` 等）与 `pkg/errors`；`utils/translation` 子包额外依赖 `log` 包与标准库 `net/http`。
